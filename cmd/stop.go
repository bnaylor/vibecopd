package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		vibecopDir, err := config.VibecopDir()
		if err != nil {
			return err
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		pid, err := daemon.ReadPID(socketPath)
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}

		if !daemon.ProcessExists(pid) {
			fmt.Fprintf(os.Stderr, "vibecop: pid %d not found (stale PID file)\n", pid)
			os.Remove(daemon.PIDPath(socketPath))
			os.Remove(socketPath)
			return nil
		}

		p, _ := os.FindProcess(pid)
		if err := p.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal daemon: %w", err)
		}

		fmt.Fprintf(os.Stderr, "vibecop: sent SIGTERM to pid %d\n", pid)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
