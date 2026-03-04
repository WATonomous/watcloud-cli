package cmd

import "github.com/spf13/cobra"

var slurmCmd = &cobra.Command{
	Use:   "slurm",
	Short: "SLURM cluster utilities",
}

func init() {
	rootCmd.AddCommand(slurmCmd)
}