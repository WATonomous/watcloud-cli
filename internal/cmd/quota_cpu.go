package cmd

import (
	"fmt"

	"watcloud-cli/internal/quota"

	"github.com/spf13/cobra"
)

var quotaCpuCmd = &cobra.Command{
	Use:   "cpu",
	Short: "Displays CPU usage percentage.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := quota.CPUUsage(); err != nil {
			fmt.Println("Error:", err)
		}
	},
}

func init() {
	quotaCmd.AddCommand(quotaCpuCmd)
}
