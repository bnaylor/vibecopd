package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	installHarness string
	installAll     bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install hook scripts into coding harness configs",
	Long: `Install vibecop hook scripts into the specified harness.
Use --harness to target one (claude, gemini, deepseek) or --all for all detected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("vibecop install: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().StringVar(&installHarness, "harness", "", "Harness to install into (claude|gemini|deepseek)")
	installCmd.Flags().BoolVar(&installAll, "all", false, "Install into all detected harnesses")
}
