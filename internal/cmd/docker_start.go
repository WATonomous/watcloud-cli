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
	Short:   "Starts the rootless Docker Daemon.",
	Long: "Starts the rootless Docker Daemon using slurm-start-dockerd.sh. " +
		"Docker uses the job's scratch disk, which can't be resized once the job has started. " +
		"Request it when you submit the job with --gres tmpdisk:<MiB>.",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("%s no longer takes a disk size; request scratch disk when you submit the job with --gres tmpdisk:<MiB>", cmd.CommandPath())
		}
		return nil
	},
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
