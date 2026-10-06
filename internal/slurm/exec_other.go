//go:build !unix

package slurm

import "fmt"

func Exec(args []string) error {
	return fmt.Errorf("submitting jobs is only supported on Linux; use --dry-run to print the command")
}
