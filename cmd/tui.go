package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Attach the TUI to a running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop tui: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
