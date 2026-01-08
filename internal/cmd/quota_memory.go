package cmd

import (
	"fmt"

	"watcloud-cli/internal/quota"

	"github.com/spf13/cobra"
)

var quotaMemoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Shows memory usage statistics.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := quota.MemoryUsage(); err != nil {
			fmt.Println("Error:", err)
		}
	},
}

func init() {
	quotaCmd.AddCommand(quotaMemoryCmd)
}
