package cmd

import (
	"fmt"
	"os"
	"watcloud-cli/internal/docker"

	"github.com/spf13/cobra"
)

var dockerStartCmd = &cobra.Command{
	Use:     "start",
	Aliases: []string{"run"},
	Short: "Starts the rootless Docker Daemon.",
	Long:  "Starts the rootless Docker Daemon using slurm-start-dockerd.sh.\n\n" +
		"Scratch disk (/tmp, where Docker stores images) is fixed when the job is submitted.\n" +
		"Request it then, e.g. srun --gres tmpdisk:<MiB> --pty bash",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {

		if err := docker.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	dockerCmd.AddCommand(dockerStartCmd)
}
