package cmd

import (
	"github.com/spf13/cobra"
)

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Manage and inspect Docker daemons",
}

func init() {
	rootCmd.AddCommand(dockerCmd)
}
