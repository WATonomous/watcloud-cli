package slurm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeScript makes a script in a temp dir and returns its path with forward
// slashes, as it would be on the cluster.
func writeScript(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "train.sh")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

func TestBatchWrap(t *testing.T) {
	r := Request{Batch: true, Minutes: 720, GPUMemMiB: 8192, Command: []string{"python", "my train.py"}}
	if _, err := r.Check(nil); err != nil {
		t.Fatal(err)
	}
	args := r.Args()
	want := "sbatch --parsable --gres shard:8192 --time 12:00:00 --wrap"
	if got := strings.Join(args[:len(args)-1], " "); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := args[len(args)-1]; got != "python 'my train.py'" {
		t.Errorf("wrapped command = %s", got)
	}
}

func TestBatchDockerWrap(t *testing.T) {
	r := Request{Batch: true, Minutes: 10, Docker: true, Command: []string{"docker", "run", "--rm", "hello-world"}}
	if _, err := r.Check(nil); err != nil {
		t.Fatal(err)
	}
	wrap := r.Args()[len(r.Args())-1]
	if !strings.HasPrefix(wrap, "bash -c '") || !strings.Contains(wrap, "exit 1") ||
		!strings.HasSuffix(wrap, "' watcloud docker run --rm hello-world") {
		t.Errorf("unexpected wrapped command: %s", wrap)
	}
}

func TestBatchScript(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\n#SBATCH --job-name train\npython train.py\n", 0o755)
	r := Request{Batch: true, Minutes: 60, Command: []string{path, "--epochs", "3"}}
	notices, err := r.Check(nil)
	if err != nil {
		t.Fatal(err)
	}
	args := r.Args()
	if got := strings.Join(args[len(args)-3:], " "); got != path+" --epochs 3" {
		t.Errorf("script should be passed to sbatch directly, got %v", args)
	}
	for _, n := range notices {
		if n.Warning {
			t.Errorf("unexpected warning without --docker: %s", n.Text)
		}
	}
}

func TestBatchScriptDocker(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\n#SBATCH --job-name train\ndocker build .\n", 0o755)
	r := Request{Batch: true, Minutes: 60, Docker: true, Command: []string{path}}
	notices, err := r.Check(nil)
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, n := range notices {
		warned = warned || (n.Warning && strings.Contains(n.Text, "#SBATCH"))
	}
	if !warned {
		t.Errorf("want a warning that #SBATCH lines are ignored, got %v", notices)
	}
	args := r.Args()
	if args[len(args)-2] != "--wrap" || !strings.HasSuffix(args[len(args)-1], "watcloud "+path) {
		t.Errorf("script should run inside the Docker wrapper, got %v", args)
	}
}

func TestBatchScriptDockerNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bit on Windows")
	}
	path := writeScript(t, "#!/bin/bash\ndocker build .\n", 0o644)
	r := Request{Batch: true, Minutes: 60, Docker: true, Command: []string{path}}
	if _, err := r.Check(nil); err == nil || !strings.Contains(err.Error(), "chmod +x") {
		t.Errorf("got %v, want a chmod +x error", err)
	}
}

func TestBatchRelativeScript(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\ntrue\n", 0o755)
	dir, name := filepath.Split(path)
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	r := Request{Batch: true, Minutes: 60, Command: []string{name}}
	if _, err := r.Check(nil); err != nil {
		t.Fatal(err)
	}
	if r.Command[0] != "./train.sh" {
		t.Errorf("script should run as ./train.sh, got %s", r.Command[0])
	}
}

func TestBatchChecks(t *testing.T) {
	c := testCluster()

	r := Request{Batch: true, Minutes: 60}
	if _, err := r.Check(c); err == nil || !strings.Contains(err.Error(), "command or script") {
		t.Errorf("no command: got %v", err)
	}

	r = Request{Batch: true, Command: []string{"true"}}
	if _, err := r.Check(c); err == nil || !strings.Contains(err.Error(), "30 min on compute") {
		t.Errorf("no --time: got %v", err)
	}

	r = Request{Batch: true, Partition: "compute_dense", Minutes: 3 * 1440, Command: []string{"true"}}
	if _, err := r.Check(c); err != nil {
		t.Errorf("3 days on compute_dense should be allowed: %v", err)
	}

	r = Request{Batch: true, Minutes: 3 * 1440, Command: []string{"true"}}
	if _, err := r.Check(c); err == nil || !strings.Contains(err.Error(), "longest job") {
		t.Errorf("3 days on compute: got %v", err)
	}

	r = Request{Batch: true, Minutes: 12 * 60, Command: []string{"true"}}
	notices, err := r.Check(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notices {
		if strings.Contains(n.Text, "Interactive") {
			t.Errorf("batch jobs shouldn't get the interactive time warning")
		}
	}
}

func TestOutputFile(t *testing.T) {
	r := Request{Batch: true, Minutes: 60, Command: []string{"true"}}
	r.Check(nil)
	if got, want := r.OutputFile("12", "/home/u"), filepath.Join("/home/u", "slurm-12.out"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	for _, directive := range []string{"#SBATCH --output=logs/%j.out", "#SBATCH -o logs/%j.out"} {
		path := writeScript(t, "#!/bin/bash\n"+directive+"\ntrue\n", 0o755)
		r = Request{Batch: true, Minutes: 60, Command: []string{path}}
		r.Check(nil)
		if got := r.OutputFile("12", "/home/u"); got != "" {
			t.Errorf("%s: script sets its own output, got %s", directive, got)
		}
	}
}

func TestSbatchDirectives(t *testing.T) {
	path := writeScript(t, "#!/bin/bash\n\n# comment\n#SBATCH --mem=4G\n#SBATCH  -c 2\necho hi\n#SBATCH --time=1:00:00\n", 0o755)
	got := sbatchDirectives(path)
	if strings.Join(got, "|") != "--mem=4G|-c 2" {
		t.Errorf("got %q; directives after the first command should be ignored", got)
	}
}

func TestParseJobID(t *testing.T) {
	for in, want := range map[string]string{"123\n": "123", "123;cluster\n": "123"} {
		if got := parseJobID(in); got != want {
			t.Errorf("parseJobID(%q) = %q; want %q", in, got, want)
		}
	}
}
