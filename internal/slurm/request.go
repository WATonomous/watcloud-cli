package slurm

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DockerDefaultDiskMiB is the scratch disk given to --docker jobs that don't
	// ask for one: room for a typical CUDA image and its unpacked layers, and it
	// fits on every node.
	DockerDefaultDiskMiB = 20 * 1024

	// job_submit.lua warns about interactive jobs longer than this.
	interactiveLimitMinutes = 6 * 60

	// job_submit.lua rejects interactive jobs in this partition.
	densePartition = "compute_dense"

	dockerdLog = "/tmp/dockerd.log"
)

// Request is a job described in the user's terms. Zero values mean "not asked
// for", which leaves the cluster default in place.
type Request struct {
	CPUs      int
	MemMiB    int64
	DiskMiB   int64
	GPUMemMiB int64
	GPUs      int
	GPUType   string
	Minutes   int
	Partition string
	Docker    bool
	// Command runs instead of an interactive shell when set.
	Command []string
	// Shell is the interactive shell to start.
	Shell string
}

// Notice is something the user should know before the job is submitted.
type Notice struct {
	Warning bool
	Text    string
}

func note(format string, a ...any) Notice { return Notice{Text: fmt.Sprintf(format, a...)} }
func warn(format string, a ...any) Notice {
	return Notice{Warning: true, Text: fmt.Sprintf(format, a...)}
}

// Check validates the request, fills in defaults, and returns what the user
// should be told about it. cluster may be nil, which skips the checks that
// need to know the nodes.
func (r *Request) Check(cluster *Cluster) ([]Notice, error) {
	if r.CPUs < 0 || r.GPUs < 0 {
		return nil, fmt.Errorf("--cpus and --gpus can't be negative")
	}
	if r.GPUs > 0 && r.GPUMemMiB > 0 {
		return nil, fmt.Errorf("use either --gpus (whole GPUs) or --gpu-mem (a share of one), not both")
	}
	if r.GPUType != "" && r.GPUs == 0 && r.GPUMemMiB == 0 {
		return nil, fmt.Errorf("--gpu-type needs --gpus or --gpu-mem")
	}
	if r.Partition == densePartition {
		return nil, fmt.Errorf("the %s partition only accepts batch jobs (sbatch)", densePartition)
	}

	var notices []Notice
	switch {
	case r.DiskMiB == 0 && r.Docker:
		r.DiskMiB = DockerDefaultDiskMiB
		notices = append(notices, note("No --disk given: using %s of scratch disk for Docker images.", humanSize(r.DiskMiB)))
	case r.DiskMiB == 0:
		notices = append(notices, note("No --disk given: /tmp gets the 100 MiB default, and it can't grow once the job starts."))
	}
	if r.Docker && r.MemMiB == 0 {
		notices = append(notices, warn("No --mem given: the job gets the small per-CPU default. Docker usually needs a few GiB, e.g. --mem 8G."))
	}
	if r.Minutes > interactiveLimitMinutes {
		notices = append(notices, warn("Interactive jobs over 6 hours will soon be rejected. Use sbatch for long jobs."))
	}

	if cluster != nil {
		more, err := cluster.check(r)
		if err != nil {
			return nil, err
		}
		notices = append(notices, more...)
	}
	return notices, nil
}

// Args returns the srun command line for the request.
func (r *Request) Args() []string {
	args := []string{"srun"}
	if r.Partition != "" {
		args = append(args, "--partition", r.Partition)
	}
	if r.CPUs > 0 {
		args = append(args, "--cpus-per-task", strconv.Itoa(r.CPUs))
	}
	if r.MemMiB > 0 {
		args = append(args, "--mem", slurmSize(r.MemMiB))
	}

	var gres []string
	gpuType := ""
	if r.GPUType != "" {
		gpuType = r.GPUType + ":"
	}
	if r.GPUs > 0 {
		gres = append(gres, fmt.Sprintf("gpu:%s%d", gpuType, r.GPUs))
	}
	if r.GPUMemMiB > 0 {
		gres = append(gres, fmt.Sprintf("shard:%s%d", gpuType, r.GPUMemMiB))
	}
	if r.DiskMiB > 0 {
		gres = append(gres, fmt.Sprintf("tmpdisk:%d", r.DiskMiB))
	}
	if len(gres) > 0 {
		args = append(args, "--gres", strings.Join(gres, ","))
	}

	if r.Minutes > 0 {
		args = append(args, "--time", formatTime(r.Minutes))
	}

	interactive := len(r.Command) == 0
	if interactive {
		args = append(args, "--pty")
	}
	if r.Docker {
		// "$@" is the shell or command that follows.
		args = append(args, "bash", "-c", dockerScript(interactive), "watcloud")
	}
	if interactive {
		return append(args, r.Shell)
	}
	return append(args, r.Command...)
}

