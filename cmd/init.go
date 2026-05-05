package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/evaluator"
	"github.com/spf13/cobra"
)

var (
	initHarness string
	initDryRun  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Guardian mode for the current project",
	Long: `Analyze the current project and generate a Guardian prompt.
An agent must be specified with --harness (claude, gemini).
Use --dry-run to preview without saving.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		projectHash := config.ProjectHash(projectPath)

		// Check for .skip-init.
		skipPath, err := config.SkipInitPath(projectHash)
		if err == nil {
			if _, err := os.Stat(skipPath); err == nil {
				return fmt.Errorf("project has .skip-init — Guardian mode cannot be initialized")
			}
		}

		// Generate the Guardian prompt.
		fmt.Fprintf(os.Stderr, "vibecop: generating Guardian prompt using %s...\n", initHarness)
		generated, err := evaluator.GeneratePrompt(initHarness, "")
		if err != nil {
			return fmt.Errorf("prompt generation failed: %w", err)
		}

		if initDryRun {
			fmt.Println(generated)
			return nil
		}

		// Show for review.
		fmt.Fprintf(os.Stderr, "\n=== Generated Guardian Prompt ===\n\n")
		fmt.Fprintln(os.Stderr, generated)
		fmt.Fprintf(os.Stderr, "\n================================\n\n")

		if !confirm("Save this Guardian prompt?") {
			fmt.Fprintln(os.Stderr, "vibecop: cancelled")
			return nil
		}

		// Ensure project storage directory exists.
		projectDir, err := config.EnsureProjectDir(projectHash)
		if err != nil {
			return fmt.Errorf("create project dir: %w", err)
		}

		promptPath := filepath.Join(projectDir, "system-prompt.md")
		if err := os.WriteFile(promptPath, []byte(generated), 0644); err != nil {
			return fmt.Errorf("write system prompt: %w", err)
		}

		fmt.Fprintf(os.Stderr, "vibecop: Guardian prompt saved to %s\n", promptPath)
		return nil
	},
}

func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [Y/n] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initHarness, "harness", "", "Agent to use for prompt generation (claude|gemini)")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Print generated prompt without saving")
	initCmd.MarkFlagRequired("harness")
}
