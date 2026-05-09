package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/setup"
	"github.com/spf13/cobra"
)

var (
	cfgFile        string
	vibecopCfg     config.Config
	vibecopCfgPath string
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
		vibecopCfgPath = path

		// First-run: auto-launch setup for interactive commands only.
		if cfgFile == "" {
			if _, staterr := os.Stat(path); os.IsNotExist(staterr) {
				if shouldTriggerSetup(cmd.Name()) {
					fmt.Fprintf(os.Stderr, "vibecop: no configuration found\n\n")
					if _, err := setup.Run(); err != nil {
						return fmt.Errorf("setup: %w", err)
					}
					// Post-setup: offer hooks, test, next-steps.
					// Auto-trigger has no flag plumbing — fall back to the
					// default `vibecop hook` (PATH-based) command. Users who
					// need a custom binary path can re-run `vibecop install
					// --vibecop-path …` afterwards.
					postSetup(path, "")
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

// VibeCopConfigPath returns the absolute path of the loaded config.toml.
// Empty string until PersistentPreRunE has resolved it. Used by surfaces
// that need to reference or edit the live config file (e.g. the TUI's
// view/edit pane).
func VibeCopConfigPath() string {
	return vibecopCfgPath
}

// shouldTriggerSetup returns true if the named command should launch the
// interactive setup wizard when no config file is present. Commands that
// run non-interactively (hook is called by the harness, stop/status may be
// called from scripts) must never block waiting for stdin.
func shouldTriggerSetup(cmdName string) bool {
	switch cmdName {
	case "hook", "setup", "help", "completion", "stop", "status":
		return false
	}
	return true
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
