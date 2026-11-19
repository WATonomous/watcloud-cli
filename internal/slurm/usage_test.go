package slurm

import (
	"testing"

	"github.com/fatih/color"
)

// Real output: shard:1024 requested, 1.5 GiB allocated on delta-slurm1.
const sacctShardJob = `700512|s5ouyang|gpu-usage-probe|COMPLETED|billing=1,cpu=1,gres/shard=1024,gres/tmpdisk=100,mem=1G,node=1|
700512.extern||extern|COMPLETED|billing=1,cpu=1,gres/shard=1024,gres/tmpdisk=100,mem=1G,node=1|cpu=00:00:00,energy=0,fs/disk=7204,gres/gpumem=0,gres/gpuutil=0,mem=192K,pages=0,vmem=0
700512.0||gpu-usage-probe|COMPLETED|cpu=1,gres/shard=1024,gres/tmpdisk=100,mem=1G,node=1|cpu=00:01:15,energy=0,fs/disk=410805,gres/gpumem=1692M,gres/gpuutil=99,mem=32156K,pages=22,vmem=0
`

func TestParseSacctUsageShardJob(t *testing.T) {
	usages, err := ParseSacctUsage(sacctShardJob)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 {
		t.Fatalf("got %d jobs, want 1", len(usages))
	}
	u := usages[0]
	if u.JobID != "700512" || u.User != "s5ouyang" || u.State != "COMPLETED" || u.RequestedShards != 1024 {
		t.Errorf("job = %+v", u)
	}
	if !u.HasUsage || u.PeakMemMiB != 1692 || u.PeakUtil != 99 || !u.OverRequest() {
		t.Errorf("usage = %+v", u)
	}
}

func TestParseSacctUsageMultipleJobs(t *testing.T) {
	out := `100|alice|train|CANCELLED by 1500|cpu=4,gres/gpu=2,gres/gpu:rtx_3090=2,mem=16G,node=1|
100.batch||batch|CANCELLED|cpu=4,gres/gpu=2,mem=16G,node=1|cpu=01:00:00,gres/gpumem=21.50G,gres/gpuutil=100,mem=2G
101|bob|cpu-only|COMPLETED|cpu=1,mem=1G,node=1|
101.0||cpu-only|COMPLETED|cpu=1,mem=1G,node=1|cpu=00:00:01,mem=1M
102|bob|queued|PENDING|cpu=1,gres/shard=4096,mem=1G,node=1|
`
	usages, err := ParseSacctUsage(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 3 {
		t.Fatalf("got %d jobs, want 3", len(usages))
	}
	if train := usages[0]; train.State != "CANCELLED" || train.RequestedGPUs != 2 || train.PeakMemMiB != 21.5*1024 || train.OverRequest() {
		t.Errorf("train = %+v", train)
	}
	if cpuOnly := usages[1]; cpuOnly.requestsGPU() || cpuOnly.HasUsage {
		t.Errorf("cpu-only = %+v", cpuOnly)
	}
	if queued := usages[2]; queued.RequestedShards != 4096 || queued.HasUsage {
		t.Errorf("queued = %+v", queued)
	}
}

func TestMergeSstatUsage(t *testing.T) {
	u := &GPUUsage{JobID: "200", RequestedShards: 4096}
	mergeSstatUsage(u, "200.extern|cpu=00:00:00,gres/gpumem=0,gres/gpuutil=0\n"+
		"200.0|cpu=00:05:00,gres/gpumem=3000M,gres/gpuutil=42\n")
	if !u.HasUsage || u.PeakMemMiB != 3000 || u.PeakUtil != 42 || u.OverRequest() {
		t.Errorf("usage = %+v", u)
	}
}

func TestFormatGPUUsage(t *testing.T) {
	color.NoColor = true

	cases := []struct {
		u        GPUUsage
		withUser bool
		want     string
	}{
		{
			GPUUsage{JobID: "1", Name: "a", State: "COMPLETED", RequestedShards: 4096, PeakMemMiB: 22016, PeakUtil: 100, HasUsage: true},
			false,
			"job 1 (a, completed): requested 4.0 GiB of GPU memory, peaked at 21.5 GiB, utilisation 100%",
		},
		{
			GPUUsage{JobID: "2", Name: "b", State: "RUNNING", RequestedGPUs: 2, PeakMemMiB: 300, PeakUtil: 50, HasUsage: true},
			false,
			"job 2 (b, running): requested 2 whole GPUs, peaked at 300 MiB, utilisation 50%",
		},
		{
			GPUUsage{JobID: "3", Name: "c", State: "PENDING", RequestedShards: 1024},
			false,
			"job 3 (c, pending): requested 1.0 GiB of GPU memory, no GPU usage recorded yet",
		},
		{
			GPUUsage{JobID: "4", User: "alice", Name: "d", State: "COMPLETED", RequestedShards: 1024, PeakMemMiB: 2048, PeakUtil: 90, HasUsage: true},
			true,
			"job 4 (alice, d, completed): requested 1.0 GiB of GPU memory, peaked at 2.0 GiB, utilisation 90%",
		},
	}
	for _, c := range cases {
		if got := FormatGPUUsage(&c.u, c.withUser); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}
