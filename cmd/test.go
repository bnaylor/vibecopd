package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a probe request to the configured endpoint",
	Long:  "Test the LLM endpoint with a minimal probe request and report latency.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop test: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(testCmd)
}
