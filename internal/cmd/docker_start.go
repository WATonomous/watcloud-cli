package cmd

import (
	"fmt"
	"os"
	"watcloud-cli/internal/docker"

	"github.com/spf13/cobra"
)

var dockerStartCmd = &cobra.Command{
	Use:     "start [disk_size_MiB]",
	Aliases: []string{"run"},
	Short:   "Starts the rootless Docker Daemon.",
	Long:    "Starts the rootless Docker Daemon using slurm-start-dockerd.sh. Optionally specify disk size in MiB.",
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		diskSize := ""
		if len(args) > 0 {
			diskSize = args[0]
		}

		if err := docker.Start(diskSize); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	dockerCmd.AddCommand(dockerStartCmd)
}
