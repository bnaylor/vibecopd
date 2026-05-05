package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var refineHarness string

var refineCmd = &cobra.Command{
	Use:   "refine",
	Short: "Regenerate the Guardian prompt for the current project",
	Long:  "Re-run initialization with the current system prompt and recent activity as context.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop refine: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(refineCmd)
	refineCmd.Flags().StringVar(&refineHarness, "harness", "", "Agent to use for prompt regeneration (claude|gemini|deepseek)")
	refineCmd.MarkFlagRequired("harness")
}
