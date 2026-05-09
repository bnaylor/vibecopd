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
and emits the harness-native JSON response on stdout.

Auto-detects the harness format (Claude Code, Codex, Gemini CLI, or Copilot
CLI) from the payload shape. Override with --harness.

If the daemon is unreachable, exits 0 silently (fail-open).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse the harness payload from stdin.
		nr, detected, err := hooks.DetectAndParse(os.Stdin, hookHarness)
		if err != nil {
			// Fail-open: can't parse the payload, no JSON, exit 0.
			fmt.Fprintf(os.Stderr, "VibeCop: %v\n", err)
			os.Exit(0)
		}

		// Build the daemon request, including which harness/event we saw so
		// the evaluator's telemetry and audit log can record them.
		req := daemon.Request{
			Type:        daemon.TypePermissionRequest,
			ProjectPath: nr.ProjectPathResolved(),
			Tool:        nr.Tool,
			Input:       nr.Input,
			Harness:     detected,
			HookEvent:   nr.Event,
		}

		// Connect to the daemon.
		vibecopDir, err := config.VibecopDir()
		if err != nil {
			os.Exit(0) // fail-open
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		conn, err := net.DialTimeout("unix", socketPath, hookTimeout)
		if err != nil {
			// Daemon unreachable — fail-open, no JSON, exit 0.
			os.Exit(0)
		}
		defer conn.Close()

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			os.Exit(0)
		}

		var resp daemon.Verdict
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			os.Exit(0)
		}

		// Treat daemon-reported "error" as escalate for harness output: the
		// evaluator failed but vibecop wants the harness's normal flow to run.
		if resp.Verdict == "error" {
			resp.Verdict = "escalate"
		}

		os.Exit(hooks.WriteVerdict(detected, nr.Event, resp, os.Stdout, os.Stderr))
		return nil // unreachable; cobra signature requires it
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.Flags().StringVar(&hookHarness, "harness", "", "Harness format override (claude|gemini|codex|copilot)")
}
