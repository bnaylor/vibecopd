package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := VibeCopConfig()
		vibecopDir, err := config.VibecopDir()
		if err != nil {
			return err
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		// Check daemon liveness.
		pid, pidErr := daemon.ReadPID(socketPath)
		var running bool
		if pidErr == nil {
			running = daemon.ProcessExists(pid)
		}

		fmt.Printf("Daemon:\n")
		if running {
			fmt.Printf("  Status:   running (pid %d)\n", pid)
		} else {
			fmt.Printf("  Status:   stopped\n")
		}
		fmt.Printf("  Socket:   %s\n", socketPath)

		model := cfg.Model
		fmt.Printf("\nModel:\n")
		fmt.Printf("  Endpoint:  %s\n", model.Endpoint)
		fmt.Printf("  Format:    %s\n", model.APIFormat)
		fmt.Printf("  Model:     %s\n", model.Model)
		if model.APIKey != "" {
			fmt.Printf("  API Key:   %s\n", model.APIKey[:min(len(model.APIKey), 8)]+"...")
		}

		daemonCfg := cfg.Daemon
		fmt.Printf("\nSettings:\n")
		fmt.Printf("  Timeout:       %d ms\n", daemonCfg.TimeoutMs)
		fmt.Printf("  Activity win:  %d\n", daemonCfg.ActivityWindow)
		fmt.Printf("  Audit:         %v\n", daemonCfg.AuditEnabled)

		if !running {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

