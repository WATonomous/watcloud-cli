package cmd

import (
	"github.com/spf13/cobra"
)

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Manage the rootless Docker daemon",
}

func init() {
	rootCmd.AddCommand(dockerCmd)
}
