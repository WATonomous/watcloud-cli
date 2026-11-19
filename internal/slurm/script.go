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
	// gresLines counts --gres directives. sbatch keeps only the last.
	gresLines int
	// badTmpdisk explains a tmpdisk count with an M, G or T unit, which Slurm
	// multiplies far past any node. It's an error unless --disk replaces it.
	badTmpdisk string
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
// A backslash keeps the next character as it is, as in my\ job, and a "#"
// outside quotes ends the line, even inside a word.
func directiveFields(line string) []string {
	var fields []string
	var word strings.Builder
	inWord, escaped := false, false
	var quote rune
	flush := func() {
		if inWord {
			fields = append(fields, word.String())
			word.Reset()
			inWord = false
		}
	}
	for _, c := range line {
		switch {
		case escaped:
			word.WriteRune(c)
			escaped = false
		case c == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, inWord = c, true
		case c == ' ' || c == '\t':
			flush()
		case c == '#':
			flush()
			return fields
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	flush()
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
		s.gresLines++
		s.setGres(d.value)
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

// setGres reads a --gres value like "shard:rtx_3090:8192,tmpdisk:20480". Like
// sbatch, a later --gres line replaces an earlier one, and "none" asks for
// nothing. An item it can't read is kept as it is, never dropped.
func (s *scriptOpts) setGres(value string) {
	s.gpus, s.gpuMemMiB, s.gpuType, s.diskMiB = 0, 0, "", 0
	s.otherGres, s.badTmpdisk = nil, ""
	if value == "none" {
		return
	}
	for _, item := range strings.Split(value, ",") {
		if item == "" {
			continue
		}
		name, gpuType, count, ok := parseGresItem(strings.TrimPrefix(item, "gres/"))
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
			s.diskMiB = count
			s.badTmpdisk = tmpdiskHint(item, count)
		default:
			s.otherGres = append(s.otherGres, item)
		}
	}
}

// tmpdiskHint explains a tmpdisk count like 20G, written to mean 20 GiB.
// tmpdisk counts MiB, and in a GRES count Slurm multiplies M by 1024^2 and G
// by 1024^3, so that asks for 20 PiB. It's "" for counts without that mistake.
func tmpdiskHint(item string, mib int64) string {
	m := gresCountPattern.FindStringSubmatch(item[strings.LastIndex(item, ":")+1:])
	if m == nil {
		return ""
	}
	var unit, mult, fix string
	switch strings.ToLower(m[2]) {
	case "m":
		unit, mult, fix = "MiB", "1024^2", ""
	case "g":
		unit, mult, fix = "GiB", "1024^3", "K"
	case "t":
		unit, mult, fix = "TiB", "1024^4", "M"
	default:
		return ""
	}
	return fmt.Sprintf("%s is %d MiB: tmpdisk counts MiB, and in a GRES count Slurm multiplies %s by %s. For %s %s, write tmpdisk:%s%s",
		item, mib, strings.ToUpper(m[2]), mult, m[1], unit, m[1], fix)
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
