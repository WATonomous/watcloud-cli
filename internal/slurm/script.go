package slurm

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// scriptOpts is what a batch script's #SBATCH lines ask for. Zero values mean
// the script doesn't set them.
type scriptOpts struct {
	path      string
	partition string
	minutes   int
	memMiB    int64
	cpus      int
	gpus      int
	gpuMemMiB int64
	gpuType   string
	diskMiB   int64
	// otherGres are --gres items other than gpu, shard and tmpdisk.
	otherGres []string
	// memPerOther is set by --mem-per-cpu or --mem-per-gpu, and memPer holds
	// them as sbatch arguments. A --mem flag replaces them.
	memPerOther bool
	memPer      []string
	// gpusOpt is set when GPUs are asked for outside --gres, e.g. --gpus.
	gpusOpt bool
	output  string
	array   bool
	// other holds every other directive as sbatch arguments, for when sbatch
	// won't read the script itself.
	other []string
}

func (s *scriptOpts) hasGPU() bool { return s.gpus > 0 || s.gpuMemMiB > 0 }

func (s *scriptOpts) hasDirectives() bool {
	return s.partition != "" || s.minutes > 0 || s.memMiB > 0 || s.cpus > 0 ||
		s.hasGPU() || s.diskMiB > 0 || len(s.otherGres) > 0 || len(s.other) > 0
}

// Short sbatch options, by their long names.
var shortOptions = map[string]string{
	"a": "array", "c": "cpus-per-task", "e": "error", "G": "gpus", "J": "job-name",
	"N": "nodes", "n": "ntasks", "o": "output", "p": "partition", "t": "time",
}

// readScriptOpts reads the #SBATCH lines of the script at path.
func readScriptOpts(path string) (*scriptOpts, error) {
	s := &scriptOpts{path: path}
	for _, line := range sbatchDirectives(path) {
		for _, opt := range splitDirective(line) {
			if err := s.add(opt); err != nil {
				return nil, fmt.Errorf("%s: can't read #SBATCH %s: %w", path, line, err)
			}
		}
	}
	return s, nil
}

type directive struct {
	name, value string
	// raw is the option as sbatch arguments.
	raw []string
}

// directiveFields splits a directive line the way sbatch does: on spaces, with
// quotes grouping words and then removed, so --job-name="my job" is one word.
// A "#" outside quotes at the start of a word starts a comment.
func directiveFields(line string) []string {
	var fields []string
	var word strings.Builder
	inWord := false
	var quote rune
	for _, c := range line {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, inWord = c, true
		case c == ' ' || c == '\t':
			if inWord {
				fields = append(fields, word.String())
				word.Reset()
				inWord = false
			}
		case c == '#' && !inWord:
			return fields
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		fields = append(fields, word.String())
	}
	return fields
}

// splitDirective splits one #SBATCH line into its options: "--gres=x",
// "--gres x", "-p x" and "-px" all work.
func splitDirective(line string) []directive {
	var opts []directive
	fields := directiveFields(line)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if !strings.HasPrefix(f, "-") {
			continue
		}
		d := directive{raw: []string{f}}
		if long, ok := strings.CutPrefix(f, "--"); ok {
			d.name, d.value, _ = strings.Cut(long, "=")
		} else if len(f) > 1 {
			d.name, d.value = shortOptions[f[1:2]], f[2:]
			if d.name == "" {
				d.name = f[1:2]
			}
		}
		if !strings.Contains(f, "=") && d.value == "" && i+1 < len(fields) &&
			!strings.HasPrefix(fields[i+1], "-") && !strings.HasPrefix(fields[i+1], "#") {
			i++
			d.value = fields[i]
			d.raw = append(d.raw, fields[i])
		}
		opts = append(opts, d)
	}
	return opts
}