// dockerScript starts dockerd, then execs "$@". slurm-start-dockerd.sh exports
// DOCKER_HOST only to itself, so it's set again here. An interactive shell still
// starts if dockerd fails, so the log in /tmp can be read before the job ends.
func dockerScript(interactive bool) string {
	onFailure := ""
	if !interactive {
		onFailure = " exit 1;"
	}
	return `echo "Starting Docker..." >&2; ` +
		`if out=$(slurm-start-dockerd.sh 2>&1); then ` +
		`export DOCKER_HOST="unix://${XDG_RUNTIME_DIR:-/tmp/run}/docker.sock"; echo "Docker is ready." >&2; ` +
		`else printf "%s\n" "$out" >&2; echo "Docker failed to start (log: ` + dockerdLog + `)." >&2;` + onFailure + ` fi; ` +
		`exec "$@"`
}

// ShellJoin quotes args so the line can be pasted into a shell.
func ShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Slurm sizes are binary, so G and GiB mean the same thing here.
var sizeUnits = map[string]float64{
	"m": 1, "mb": 1, "mib": 1,
	"g": 1 << 10, "gb": 1 << 10, "gib": 1 << 10,
	"t": 1 << 20, "tb": 1 << 20, "tib": 1 << 20,
}

var sizePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([A-Za-z]*)$`)

// ParseSize converts a size like "20G" or "512M" to MiB. A unit is required:
// Slurm reads a bare number as MiB, so "--disk 20" would mean 20 MiB.
func ParseSize(s string) (int64, error) {
	m := sizePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q, expected e.g. 20G or 512M", s)
	}
	if m[2] == "" {
		return 0, fmt.Errorf("size %q needs a unit, e.g. %sG or %sM", s, m[1], m[1])
	}
	mult, ok := sizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0, fmt.Errorf("unknown unit in %q, use M, G or T", s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mib := int64(math.Ceil(n * mult))
	if mib <= 0 {
		return 0, fmt.Errorf("size %q must be greater than zero", s)
	}
	return mib, nil
}

var shorthandTime = regexp.MustCompile(`^(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?$`)

// ParseTime converts a time limit to minutes. It takes shorthand (90m, 4h,
// 1d12h) and Slurm's own forms (MM:SS, HH:MM:SS, D-HH, D-HH:MM, D-HH:MM:SS).
// Like sizes, a bare number is rejected rather than read as minutes.
func ParseTime(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if m := shorthandTime.FindStringSubmatch(s); m != nil && s != "" {
		minutes := atoi(m[1])*24*60 + atoi(m[2])*60 + atoi(m[3])
		if minutes == 0 {
			return 0, fmt.Errorf("time %q must be greater than zero", s)
		}
		return minutes, nil
	}
	return parseSlurmTime(s)
}

func parseSlurmTime(s string) (int, error) {
	invalid := fmt.Errorf("invalid time %q, expected e.g. 30m, 4h, 1d12h or 2:00:00", s)

	days, rest, hasDays := 0, s, false
	if d, r, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, invalid
		}
		days, rest, hasDays = n, r, true
	}
	parts := strings.Split(rest, ":")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, invalid
		}
		nums[i] = n
	}

	var h, m, sec int
	switch {
	case hasDays && len(nums) == 1:
		h = nums[0]
	case hasDays && len(nums) == 2:
		h, m = nums[0], nums[1]
	case len(nums) == 3:
		h, m, sec = nums[0], nums[1], nums[2]
	case !hasDays && len(nums) == 2:
		m, sec = nums[0], nums[1]
	case !hasDays && len(nums) == 1:
		return 0, fmt.Errorf("time %q needs a unit, e.g. %sm or %sh", s, s, s)
	default:
		return 0, invalid
	}
	minutes := days*24*60 + h*60 + m + (sec+59)/60
	if minutes == 0 {
		return 0, fmt.Errorf("time %q must be greater than zero", s)
	}
	return minutes, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// formatTime renders minutes the way srun takes them: H:MM:SS or D-HH:MM:SS.
func formatTime(minutes int) string {
	d, h, m := minutes/(24*60), minutes%(24*60)/60, minutes%60
	if d > 0 {
		return fmt.Sprintf("%d-%02d:%02d:00", d, h, m)
	}
	return fmt.Sprintf("%d:%02d:00", h, m)
}

// humanTime renders minutes for messages, e.g. "30 min", "4 h", "1 d 12 h".
func humanTime(minutes int) string {
	d, h, m := minutes/(24*60), minutes%(24*60)/60, minutes%60
	var parts []string
	if d > 0 {
		parts = append(parts, fmt.Sprintf("%d d", d))
	}
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%d h", h))
	}
	if m > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d min", m))
	}
	return strings.Join(parts, " ")
}

// slurmSize renders MiB the way --mem takes it.
func slurmSize(mib int64) string {
	if mib%1024 == 0 {
		return fmt.Sprintf("%dG", mib/1024)
	}
	return fmt.Sprintf("%dM", mib)
}

func humanSize(mib int64) string {
	switch {
	case mib%1024 == 0:
		return fmt.Sprintf("%d GiB", mib/1024)
	case mib >= 1024:
		return fmt.Sprintf("%.1f GiB", float64(mib)/1024)
	default:
		return fmt.Sprintf("%d MiB", mib)
	}
}
