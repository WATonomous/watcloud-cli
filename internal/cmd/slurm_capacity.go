package cmd

import (
	"fmt"
	"os"
	"watcloud-cli/internal/slurm"

	"github.com/spf13/cobra"
)

var slurmCapacityCmd = &cobra.Command{
	Use:   "capacity",
	Short: "Show SLURM cluster capacity (partitions, nodes, and active jobs)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := slurm.ShowCapacity(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	slurmCmd.AddCommand(slurmCapacityCmd)
}