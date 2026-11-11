package slurm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeSlurm replaces the Slurm commands for one test.
func fakeSlurm(t *testing.T, timeout time.Duration, run func(ctx context.Context, name string, args ...string) ([]byte, error)) {
	t.Helper()
	oldRun, oldTimeout := runSlurm, slurmTimeout
	runSlurm, slurmTimeout = run, timeout
	t.Cleanup(func() { runSlurm, slurmTimeout = oldRun, oldTimeout })
}

// clusterOutput answers each command as the cluster would.
func clusterOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	switch {
	case name == "sinfo":
		return []byte(sinfoPartitions), nil
	case strings.Join(args, " ") == "show config":
		return []byte("DefMemPerCPU            = 512\nDefMemPerNode           = UNLIMITED\n"), nil
	default:
		return []byte(scontrolNodes), nil
	}
}

func TestLoadClusterTimeout(t *testing.T) {
	// What Slurm's commands do while the controller is down: no answer.
	fakeSlurm(t, 50*time.Millisecond, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	_, err := LoadCluster()
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v, want a timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s; should give up at the timeout", elapsed)
	}
}

func TestLoadClusterNoNodes(t *testing.T) {
	fakeSlurm(t, time.Second, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "scontrol" && args[len(args)-1] == "node" {
			return nil, nil
		}
		return clusterOutput(ctx, name, args...)
	})
	if _, err := LoadCluster(); err == nil || !strings.Contains(err.Error(), "no compute nodes") {
		t.Errorf("got %v, want a no-nodes error", err)
	}
}

func TestLoadCluster(t *testing.T) {
	fakeSlurm(t, time.Second, clusterOutput)
	c, err := LoadCluster()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Nodes) != 6 || len(c.Partitions) != 2 || c.DefMemPerCPU != 512 {
		t.Errorf("got %d nodes, %d partitions, DefMemPerCPU %d; want 6, 2, 512",
			len(c.Nodes), len(c.Partitions), c.DefMemPerCPU)
	}
}
