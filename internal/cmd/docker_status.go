package cmd

import (
	"fmt"
	"watcloud-cli/internal/docker"

	"github.com/spf13/cobra"
)

var dockerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Lists all non-interactive background user processes (daemons).",
	Run: func(cmd *cobra.Command, args []string) {
		result, _ := docker.ListDaemons()
		fmt.Print(result)
		fmt.Println()
	},
}

func init() {
	dockerCmd.AddCommand(dockerStatusCmd)
}
