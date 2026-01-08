package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "watcloud",
	Short: "WATcloud CLI: Inspect resource usage and daemon status",
	Long:  `WATcloud CLI is a tool to monitor WATcloud resource usage and daemon status.`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
