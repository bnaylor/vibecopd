package cmd

import (
	"fmt"
	"os"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/hooks"
	"github.com/bnaylor/vibecop/internal/setup"
	"github.com/spf13/cobra"
)

var setupVibecopPath string

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Run the interactive first-time setup wizard",
	Long: `Walk through configuring vibecop for the first time.
Creates ~/.vibecop/config.toml with your LLM provider, model,
timeout, and other settings. Also offers to install hooks and
test the connection.

Pass --vibecop-path to install hooks that call a specific vibecop
binary instead of the one on $PATH. Useful when testing a local
build without overwriting the system install.`,
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

		vibecopPath, err := resolveVibecopPath(setupVibecopPath)
		if err != nil {
			return err
		}

		result, err := setup.Run()
		if err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}

		postSetup(result.ConfigPath, vibecopPath)
		return nil
	},
}

// postSetup offers hook installation, test, and next-steps after config is created.
func postSetup(configPath, vibecopPath string) {
	var err error
	vibecopCfg, err = config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reload config: %v\n", err)
		return
	}

	fmt.Fprintln(os.Stderr)

	// Offer to install hooks.
	if confirm("Install hooks into Claude Code and Gemini CLI?") {
		if vibecopPath != "" {
			fmt.Fprintf(os.Stderr, "  using vibecop binary: %s\n", vibecopPath)
		}
		for _, h := range []string{hooks.HarnessClaude, hooks.HarnessGemini} {
			if err := hooks.InstallHooks(h, vibecopPath); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: %v\n", h, err)
			} else {
				fmt.Fprintf(os.Stderr, "  installed hook for %s\n", h)
			}
		}
	}

	// Offer to test connection.
	if confirm("Test the connection now?") {
		testCmd.RunE(testCmd, nil)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Next: 'vibecop start' to boot the daemon, or 'vibecop tui' to watch it run.\n")
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupVibecopPath, "vibecop-path", "", "Path to a specific vibecop binary the installed hook should call (default: 'vibecop' via $PATH). Resolved to absolute.")
}
