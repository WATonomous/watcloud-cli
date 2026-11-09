package slurm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeSinfo replaces sinfo for one test.
func fakeSinfo(t *testing.T, timeout time.Duration, run func(ctx context.Context, args ...string) ([]byte, error)) {
	t.Helper()
	oldRun, oldTimeout := runSinfo, sinfoTimeout
	runSinfo, sinfoTimeout = run, timeout
	t.Cleanup(func() { runSinfo, sinfoTimeout = oldRun, oldTimeout })
}

func TestLoadClusterTimeout(t *testing.T) {
	// What sinfo does while the controller is down: no answer.
	fakeSinfo(t, 50*time.Millisecond, func(ctx context.Context, args ...string) ([]byte, error) {
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
	fakeSinfo(t, time.Second, func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, nil
	})
	if _, err := LoadCluster(); err == nil || !strings.Contains(err.Error(), "no nodes") {
		t.Errorf("got %v, want a no-nodes error", err)
	}
}

func TestLoadCluster(t *testing.T) {
	fakeSinfo(t, time.Second, func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "--Node") {
			return []byte(sinfoNodes), nil
		}
		return []byte(sinfoPartitions), nil
	})
	c, err := LoadCluster()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Nodes) != 6 || len(c.Partitions) != 2 {
		t.Errorf("got %d nodes and %d partitions, want 6 and 2", len(c.Nodes), len(c.Partitions))
	}
}
