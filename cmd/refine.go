package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/evaluator"
	"github.com/spf13/cobra"
)

var refineHarness string

var refineCmd = &cobra.Command{
	Use:   "refine",
	Short: "Regenerate the Guardian prompt for the current project",
	Long:  "Re-run initialization with the current system prompt and recent activity as context.",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		projectHash := config.ProjectHash(projectPath)
		projectDir, err := config.EnsureProjectDir(projectHash)
		if err != nil {
			return fmt.Errorf("project dir: %w", err)
		}

		promptPath := filepath.Join(projectDir, "system-prompt.md")

		// Read current prompt.
		currentPrompt, err := os.ReadFile(promptPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no Guardian prompt found for this project — run 'vibecop init' first")
			}
			return fmt.Errorf("read current prompt: %w", err)
		}

		// Read recent activity.
		activityPath := filepath.Join(projectDir, "activity.jsonl")
		activityData := ""
		if data, err := os.ReadFile(activityPath); err == nil {
			activityData = string(data)
		}

		extraContext := evaluator.RefineContext(string(currentPrompt), activityData)

		fmt.Fprintf(os.Stderr, "vibecop: refining Guardian prompt using %s...\n", refineHarness)
		generated, err := evaluator.GeneratePrompt(refineHarness, extraContext)
		if err != nil {
			return fmt.Errorf("refinement failed: %w", err)
		}

		// Show for review.
		fmt.Fprintf(os.Stderr, "\n=== Refined Guardian Prompt ===\n\n")
		fmt.Fprintln(os.Stderr, generated)
		fmt.Fprintf(os.Stderr, "\n================================\n\n")

		if !confirm("Save this refined prompt?") {
			fmt.Fprintln(os.Stderr, "vibecop: cancelled")
			return nil
		}

		if err := os.WriteFile(promptPath, []byte(generated), 0644); err != nil {
			return fmt.Errorf("write refined prompt: %w", err)
		}

		fmt.Fprintf(os.Stderr, "vibecop: Guardian prompt updated at %s\n", promptPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(refineCmd)
	refineCmd.Flags().StringVar(&refineHarness, "harness", "", "Agent to use for prompt regeneration (claude|gemini)")
	refineCmd.MarkFlagRequired("harness")
}
