package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/bnaylor/vibecop/internal/hooks"
	"github.com/spf13/cobra"
)

var hookHarness string

const hookTimeout = 3 * time.Second

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook entry point (called by installed scripts)",
	Long: `Reads a harness permission request from stdin, sends it to the daemon,
and exits with the verdict code (0 = approve, 1 = deny/escalate).

Auto-detects the harness format (Claude Code or Gemini CLI) from the
payload shape. Override with --harness.

If the daemon is unreachable, exits 0 silently (fail-open).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse the harness payload from stdin.
		nr, detected, err := hooks.DetectAndParse(os.Stdin, hookHarness)
		if err != nil {
			// If we can't parse the payload, fail-open.
			fmt.Fprintf(os.Stderr, "VibeCop: %v\n", err)
			os.Exit(1)
		}

		_ = detected // harness identity available for logging if needed

		// Build the daemon request.
		req := daemon.Request{
			Type:        daemon.TypePermissionRequest,
			ProjectPath: nr.ProjectPathResolved(),
			Tool:        nr.Tool,
			Input:       nr.Input,
		}

		// Connect to the daemon.
		vibecopDir, err := config.VibecopDir()
		if err != nil {
			// Can't determine socket path — fail-open.
			os.Exit(0)
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		conn, err := net.DialTimeout("unix", socketPath, hookTimeout)
		if err != nil {
			// Daemon unreachable — fail-open, exit 0 silently.
			os.Exit(0)
		}
		defer conn.Close()

		// Send the request.
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			// Send failed — fail-open.
			os.Exit(0)
		}

		// Read the verdict.
		var resp daemon.Verdict
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			// No response — fail-open.
			os.Exit(0)
		}

		// Apply the exit code contract.
		switch resp.Verdict {
		case "approve":
			os.Exit(0)
		case "deny":
			fmt.Fprintf(os.Stderr, "VibeCop [DENY]: %s\n", resp.Reason)
			os.Exit(1)
		case "escalate":
			fmt.Fprintf(os.Stderr, "VibeCop [ESCALATE]: %s\n", resp.Reason)
			os.Exit(1)
		default:
			// Unknown verdict — fail-open.
			os.Exit(0)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.Flags().StringVar(&hookHarness, "harness", "", "Harness format override (claude|gemini)")
}
