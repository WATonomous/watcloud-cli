//go:build unix

package slurm

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Exec replaces this process with args, so srun owns the terminal and signals.
func Exec(args []string) error {
	path, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("%s not found; run this on a WATcloud login node", args[0])
	}
	return syscall.Exec(path, args, os.Environ())
}
