package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/hooks"
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

		postSetup(result.ConfigPath)
		return nil
	},
}

// postSetup offers hook installation, test, and next-steps after config is created.
func postSetup(configPath string) {
	var err error
	vibecopCfg, err = config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reload config: %v\n", err)
		return
	}

	fmt.Fprintln(os.Stderr)

	// Offer to install hooks.
	if confirm("Install hooks into Claude Code and Gemini CLI?") {
		for _, h := range []string{hooks.HarnessClaude, hooks.HarnessGemini} {
			if err := hooks.InstallHooks(h); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: %v\n", h, err)
			} else {
				fmt.Fprintf(os.Stderr, "  installed hook for %s\n", h)
			}
		}
	}

	// Offer to test connection.
	if confirm("Test the connection now?") {
		// Run test logic directly (avoid cyclic ref to setupCmd).
		testCmd.RunE(nil, nil)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Next: 'vibecop start' to boot the daemon, or 'vibecop tui' to watch it run.\n")
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
