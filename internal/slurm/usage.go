package slurm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/fatih/color"
)

const usageLookback = "now-7days"

// Slurm's bin dir is added to PATH by /etc/bash.bashrc, which sudo doesn't keep.
func slurmCommand(name string, args ...string) *exec.Cmd {
	if _, err := exec.LookPath(name); err != nil {
		name = "/opt/slurm/bin/" + name
	}
	return exec.Command(name, args...)
}

const sacctUsageFormat = "JobID,User,JobName,State,AllocTRES,TRESUsageInMax"

// A shard is 1 MiB of GPU memory, so RequestedShards is the request in MiB.
type GPUUsage struct {
	JobID           string
	User            string
	Name            string
	State           string
	RequestedShards int64
	RequestedGPUs   int64
	PeakMemMiB      float64
	PeakUtil        float64
	HasUsage        bool
}

func (u *GPUUsage) requestsGPU() bool {
	return u.RequestedShards > 0 || u.RequestedGPUs > 0
}

// Whole-GPU jobs own the card, so they can't go over.
func (u *GPUUsage) OverRequest() bool {
	return u.HasUsage && u.RequestedGPUs == 0 && u.PeakMemMiB > float64(u.RequestedShards)
}

// ShowGPUUsage prints GPU usage against the request for jobID, the current job,
// or the user's recent GPU jobs. With all, it prints every user's recent jobs
// that went over their request.
func ShowGPUUsage(jobID string, all bool) error {
	if jobID == "" && !all {
		jobID = os.Getenv("SLURM_JOB_ID")
	}

	args := []string{"-P", "--noheader", "--format=" + sacctUsageFormat}
	switch {
	case jobID != "":
		args = append(args, "-j", jobID)
	case all:
		args = append(args, "-a", "-S", usageLookback)
	default:
		args = append(args, "-S", usageLookback)
	}
	out, err := slurmCommand("sacct", args...).Output()
	if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
		return errors.New(strings.TrimSpace(string(exitErr.Stderr)))
	} else if err != nil {
		return fmt.Errorf("sacct failed: %w", err)
	}
	usages, err := ParseSacctUsage(string(out))
	if err != nil {
		return err
	}

	for _, u := range usages {
		if u.State == "RUNNING" {
			// sacct only records a step's usage once the step ends.
			if steps, err := slurmCommand("sstat", "-a", "-P", "--noheader",
				"--format=JobID,TRESUsageInMax", "-j", u.JobID).Output(); err == nil {
				mergeSstatUsage(u, string(steps))
			}
		}
	}

	var shown []*GPUUsage
	for _, u := range usages {
		if u.requestsGPU() && (!all || u.OverRequest()) {
			shown = append(shown, u)
		}
	}

	if all && !canSeeAllJobs() {
		fmt.Fprintln(os.Stderr, "Only Slurm admins can see other users' jobs; showing only yours.")
	}

	if len(shown) == 0 {
		switch {
		case jobID != "" && len(usages) == 0:
			return fmt.Errorf("job %s not found (you can only see your own jobs)", jobID)
		case jobID != "":
			fmt.Printf("Job %s did not request a GPU.\n", jobID)
		case all:
			fmt.Println("No GPU jobs went over their request in the last 7 days.")
		default:
			fmt.Println("No GPU jobs in the last 7 days.")
		}
		return nil
	}

	for _, u := range shown {
		fmt.Println(FormatGPUUsage(u, all))
	}
	return nil
}

// slurmdbd hides other users' jobs (PrivateData=jobs) from everyone except
// root and Slurm operators/administrators.
func canSeeAllJobs() bool {
	if os.Geteuid() == 0 {
		return true
	}
	me, err := user.Current()
	if err != nil {
		return false
	}
	out, err := slurmCommand("sacctmgr", "-n", "-P", "show", "user", me.Username, "format=AdminLevel").Output()
	if err != nil {
		return false
	}
	level := strings.TrimSpace(string(out))
	return level == "Operator" || level == "Administrator"
}

