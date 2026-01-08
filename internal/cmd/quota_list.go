package cmd

import (
	"fmt"

	"watcloud-cli/internal/quota"

	"github.com/spf13/cobra"
)

var quotaListCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists all quota usage (disk, memory, CPU).",
	Run: func(cmd *cobra.Command, args []string) {
		err1 := quota.CPUUsage()
		err2 := quota.DiskUsage()
		err3 := quota.MemoryUsage()
		if err1 != nil || err2 != nil || err3 != nil {
			fmt.Println("Error getting quota info:")
			if err1 != nil {
				fmt.Println("CPU:", err1)
			}
			if err2 != nil {
				fmt.Println("Disk:", err2)
			}
			if err3 != nil {
				fmt.Println("Memory:", err3)
			}
		}
	},
}

func init() {
	quotaCmd.AddCommand(quotaListCmd)
}
