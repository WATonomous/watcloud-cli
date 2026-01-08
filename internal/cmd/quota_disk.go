package cmd

import (
	"fmt"

	"watcloud-cli/internal/quota"

	"github.com/spf13/cobra"
)

var quotaDiskCmd = &cobra.Command{
	Use:   "disk",
	Short: "Shows your disk usage percentage and free space.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := quota.DiskUsage(); err != nil {
			fmt.Println("Error:", err)
		}
	},
}

func init() {
	quotaCmd.AddCommand(quotaDiskCmd)
}
