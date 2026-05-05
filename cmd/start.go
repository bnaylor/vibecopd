package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the background daemon",
	Long:  "Start the vibecop daemon. Runs in the foreground; send SIGTERM or use 'vibecop stop' to shut down.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := VibeCopConfig()

		vibecopDir, err := config.VibecopDir()
		if err != nil {
			return err
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		d := daemon.New(socketPath, cfg)
		d.OnPermission(defaultPermissionHandler)

		if err := d.Start(); err != nil {
			return fmt.Errorf("daemon start: %w", err)
		}

		fmt.Fprintf(os.Stderr, "vibecop: daemon started (pid %d)\n", os.Getpid())
		return d.Run()
	},
}

func defaultPermissionHandler(req daemon.Request) daemon.Verdict {
	// Placeholder — step 4 will wire the LLM evaluator here.
	// For now, escalate everything to the human.
	return daemon.Verdict{
		Verdict: "escalate",
		Reason:  "VibeCop: evaluator not yet implemented",
	}
}

func init() {
	rootCmd.AddCommand(startCmd)
}
