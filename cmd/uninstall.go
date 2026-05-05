package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var uninstallHarness string

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove installed hook scripts",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop uninstall: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().StringVar(&uninstallHarness, "harness", "", "Harness to remove from (claude|gemini|deepseek)")
}
