package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bnaylor/vibecop/internal/hooks"
	"github.com/spf13/cobra"
)

var (
	installHarness     string
	installAll         bool
	installVibecopPath string
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install hook scripts into coding harness configs",
	Long: `Install vibecop hook scripts into the specified harness.
Use --harness to target one (claude, gemini, codex, copilot) or --all for all
supported harnesses.

By default the hook calls "vibecop hook" and relies on $PATH to find it. Pass
--vibecop-path to point the hook at a specific binary instead — useful when
testing a local build without overwriting the system install.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets := resolveInstallTargets()
		if len(targets) == 0 {
			return fmt.Errorf("specify --harness or --all")
		}

		vibecopPath, err := resolveVibecopPath(installVibecopPath)
		if err != nil {
			return err
		}

		for _, h := range targets {
			if err := hooks.InstallHooks(h, vibecopPath); err != nil {
				fmt.Fprintf(os.Stderr, "vibecop: %s install failed: %v\n", h, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "vibecop: installed hook for %s\n", h)
			if note := harnessInstallNote(h); note != "" {
				fmt.Fprintf(os.Stderr, "  note: %s\n", note)
			}
		}
		return nil
	},
}

// harnessInstallNote returns a one-line operator-facing note about a
// harness-specific limitation surfaced at install time. Keep it short —
// the hook also emits a runtime hint via emitHintOnce for the same case.
func harnessInstallNote(harness string) string {
	if harness == hooks.HarnessCopilot {
		return `Copilot does not currently honor permissionDecision="allow"; vibecop's approve verdict is informational only. Use ` + "`/allow-all on`" + ` inside Copilot for harness-side auto-approval — vibecop deny still blocks.`
	}
	return ""
}

// resolveVibecopPath turns an optional --vibecop-path flag value into the
// string the hook command should embed. Empty means "use $PATH" (the hook
// is written as "vibecop hook"); a non-empty value is resolved to absolute
// so the hook works regardless of the agent's CWD.
func resolveVibecopPath(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve --vibecop-path %q: %w", raw, err)
	}
	return abs, nil
}

func resolveInstallTargets() []string {
	if installHarness != "" {
		return []string{installHarness}
	}
	if installAll {
		return []string{
			hooks.HarnessClaude,
			hooks.HarnessGemini,
			hooks.HarnessCodex,
			hooks.HarnessCopilot,
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().StringVar(&installHarness, "harness", "", "Harness to install into (claude|gemini|codex|copilot) — use 'claude' for any claude-compatible wrapper")
	installCmd.Flags().BoolVar(&installAll, "all", false, "Install into all supported harnesses")
	installCmd.Flags().StringVar(&installVibecopPath, "vibecop-path", "", "Path to a specific vibecop binary the hook should call (default: 'vibecop' via $PATH). Resolved to absolute.")
}
