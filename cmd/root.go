package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
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

		// First-run detection: if no config exists and the user didn't
		// run 'setup', nudge them.
		if cfgFile == "" {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				if cmd.Name() != "setup" && cmd.Name() != "help" && cmd.Name() != "completion" {
					fmt.Fprintf(os.Stderr, "vibecop: no configuration found at %s\n", path)
					fmt.Fprintf(os.Stderr, "  Run 'vibecop setup' to configure your endpoint.\n")
					fmt.Fprintf(os.Stderr, "  Or create the file manually.\n\n")
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
