package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/setup"
	"github.com/spf13/cobra"
)

var (
	cfgFile   string
	vibecopCfg config.Config
)

var rootCmd = &cobra.Command{
	Use:   "vibecop",
	Short: "AI second-opinion daemon for coding agent permission checks",
	Long: `vibecop is a daemon that reviews tool-use requests from coding agents
and provides fast, independent approve/deny/escalate verdicts.
Runs in the background; attach the TUI to monitor activity.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		path := cfgFile
		if path == "" {
			var err error
			path, err = config.DefaultConfigPath()
			if err != nil {
				return err
			}
		}
		var err error
		vibecopCfg, err = config.Load(path)
		if err != nil {
			return err
		}

		// First-run: auto-launch setup for any command that needs config.
		if cfgFile == "" {
			if _, staterr := os.Stat(path); os.IsNotExist(staterr) {
				name := cmd.Name()
				if name != "setup" && name != "help" && name != "completion" {
					fmt.Fprintf(os.Stderr, "vibecop: no configuration found\n\n")
					if _, err := setup.Run(); err != nil {
						return fmt.Errorf("setup: %w", err)
					}
					// Post-setup: offer hooks, test, next-steps.
					postSetup(path)
				}
			}
		}
		return nil
	},
}

// VibeCopConfig returns the loaded configuration.
// Only valid after PersistentPreRunE has run.
func VibeCopConfig() config.Config {
	return vibecopCfg
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.vibecop/config.toml)")
}
