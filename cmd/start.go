package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the background daemon",
	Long:  "Start the vibecop daemon (detaches from the terminal).",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop start: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
