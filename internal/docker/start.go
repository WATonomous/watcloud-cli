package docker

import (
	"fmt"
	"os"
	"os/exec"
)

// Starts the rootless Docker daemon using slurm-start-dockerd.sh
func Start() error {
	// Run slurm-start-dockerd.sh
	cmd := exec.Command("slurm-start-dockerd.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start Docker daemon: %w", err)
	}

	return nil
}
