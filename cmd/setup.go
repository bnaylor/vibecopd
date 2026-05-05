package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/setup"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Run the interactive first-time setup wizard",
	Long: `Walk through configuring vibecop for the first time.
Creates ~/.vibecop/config.toml with your LLM provider, model,
timeout, and other settings. Also offers to install hooks and
test the connection.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// If config already exists, warn and confirm.
		if path, ok := setup.ConfigPath(); ok {
			fmt.Fprintf(os.Stderr, "Configuration already exists at %s\n\n", path)
			setup.ShowConfig(path)
			if !confirm("Overwrite?") {
				fmt.Fprintln(os.Stderr, "vibecop: cancelled")
				return nil
			}
		}

		result, err := setup.Run()
		if err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\nWhat's next?\n")
		fmt.Fprintf(os.Stderr, "  1. Run 'vibecop test' to verify the endpoint works\n")
		fmt.Fprintf(os.Stderr, "  2. Run 'vibecop start' to start the daemon\n")
		fmt.Fprintf(os.Stderr, "  3. Run 'vibecop install --all' to wire hooks into your coding agents\n")
		fmt.Fprintf(os.Stderr, "  4. Run 'vibecop tui' to watch decisions in real-time\n")

		// Offer to run test.
		if confirm("Run 'vibecop test' now?") {
			vibecopCfg, err = config.Load(result.ConfigPath)
			if err == nil {
				testCmd.RunE(cmd, args)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
