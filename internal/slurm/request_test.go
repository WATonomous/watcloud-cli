package slurm

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	ok := map[string]int64{
		"512M":   512,
		"512MiB": 512,
		"20G":    20480,
		"20gb":   20480,
		"1.5G":   1536,
		"1T":     1 << 20,
		" 8G ":   8192,
	}
	for in, want := range ok {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"20", "", "G", "20X", "-1G", "0G"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) succeeded; want error", in)
		}
	}
}

func TestParseTime(t *testing.T) {
	ok := map[string]int{
		"30m":        30,
		"4h":         240,
		"1h30m":      90,
		"2d":         2880,
		"1d12h":      2160,
		"30:00":      30,
		"0:30":       1,
		"2:00:00":    120,
		"1-00:00:00": 1440,
		"1-12":       2160,
		"1-12:30":    2190,
	}
	for in, want := range ok {
		if got, err := ParseTime(in); err != nil || got != want {
			t.Errorf("ParseTime(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"30", "", "0m", "1:2:3:4", "h", "abc", "infinite"} {
		if _, err := ParseTime(in); err == nil {
			t.Errorf("ParseTime(%q) succeeded; want error", in)
		}
	}
}

func TestFormatTime(t *testing.T) {
	for minutes, want := range map[int]string{30: "0:30:00", 240: "4:00:00", 2190: "1-12:30:00"} {
		if got := formatTime(minutes); got != want {
			t.Errorf("formatTime(%d) = %q; want %q", minutes, got, want)
		}
	}
}

func TestArgs(t *testing.T) {
	r := Request{CPUs: 4, MemMiB: 16384, GPUMemMiB: 8192, GPUType: "rtx_3090", DiskMiB: 40960, Minutes: 240, Shell: "/bin/bash"}
	want := "srun --cpus-per-task 4 --mem 16G --gres shard:rtx_3090:8192,tmpdisk:40960 --time 4:00:00 --pty /bin/bash"
	if got := ShellJoin(r.Args()); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	r = Request{GPUs: 1, Command: []string{"python", "train.py"}}
	want = "srun --gres gpu:1 python train.py"
	if got := ShellJoin(r.Args()); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestArgsDocker(t *testing.T) {
	r := Request{Shell: "/bin/zsh", Docker: true}
	if _, err := r.Check(nil); err != nil {
		t.Fatal(err)
	}
	args := r.Args()
	if !strings.Contains(strings.Join(args, " "), "tmpdisk:20480") {
		t.Errorf("--docker without --disk should default to 20 GiB: %v", args)
	}
	tail := args[len(args)-6:]
	if tail[0] != "--pty" || tail[1] != "bash" || tail[2] != "-c" || tail[4] != "watcloud" {
		t.Errorf("unexpected wrapper: %q", tail)
	}
	if args[len(args)-1] != "/bin/zsh" {
		t.Errorf("shell should run last, got %q", args[len(args)-1])
	}
	if strings.Contains(tail[3], "exit 1") {
		t.Errorf("interactive shell should still start when dockerd fails")
	}

	r = Request{Docker: true, Command: []string{"docker", "build", "."}}
	args = r.Args()
	if !strings.Contains(args[len(args)-5], "exit 1") {
		t.Errorf("a command should not run when dockerd fails")
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"tmpdisk:100": "tmpdisk:100",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		`$HOME`:       `'$HOME'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s; want %s", in, got, want)
		}
	}
}

func TestCheckErrors(t *testing.T) {
	for name, r := range map[string]Request{
		"gpus and gpu-mem": {GPUs: 1, GPUMemMiB: 1024},
		"type without gpu": {GPUType: "rtx_3090"},
		"dense":            {Partition: "compute_dense"},
	} {
		if _, err := r.Check(nil); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// From `sinfo -N -o "%N|%c|%m|%G"` on the cluster, October 2026.
const sinfoNodes = `delta-slurm1|6|40000|gpu:rtx_2080_ti:2(S:0-5),shard:rtx_2080_ti:22K(S:0-5),tmpdisk:268K
delta-slurm1|6|40000|gpu:rtx_2080_ti:2(S:0-5),shard:rtx_2080_ti:22K(S:0-5),tmpdisk:268K
thor-slurm1|60|211349|gpu:rtx_3090:2(S:0-59),shard:rtx_3090:48K(S:0-59),tmpdisk:968K
tr-slurm2|60|56000|gpu:gtx_1080:1(S:0-59),shard:gtx_1080:8K(S:0-59),tmpdisk:68K
trpro-slurm1|80|390764|gpu:rtx_3090:4(S:0-79),shard:rtx_3090:96K(S:0-79),tmpdisk:968K
trpro-slurm2|40|64283|gpu:rtx_4090:1(S:0-39),shard:rtx_4090:24564(S:0-39),tmpdisk:68K
wato2-slurm1|6|19990|gpu:rtx_2080_ti:1(S:0-5),shard:rtx_2080_ti:11K(S:0-5),tmpdisk:100K
`

const sinfoPartitions = `compute*|30:00|1-00:00:00
compute_dense|30:00|7-00:00:00
`

func testCluster() *Cluster {
	return &Cluster{Nodes: parseNodes(sinfoNodes), Partitions: parsePartitions(sinfoPartitions)}
}

func TestParseNodes(t *testing.T) {
	nodes := parseNodes(sinfoNodes)
	if len(nodes) != 6 {
		t.Fatalf("got %d nodes, want 6 (duplicates dropped)", len(nodes))
	}
	thor := nodes[1]
	if thor.Name != "thor-slurm1" || thor.CPUs != 60 || thor.MemMiB != 211349 ||
		thor.TmpDiskMiB != 968*1024 || thor.GPUs["rtx_3090"] != 2 || thor.Shards["rtx_3090"] != 48*1024 {
		t.Errorf("bad parse: %+v", thor)
	}
	if got := nodes[4].maxGPUMem(""); got != 24564 {
		t.Errorf("rtx_4090 per-GPU memory = %d, want 24564", got)
	}
}

func TestParsePartitions(t *testing.T) {
	p, ok := testCluster().partition("")
	if !ok || p.Name != "compute" || p.DefaultMinutes != 30 || p.MaxMinutes != 1440 {
		t.Errorf("default partition = %+v, %v", p, ok)
	}
}

func TestClusterCheck(t *testing.T) {
	c := testCluster()
	fits := map[string]Request{
		"small":           {CPUs: 4, MemMiB: 8192},
		"big disk":        {DiskMiB: 500 * 1024},
		"one 4090 share":  {GPUMemMiB: 24 * 1024},
		"typed gpus":      {GPUs: 4, GPUType: "rtx_3090"},
		"time within max": {Minutes: 1440},
	}
	for name, r := range fits {
		if _, err := c.check(&r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	fails := map[string]struct {
		r    Request
		want string
	}{
		"disk too big":      {Request{DiskMiB: 2 << 20}, "scratch disk"},
		"gpu-mem over card": {Request{GPUMemMiB: 30 * 1024}, "one GPU"},
		"typed share":       {Request{GPUMemMiB: 16 * 1024, GPUType: "rtx_2080_ti"}, "one rtx_2080_ti GPU"},
		"unknown type":      {Request{GPUs: 1, GPUType: "a100"}, "no GPU type"},
		"too long":          {Request{Minutes: 2 * 1440}, "longest job"},
		"no partition":      {Request{Partition: "gpu"}, "no partition"},
		"not on one node":   {Request{CPUs: 80, GPUType: "rtx_4090", GPUs: 1}, "no single node"},
	}
	for name, tc := range fails {
		_, err := c.check(&tc.r)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, tc.want)
		}
	}
}

func TestDefaultTimeNotice(t *testing.T) {
	notices, err := testCluster().check(&Request{})
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Text, "30 min") {
		t.Errorf("got %v, %v", notices, err)
	}
}
