package slurm

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// checkBatch validates the parts of a request only batch jobs have.
func (r *Request) checkBatch(cluster *Cluster) ([]Notice, error) {
	if len(r.Command) == 0 {
		return nil, fmt.Errorf("--batch needs a command or script after --, e.g. watcloud slurm run --batch --time 4h -- python train.py")
	}
	// Nobody is watching a batch job when it hits the default limit.
	if r.Minutes == 0 {
		msg := "--batch needs --time, or the job is killed at the partition's default limit"
		if cluster != nil {
			if p, ok := cluster.partition(r.Partition); ok && p.DefaultMinutes > 0 {
				msg += fmt.Sprintf(" (%s on %s)", humanTime(p.DefaultMinutes), p.Name)
			}
		}
		return nil, errors.New(msg)
	}

	path := r.Command[0]
	r.script = isScript(path)
	if !r.script {
		return nil, nil
	}
	// Run it as a path, not something to look up on $PATH.
	if !strings.Contains(path, "/") {
		r.Command[0] = "./" + path
	}
	if !r.Docker {
		return nil, nil
	}
	// With --docker the script runs inside the wrapper, so sbatch never reads it.
	if !isExecutable(path) {
		return nil, fmt.Errorf("%s must be executable to run with --docker: chmod +x %s", path, path)
	}
	if len(sbatchDirectives(path)) > 0 {
		return []Notice{warn("The #SBATCH lines in %s are ignored with --docker. Pass those settings as flags instead.", path)}, nil
	}
	return nil, nil
}

func (r *Request) sbatchArgs() []string {
	args := append([]string{"sbatch", "--parsable"}, r.resourceArgs()...)
	// sbatch reads a script file itself, so its #SBATCH lines still apply.
	// Flags given here take precedence over them.
	if r.script && !r.Docker {
		return append(args, r.Command...)
	}
	work := r.Command
	if r.Docker {
		work = append([]string{"bash", "-c", dockerScript(false), "watcloud"}, r.Command...)
	}
	return append(args, "--wrap", ShellJoin(work))
}

// OutputFile is where the job's output goes: Slurm's default, slurm-<id>.out
// in the directory it was submitted from. It's "" when the script picks its own.
func (r *Request) OutputFile(jobID, dir string) string {
	if r.script {
		for _, d := range sbatchDirectives(r.Command[0]) {
			if strings.HasPrefix(d, "--output") || strings.HasPrefix(d, "-o") {
				return ""
			}
		}
	}
	return filepath.Join(dir, "slurm-"+jobID+".out")
}

// Submit runs sbatch and returns the job ID. sbatch's own messages, including
// job_submit.lua's, pass through on stderr.
func Submit(args []string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%s not found; run this on a WATcloud login node", args[0])
	}
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", args[0], err)
	}
	return parseJobID(string(out)), nil
}

// parseJobID reads sbatch --parsable output: "<id>" or "<id>;<cluster>".
func parseJobID(out string) string {
	id, _, _ := strings.Cut(strings.TrimSpace(out), ";")
	return id
}

// isScript reports whether path is a file sbatch would take as a script,
// which must start with "#!".
func isScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 2)
	n, _ := f.Read(head)
	return n == 2 && string(head) == "#!"
}

// sbatchDirectives returns the options on a script's #SBATCH lines. Like
// sbatch, it stops at the first line that isn't blank or a comment.
func sbatchDirectives(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var directives []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		if d, ok := strings.CutPrefix(line, "#SBATCH"); ok {
			directives = append(directives, strings.TrimSpace(d))
		}
	}
	return directives
}