func (s *scriptOpts) add(d directive) error {
	var err error
	switch d.name {
	case "partition":
		s.partition = d.value
	case "time":
		s.minutes, err = parseDirectiveTime(d.value)
	case "mem":
		s.memMiB, err = parseSlurmSize(d.value)
	case "mem-per-cpu", "mem-per-gpu":
		s.memPerOther = true
		s.memPer = append(s.memPer, d.raw...)
	case "cpus-per-task":
		s.cpus, err = strconv.Atoi(d.value)
	case "gres":
		err = s.addGres(d.value)
	case "gpus", "gpus-per-node", "gpus-per-task", "gpus-per-socket":
		s.gpusOpt = true
		s.other = append(s.other, d.raw...)
	case "output":
		s.output = d.value
		s.other = append(s.other, d.raw...)
	case "array":
		s.array = true
		s.other = append(s.other, d.raw...)
	default:
		s.other = append(s.other, d.raw...)
	}
	return err
}

// addGres reads a --gres value like "shard:rtx_3090:8192,tmpdisk:20480".
// An item it can't read is kept as it is, never dropped.
func (s *scriptOpts) addGres(value string) error {
	for _, item := range strings.Split(value, ",") {
		if item == "" {
			continue
		}
		name, gpuType, count, ok := parseGresItem(item)
		switch {
		case ok && name == "gpu":
			s.gpus = int(count)
			if gpuType != "" {
				s.gpuType = gpuType
			}
		case ok && name == "shard":
			s.gpuMemMiB = count
			if gpuType != "" {
				s.gpuType = gpuType
			}
		case ok && name == "tmpdisk" && gpuType == "":
			if err := checkTmpdisk(item, count); err != nil {
				return err
			}
			s.diskMiB = count
		default:
			s.otherGres = append(s.otherGres, item)
		}
	}
	return nil
}

// checkTmpdisk catches tmpdisk:20G written to mean 20 GiB. tmpdisk counts
// MiB, and in a GRES count Slurm reads G as x1073741824, so that asks for
// 20 PiB and no node could ever run the job.
func checkTmpdisk(item string, mib int64) error {
	if mib < 1<<30 {
		return nil
	}
	count := item[strings.LastIndex(item, ":")+1:]
	digits := strings.TrimRight(count, "GgTt")
	unit, fix := "GiB", "K"
	if strings.HasSuffix(strings.ToLower(count), "t") {
		unit, fix = "TiB", "M"
	}
	return fmt.Errorf("%s is %d MiB: tmpdisk counts MiB, and in a GRES count Slurm multiplies G by 1024^3 and T by 1024^4. For %s %s, write tmpdisk:%s%s",
		item, mib, digits, unit, digits, fix)
}

// parseGresItem reads name[:type][:count]. The count defaults to 1, as in
// Slurm, so "gpu:rtx_3090" is one GPU and "gpu" is one of any type.
func parseGresItem(item string) (name, gpuType string, count int64, ok bool) {
	f := strings.Split(item, ":")
	name, count = f[0], 1
	switch len(f) {
	case 1:
	case 2:
		if n, isCount := parseGresCount(f[1]); isCount {
			count = n
		} else {
			gpuType = f[1]
		}
	case 3:
		n, isCount := parseGresCount(f[2])
		if !isCount {
			return "", "", 0, false
		}
		gpuType, count = f[1], n
	default:
		return "", "", 0, false
	}
	return name, gpuType, count, count > 0
}

// parseDirectiveTime reads a time the way sbatch does: a bare number is
// minutes.
func parseDirectiveTime(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n, nil
	}
	return parseSlurmTime(strings.ToLower(s))
}

var slurmSizePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)([KkMmGgTt]?)[Bb]?$`)

// parseSlurmSize reads a size the way sbatch does: a bare number is MiB.
func parseSlurmSize(s string) (int64, error) {
	m := slurmSizePattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"": 1, "k": 1.0 / 1024, "m": 1, "g": 1 << 10, "t": 1 << 20}[strings.ToLower(m[2])]
	return int64(math.Ceil(n * mult)), nil
}

// Interpreters that take a script file as their first argument.
var interpreters = map[string]bool{"bash": true, "sh": true, "zsh": true, "python": true, "python3": true}

// interpretedScript returns the script in a command like `bash train.sh`.
func interpretedScript(cmd []string) string {
	if len(cmd) < 2 || !interpreters[filepath.Base(cmd[0])] || strings.HasPrefix(cmd[1], "-") {
		return ""
	}
	return cmd[1]
}
