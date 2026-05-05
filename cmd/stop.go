package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop stop: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
