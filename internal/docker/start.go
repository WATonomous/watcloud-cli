package docker

import (
	"fmt"
	"os"
	"os/exec"
)

// Starts the rootless Docker daemon using slurm-start-dockerd.sh
// diskSize is optional - if provided, it will be used with --gres tmpdisk flag
func Start(diskSize string) error {
	cmdArgs := []string{}

	// If disk size argument is provided, add the --gres flag
	if diskSize != "" {
		cmdArgs = append(cmdArgs, "--gres", fmt.Sprintf("tmpdisk:%s", diskSize))
	}

	// Run slurm-start-dockerd.sh
	cmd := exec.Command("slurm-start-dockerd.sh", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start Docker daemon: %w", err)
	}

	return nil
}
