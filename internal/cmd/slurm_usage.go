package cmd

import (
	"fmt"
	"os"
	"watcloud-cli/internal/slurm"

	"github.com/spf13/cobra"
)

var slurmUsageAll bool

var slurmUsageCmd = &cobra.Command{
	Use:   "usage [job_id]",
	Short: "Show GPU memory and utilisation against what the job requested",
	Long: "Show a job's peak GPU memory and utilisation next to the GPU memory it requested. " +
		"Without a job ID, shows the current job inside a job, or your GPU jobs from the last 7 days. " +
		"With --all, shows every user's GPU jobs from the last 7 days that went over their request (Slurm admins only).",
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		jobID := ""
		if len(args) == 1 {
			jobID = args[0]
		}
		if slurmUsageAll && jobID != "" {
			fmt.Fprintln(os.Stderr, "Error: --all does not take a job ID")
			os.Exit(1)
		}
		if err := slurm.ShowGPUUsage(jobID, slurmUsageAll); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	slurmUsageCmd.Flags().BoolVar(&slurmUsageAll, "all", false, "show every user's jobs that went over their request")
	slurmCmd.AddCommand(slurmUsageCmd)
}