func FormatGPUUsage(u *GPUUsage, withUser bool) string {
	prefix := fmt.Sprintf("job %s (%s, %s): ", u.JobID, u.Name, strings.ToLower(u.State))
	if withUser {
		prefix = fmt.Sprintf("job %s (%s, %s, %s): ", u.JobID, u.User, u.Name, strings.ToLower(u.State))
	}

	var requested string
	if u.RequestedGPUs > 0 {
		requested = fmt.Sprintf("requested %d whole GPU", u.RequestedGPUs)
		if u.RequestedGPUs > 1 {
			requested += "s"
		}
	} else {
		requested = "requested " + formatMiB(float64(u.RequestedShards)) + " of GPU memory"
	}

	if !u.HasUsage {
		return prefix + requested + ", no GPU usage recorded yet"
	}

	used := fmt.Sprintf("peaked at %s", formatMiB(u.PeakMemMiB))
	if u.OverRequest() {
		used = color.New(color.FgRed).Sprint(used)
	}
	return fmt.Sprintf("%s%s, %s, utilisation %.0f%%", prefix, requested, used, u.PeakUtil)
}

func formatMiB(mib float64) string {
	if mib < 1024 {
		return fmt.Sprintf("%.0f MiB", mib)
	}
	return fmt.Sprintf("%.1f GiB", mib/1024)
}

// The request is on the job's own row; usage is only on its step rows
// (.batch, .extern, .0, ...), so the peak is the max over steps.
func ParseSacctUsage(out string) ([]*GPUUsage, error) {
	var usages []*GPUUsage
	byID := map[string]*GPUUsage{}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 6 {
			return nil, fmt.Errorf("unexpected sacct line: %q", line)
		}
		id, owner, name, state, alloc, usageMax := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]

		jobID, _, isStep := strings.Cut(id, ".")
		u, ok := byID[jobID]
		if !ok {
			u = &GPUUsage{JobID: jobID}
			byID[jobID] = u
			usages = append(usages, u)
		}

		if !isStep {
			u.User = owner
			u.Name = name
			// "CANCELLED by 1234" -> "CANCELLED"
			u.State, _, _ = strings.Cut(state, " ")
			tres := parseTRES(alloc)
			u.RequestedShards, _ = strconv.ParseInt(tres["gres/shard"], 10, 64)
			u.RequestedGPUs, _ = strconv.ParseInt(tres["gres/gpu"], 10, 64)
			continue
		}
		mergeStepUsage(u, usageMax)
	}
	return usages, nil
}

func mergeSstatUsage(u *GPUUsage, out string) {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if _, usageMax, ok := strings.Cut(line, "|"); ok {
			mergeStepUsage(u, usageMax)
		}
	}
}

func mergeStepUsage(u *GPUUsage, usageMax string) {
	tres := parseTRES(usageMax)
	mem, hasMem := tres["gres/gpumem"]
	util, hasUtil := tres["gres/gpuutil"]
	if !hasMem && !hasUtil {
		return
	}
	u.HasUsage = true
	if mib, err := parseSizeMiB(mem); err == nil && mib > u.PeakMemMiB {
		u.PeakMemMiB = mib
	}
	if pct, err := strconv.ParseFloat(util, 64); err == nil && pct > u.PeakUtil {
		u.PeakUtil = pct
	}
}

func parseTRES(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// Parses sacct sizes such as "1692M" or "21.50G". A bare number is bytes.
func parseSizeMiB(s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	scale := 1.0 / (1024 * 1024)
	if mul, ok := sizeSuffixMiB[s[len(s)-1]]; ok {
		scale = mul
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return v * scale, nil
}

var sizeSuffixMiB = map[byte]float64{
	'K': 1.0 / 1024,
	'M': 1,
	'G': 1024,
	'T': 1024 * 1024,
	'P': 1024 * 1024 * 1024,
}
