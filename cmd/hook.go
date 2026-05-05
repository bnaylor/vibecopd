package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook entry point (called by installed scripts)",
	Long:  "Reads a harness permission request from stdin, sends it to the daemon, and exits with the verdict code.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop hook: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
