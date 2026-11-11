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

// checkBatch reads the batch script, if there is one, and validates what only
// batch jobs have.
func (r *Request) checkBatch() ([]Notice, error) {
	if len(r.Command) == 0 {
		return nil, fmt.Errorf("--batch needs a command or script after --, e.g. watcloud slurm run --batch --time 4h -- python train.py")
	}

	path := r.Command[0]
	r.script = isScript(path)
	if r.script && !strings.Contains(path, "/") {
		// Run it as a path, not something to look up on $PATH.
		r.Command[0] = "./" + path
	}
	if !r.script {
		path = interpretedScript(r.Command)
		if path == "" || !fileExists(path) {
			return nil, nil
		}
	}

	opts, err := readScriptOpts(path)
	if err != nil {
		return nil, err
	}
	r.opts = opts
	if r.script && r.Docker && !isExecutable(path) {
		return nil, fmt.Errorf("%s must be executable to run with --docker: chmod +x %s", path, path)
	}
	if r.sbatchReadsScript() || !opts.hasDirectives() {
		return nil, nil
	}
	how := "inside the Docker wrapper"
	if !r.script {
		how = "as `" + ShellJoin(r.Command[:2]) + "`"
	}
	return []Notice{note("Passing the #SBATCH lines in %s to sbatch, since the script runs %s.", path, how)}, nil
}

// sbatchReadsScript reports whether sbatch is handed the script itself, and so
// reads its #SBATCH lines without help.
func (r *Request) sbatchReadsScript() bool { return r.script && !r.Docker }

func (r *Request) sbatchArgs() []string {
	args := []string{"sbatch", "--parsable"}
	full := r.withScript()
	if r.sbatchReadsScript() {
		// Only what the flags change. A command-line --gres replaces the
		// script's whole --gres, so it's passed with the script's other items.
		return append(append(args, r.flagArgs(full)...), r.Command...)
	}
	// sbatch won't see the script's #SBATCH lines, so they're passed here.
	if r.opts != nil {
		args = append(args, r.opts.other...)
	}
	args = append(args, full.resourceArgs()...)
	work := r.Command
	if r.Docker {
		work = append([]string{"bash", "-c", dockerScript(false), "watcloud"}, r.Command...)
	}
	return append(args, "--wrap", ShellJoin(work))
}

// flagArgs returns the resource flags the user set, with full the request
// merged with the script.
func (r *Request) flagArgs(full Request) []string {
	var args []string
	if r.Partition != "" {
		args = append(args, "--partition", r.Partition)
	}
	if r.CPUs > 0 {
		args = append(args, "--cpus-per-task", fmt.Sprint(r.CPUs))
	}
	if r.MemMiB > 0 {
		args = append(args, "--mem", slurmSize(r.MemMiB))
	}
	if r.GPUs > 0 || r.GPUMemMiB > 0 || r.GPUType != "" || r.DiskMiB > 0 {
		if gres := full.gres(); gres != "" {
			args = append(args, "--gres", gres)
		}
	}
	if r.Minutes > 0 {
		args = append(args, "--time", formatTime(r.Minutes))
	}
	return args
}

// OutputFile says where a batch job's output goes: Slurm's default,
// slurm-<id>.out in the directory it was submitted from, unless the script
// sets --output.
func (r *Request) OutputFile(jobID, dir string) string {
	if r.opts != nil && r.opts.output != "" {
		return fmt.Sprintf("%s, as set by #SBATCH --output in %s", r.opts.output, r.opts.path)
	}
	if r.opts != nil && r.opts.array {
		return filepath.Join(dir, "slurm-"+jobID+"_<index>.out") + ", one per array task"
	}
	return filepath.Join(dir, "slurm-"+jobID+".out")
}

// checkCommand catches a command that is sure to fail as soon as the job
// starts, after it has waited in the queue. A bare command name missing here
// isn't one: GPU tools like nvidia-smi may exist only on compute nodes.
func checkCommand(cmd []string) error {
	name := cmd[0]
	if strings.Contains(name, "/") && !fileExists(name) {
		return fmt.Errorf("%s: no such file", name)
	}
	if !strings.Contains(name, "/") && fileExists(name) {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s isn't a command; run it as ./%s, or with its interpreter, e.g. -- python3 %s", name, name, name)
		}
	}
	if script := interpretedScript(cmd); script != "" && !fileExists(script) && looksLikeFile(script) {
		return fmt.Errorf("%s: no such file", script)
	}
	return nil
}

func looksLikeFile(arg string) bool {
	return strings.Contains(arg, "/") || filepath.Ext(arg) != ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
