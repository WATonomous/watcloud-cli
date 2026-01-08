package cmd

import (
	"watcloud-cli/internal/status"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of the cluster (up/down/maintenance)",
	Run: func(cmd *cobra.Command, args []string) {
		status.GetClusterStatus()
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
