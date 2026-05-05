package cmd

import (
	"fmt"

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
An agent must be specified with --harness (claude, gemini, deepseek).
Use --dry-run to preview without saving.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop init: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initHarness, "harness", "", "Agent to use for prompt generation (claude|gemini|deepseek)")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Print generated prompt without saving")
	initCmd.MarkFlagRequired("harness")
}
