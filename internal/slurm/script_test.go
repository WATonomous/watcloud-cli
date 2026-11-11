package slurm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitDirective(t *testing.T) {
	for line, want := range map[string]string{
		"--gres=shard:8192,tmpdisk:20480": "gres=shard:8192,tmpdisk:20480",
		"--gres shard:8192":               "gres=shard:8192",
		"-p compute_dense":                "partition=compute_dense",
		"-c4":                             "cpus-per-task=4",
		"--exclusive":                     "exclusive=",
		"--mem=8G  # plenty":              "mem=8G",
		"-o logs/%x-%j.out":               "output=logs/%x-%j.out",
	} {
		var got []string
		for _, d := range splitDirective(line) {
			got = append(got, d.name+"="+d.value)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("splitDirective(%q) = %v, want %s", line, got, want)
		}
	}
}

func TestReadScriptOpts(t *testing.T) {
	path := writeScript(t, `#!/bin/bash
#SBATCH --partition=compute_dense
#SBATCH --time=2-00:00:00
#SBATCH --mem=16G
#SBATCH -c 4
#SBATCH --gres shard:rtx_3090:8192,tmpdisk:20K
#SBATCH --job-name=train
python train.py
`, 0o755)
	s, err := readScriptOpts(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.partition != "compute_dense" || s.minutes != 2*1440 || s.memMiB != 16384 || s.cpus != 4 ||
		s.gpuMemMiB != 8192 || s.gpuType != "rtx_3090" || s.diskMiB != 20480 ||
		strings.Join(s.other, " ") != "--job-name=train" {
		t.Errorf("bad parse: %+v", s)
	}

	// sbatch reads a bare number as minutes for --time and MiB for --mem.
	path = writeScript(t, "#!/bin/bash\n#SBATCH --time=90\n#SBATCH --mem=2048\n", 0o755)
	if s, _ := readScriptOpts(path); s.minutes != 90 || s.memMiB != 2048 {
		t.Errorf("bare numbers: %+v", s)
	}
}

// gresOf returns the --gres value of an sbatch line, or "" without one.
func gresOf(args []string) string {
	for i, a := range args {
		if a == "--gres" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestFlagKeepsScriptGres(t *testing.T) {
	// A command-line --gres replaces the script's whole --gres, so a flag must
	// pass the script's other items with its own.
	path := writeScript(t, "#!/bin/bash\n#SBATCH --gres shard:8192,tmpdisk:20480\n#SBATCH --mem=16G\n", 0o755)
	r := Request{Batch: true, DiskMiB: 40960, Minutes: 720, Command: []string{path}}
	if _, err := r.Check(testCluster()); err != nil {
		t.Fatal(err)
	}
	if got := gresOf(r.Args()); got != "shard:8192,tmpdisk:40960" {
		t.Errorf("--disk should keep the script's GPU share: --gres %s", got)
	}

	path = writeScript(t, "#!/bin/bash\n#SBATCH --gres tmpdisk:1024\n#SBATCH --mem=16G\n", 0o755)
	r = Request{Batch: true, GPUMemMiB: 8192, Minutes: 60, Command: []string{path}}
	if _, err := r.Check(testCluster()); err != nil {
		t.Fatal(err)
	}
	if got := gresOf(r.Args()); got != "shard:8192,tmpdisk:1024" {
		t.Errorf("--gpu-mem should keep the script's tmpdisk: --gres %s", got)
	}

	// No GRES flag: the script's --gres is left for sbatch to read.
	r = Request{Batch: true, Minutes: 60, Command: []string{path}}
	r.Check(testCluster())
	if got := gresOf(r.Args()); got != "" {
		t.Errorf("no GRES flag should pass no --gres, got %s", got)
	}
}

func TestChecksUseScript(t *testing.T) {
	// The compute_dense example from the docs: partition and time are in the script.
	path := writeScript(t, "#!/bin/bash\n#SBATCH --partition=compute_dense\n#SBATCH --time=2-00:00:00\n#SBATCH --gres tmpdisk:10240\n#SBATCH --mem=4G\n", 0o755)
	r := Request{Batch: true, Command: []string{path}}
	notices, err := r.Check(testCluster())
	if err != nil {
		t.Fatalf("the script's --time should count: %v", err)
	}
	if hasNotice(notices, "100 MiB default") {
		t.Errorf("the script sets tmpdisk, so no /tmp note: %v", notices)
	}
	if args := strings.Join(r.Args(), " "); strings.Contains(args, "--time") || strings.Contains(args, "--partition") {
		t.Errorf("sbatch reads the script's own --time and --partition: %s", args)
	}

	r = Request{Batch: true, Minutes: 2 * 1440, Command: []string{path}}
	if _, err := r.Check(testCluster()); err != nil {
		t.Errorf("2 days fits the script's compute_dense partition: %v", err)
	}

	// The script's time over its partition's limit is still caught.
	path = writeScript(t, "#!/bin/bash\n#SBATCH --time=2-00:00:00\n", 0o755)
	r = Request{Batch: true, Command: []string{path}}
	if _, err := r.Check(testCluster()); err == nil || !strings.Contains(err.Error(), "longest job on compute") {
		t.Errorf("got %v, want the compute time limit", err)
	}
}

func TestCommandTypos(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.ToSlash(filepath.Join(dir, "trian.sh"))
	for name, cmd := range map[string][]string{
		"missing script":      {missing},
		"missing interpreted": {"bash", missing},
	} {
		r := Request{Batch: true, Minutes: 60, Command: cmd}
		if _, err := r.Check(nil); err == nil || !strings.Contains(err.Error(), "no such file") {
			t.Errorf("%s: got %v, want no such file", name, err)
		}
	}

	r := Request{Minutes: 60, Command: []string{"nvidia-smi", "-L"}}
	if _, err := r.Check(nil); err != nil {
		t.Errorf("a command missing here may exist on compute nodes: %v", err)
	}
}

func TestInterpretedScript(t *testing.T) {
	// `bash train.sh` goes through --wrap, so sbatch never sees the #SBATCH
	// lines; they're passed on the command line instead.
	path := writeScript(t, "#!/bin/bash\n#SBATCH --gres shard:8192\n#SBATCH --mem=16G\n#SBATCH --job-name=train\npython train.py\n", 0o755)
	r := Request{Batch: true, Minutes: 60, Command: []string{"bash", path}}
	notices, err := r.Check(testCluster())
	if err != nil {
		t.Fatal(err)
	}
	if !hasNotice(notices, "Passing the #SBATCH lines") {
		t.Errorf("want a note about passing the #SBATCH lines, got %v", notices)
	}
	args := r.Args()
	joined := strings.Join(args, " ")
	if gresOf(args) != "shard:8192" || !strings.Contains(joined, "--job-name=train") ||
		!strings.Contains(joined, "--mem 16G") || args[len(args)-2] != "--wrap" {
		t.Errorf("the script's #SBATCH lines should be on the command line: %s", joined)
	}
}

func TestMemNotice(t *testing.T) {
	c := testCluster()
	r := Request{GPUMemMiB: 8192, Minutes: 60}
	notices, _ := r.Check(c)
	if !hasWarning(notices, "the job gets 512 MiB (512 MiB per CPU)") {
		t.Errorf("a GPU job without --mem should be warned, got %v", notices)
	}

	r = Request{CPUs: 4, Minutes: 60}
	notices, _ = r.Check(c)
	if !hasNotice(notices, "the job gets 2 GiB") || hasWarning(notices, "--mem") {
		t.Errorf("a CPU job without --mem gets a note, got %v", notices)
	}

	path := writeScript(t, "#!/bin/bash\n#SBATCH --mem=16G\n", 0o755)
	r = Request{Batch: true, GPUMemMiB: 8192, Minutes: 60, Command: []string{path}}
	notices, _ = r.Check(c)
	if hasNotice(notices, "No --mem") {
		t.Errorf("the script sets --mem, got %v", notices)
	}
}

func hasWarning(notices []Notice, text string) bool {
	for _, n := range notices {
		if n.Warning && strings.Contains(n.Text, text) {
			return true
		}
	}
	return false
}

func TestScriptGPUs(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\n#SBATCH --gpus=1\n", 0o755)
	r := Request{Batch: true, GPUMemMiB: 8192, Minutes: 60, Command: []string{path}}
	if _, err := r.Check(nil); err == nil || !strings.Contains(err.Error(), "#SBATCH --gpus") {
		t.Errorf("GPUs in two places: got %v", err)
	}

	// --gpu-type alone changes the script's GPU type.
	path = writeScript(t, "#!/bin/bash\n#SBATCH --gres gpu:1\n#SBATCH --mem=8G\n", 0o755)
	r = Request{Batch: true, GPUType: "rtx_3090", Minutes: 60, Command: []string{path}}
	if _, err := r.Check(testCluster()); err != nil {
		t.Fatal(err)
	}
	if got := gresOf(r.Args()); got != "gpu:rtx_3090:1" {
		t.Errorf("got --gres %s, want gpu:rtx_3090:1", got)
	}
}

func TestOtherGresKept(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\n#SBATCH --gres=tmpdisk:1024,license:2\n", 0o755)
	r := Request{Batch: true, DiskMiB: 4096, Minutes: 60, Command: []string{path}}
	r.Check(nil)
	if got := gresOf(r.Args()); got != "tmpdisk:4096,license:2" {
		t.Errorf("unknown GRES should be kept: --gres %s", got)
	}
	os.Remove(path)
}
