package slurm

import (
	"fmt"
	"os"
	"os/exec"
)

const slurmCapacityScript = "/usr/local/bin/watcloud-slurm-capacity"

// gatherCmd collects all required SLURM state in one pass.
// The 5 sections (partitions, nodes, squeue summary, jobs detail, timestamp)
// are separated by "---" as expected by watcloud-slurm-capacity.
const gatherCmd = `scontrol show partition --oneliner; echo "---"; ` +
	`scontrol show node --oneliner; echo "---"; ` +
	`squeue --all -o "%.18i %.9P %.30j %.8u %.8T %.10M %.10l %.6D %R"; echo "---"; ` +
	`scontrol show job --oneliner; echo "---"; ` +
	`date --iso-8601=seconds`

// ShowCapacity gathers SLURM state and pipes it through the
// watcloud-slurm-capacity script for rendering.
func ShowCapacity() error {
	gather := exec.Command("bash", "-c", gatherCmd)
	render := exec.Command(slurmCapacityScript)

	pipe, err := gather.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	render.Stdin = pipe
	render.Stdout = os.Stdout
	render.Stderr = os.Stderr

	if err := render.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", slurmCapacityScript, err)
	}
	if err := gather.Run(); err != nil {
		return fmt.Errorf("failed to gather SLURM data: %w", err)
	}
	return render.Wait()
}