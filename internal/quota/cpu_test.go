package quota

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectCPUContextForLimitedLoginSession(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/user.slice/user-2265.slice/session-1.scope\n")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "user-2265.slice", "cpu.max"), "100000 100000\n")
	if err := os.MkdirAll(filepath.Join(root, "user.slice", "user-2265.slice", "session-1.scope"), 0o755); err != nil {
		t.Fatal(err)
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	wantPath := filepath.Join(root, "user.slice", "user-2265.slice")
	if context.cgroupPath != wantPath || context.allocatedCPUs != 1 || !context.hasAllocation || !context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want path %q with one limited CPU", context, wantPath)
	}
}

func TestSelectCPUContextForSlurmJob(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_batch\n")
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), "0-2\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
		t.Fatal(err)
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.allocatedCPUs != 3 || !context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want unlimited Slurm job cgroup with 3 allocated CPUs", context)
	}
}

func TestSelectCPUContextIgnoresDelegatedJobNames(t *testing.T) {
	for _, suffix := range []string{
		"app.slice/job_42",
		"slurmstepd.scope/job_42",
		"system.slice/slurmstepd.scope/job_42",
	} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			userPath := "/user.slice/user-2265.slice"
			currentPath := userPath + "/user@2265.service/" + suffix
			userCgroup := filepath.Join(root, userPath)
			current := filepath.Join(root, currentPath)
			writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
			writeTestFile(t, filepath.Join(userCgroup, "cpu.max"), "100000 100000\n")
			writeTestFile(t, filepath.Join(current, "cpu.max"), "400000 100000\n")
			writeTestFile(t, filepath.Join(current, "cpuset.cpus.effective"), "0-7\n")

			context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			if context.cgroupPath != userCgroup || context.allocatedCPUs != 1 || !context.hasAllocation || !context.throttlingApplies {
				t.Fatalf("selectCPUContext() = %+v, want the per-user 1-CPU cap", context)
			}
		})
	}
}

func TestSelectCPUContextForPamSlurmAdoptSession(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_extern/user/task_0\n")
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), "4-5\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_extern", "user", "task_0"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLURM_JOB_ID", "")
	t.Setenv("SLURM_CPUS_ON_NODE", "")

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.allocatedCPUs != 2 || !context.hasAllocation {
		t.Fatalf("selectCPUContext() = %+v, want adopted job cgroup with two effective CPUs", context)
	}
}

func TestSelectCPUContextForSlurmJobWithCPULimit(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_batch\n")
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "200000 100000\n")
	writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), "0-2\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
		t.Fatal(err)
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.allocatedCPUs != 2 || !context.hasAllocation || !context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want the tighter 2-CPU cpu.max allocation", context)
	}
}

func TestSelectCPUContextForSlurmJobWithoutAllocation(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_batch\n")
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "max 100000\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
		t.Fatal(err)
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want Slurm job cgroup with unavailable allocation", context)
	}
}

func TestSelectSlurmCPUContextAllocationSources(t *testing.T) {
	tests := []struct {
		name          string
		cpuMax        string
		cpuSet        string
		wantCPUs      float64
		wantFound     bool
		wantThrottled bool
	}{
		{name: "CPU set only", cpuSet: "0-3\n", wantCPUs: 4, wantFound: true},
		{name: "CPU cap only", cpuMax: "150000 100000\n", wantCPUs: 1.5, wantFound: true, wantThrottled: true},
		{name: "neither source", wantFound: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jobCgroup := t.TempDir()
			if test.cpuMax != "" {
				writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), test.cpuMax)
			}
			if test.cpuSet != "" {
				writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), test.cpuSet)
			}

			context, err := selectSlurmCPUContext(jobCgroup, os.ReadFile)
			if err != nil {
				t.Fatalf("selectSlurmCPUContext() error = %v", err)
			}
			if context.allocatedCPUs != test.wantCPUs || context.hasAllocation != test.wantFound || context.throttlingApplies != test.wantThrottled {
				t.Fatalf("selectSlurmCPUContext() = %+v, want CPUs=%v found=%v throttled=%v", context, test.wantCPUs, test.wantFound, test.wantThrottled)
			}
		})
	}
}

func TestSelectCPUContextWithoutAllocation(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/unlimited.scope\n")
	current := filepath.Join(root, "unlimited.scope")
	writeTestFile(t, filepath.Join(current, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != current || context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want current unlimited cgroup without allocation", context)
	}
}

func TestSelectCPUContextDoesNotTrustSlurmEnvironment(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/user.slice/user-2265.slice/session-1.scope\n")
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")
	writeTestFile(t, filepath.Join(userCgroup, "cpu.max"), "100000 100000\n")
	if err := os.MkdirAll(filepath.Join(userCgroup, "session-1.scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLURM_JOB_ID", "77")
	t.Setenv("SLURM_CPUS_ON_NODE", "8")

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != userCgroup || context.allocatedCPUs != 1 {
		t.Fatalf("selectCPUContext() = %+v, want login allocation despite any external Slurm environment", context)
	}
}

func TestSelectCPUContextUsesTightestLoginLimit(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/user.slice/user-2265.slice/session-1.scope\n")
	sessionCgroup := filepath.Join(root, "user.slice", "user-2265.slice", "session-1.scope")
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")
	writeTestFile(t, filepath.Join(sessionCgroup, "cpu.max"), "400000 100000\n")
	writeTestFile(t, filepath.Join(userCgroup, "cpu.max"), "100000 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "cpu.max"), "50000 100000\n")

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != userCgroup || context.allocatedCPUs != 1 {
		t.Fatalf("selectCPUContext() = %+v, want tightest per-user limit at %q", context, userCgroup)
	}
}

func TestSelectCPUContextSlurmErrors(t *testing.T) {
	tests := []struct {
		name    string
		cpuMax  string
		cpuSet  string
		wantErr string
	}{
		{name: "invalid CPU set", cpuMax: "max 100000\n", cpuSet: "0-bad\n", wantErr: "effective CPU set"},
		{name: "invalid CPU limit", cpuMax: "bad 100000\n", cpuSet: "0-1\n", wantErr: "Slurm job CPU limit"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_batch\n")
			jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
			writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), test.cpuMax)
			writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), test.cpuSet)
			if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
				t.Fatal(err)
			}

			_, err := selectCPUContext(root, procSelfCgroup, os.ReadFile)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("selectCPUContext() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestCPUUsageEndToEnd(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/user.slice/user-2265.slice/session-1.scope\n")
	limitedCgroup := filepath.Join(root, "user.slice", "user-2265.slice")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(limitedCgroup, "cpu.max"), "100000 100000\n")
	if err := os.MkdirAll(filepath.Join(limitedCgroup, "session-1.scope"), 0o755); err != nil {
		t.Fatal(err)
	}

	stats := [][]byte{
		[]byte("usage_usec 1000000\nnr_periods 40\nnr_throttled 4\nthrottled_usec 100\n"),
		[]byte("usage_usec 4800000\nnr_periods 50\nnr_throttled 5\nthrottled_usec 200\n"),
	}
	statIndex := 0
	readFile := func(path string) ([]byte, error) {
		if path == filepath.Join(limitedCgroup, "cpu.stat") {
			value := stats[statIndex]
			statIndex++
			return value, nil
		}
		return os.ReadFile(path)
	}
	times := []time.Time{
		time.Unix(0, 0),
		time.Unix(0, 0),
		time.Unix(2, 0),
		time.Unix(2, 0),
	}
	timeIndex := 0
	var output bytes.Buffer

	err := cpuUsage(cpuUsageOptions{
		cgroupRoot:     root,
		procSelfCgroup: procSelfCgroup,
		sampleDuration: time.Second,
		output:         &output,
		readFile:       readFile,
		now: func() time.Time {
			value := times[timeIndex]
			timeIndex++
			return value
		},
		sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("cpuUsage() error = %v", err)
	}
	for _, want := range []string{
		"Using 1.9 of 1 CPUs",
		"Throttled in 10.0% of CPU periods for this allocation",
		"CPU throttling occurred during this sample",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("cpuUsage() output %q does not contain %q", output.String(), want)
		}
	}
}

func TestCPUUsageForSlurmJobUsesJobScope(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	jobPath := "/system.slice/slurmstepd.scope/job_77"
	taskPath := jobPath + "/step_0/user/task_0"
	writeTestFile(t, procSelfCgroup, "0::"+taskPath+"\n")
	jobCgroup := filepath.Join(root, jobPath)
	taskCgroup := filepath.Join(root, taskPath)

	// After ConstrainCores is enabled, the job's effective set reflects its
	// allocation. A task can be narrower, and the node can still expose 60 CPUs.
	writeTestFile(t, filepath.Join(root, "cpuset.cpus.effective"), "0-59\n")
	writeTestFile(t, filepath.Join(jobCgroup, "cpuset.cpus.effective"), "56-57\n")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(taskCgroup, "cpuset.cpus.effective"), "56\n")
	writeTestFile(t, filepath.Join(taskCgroup, "cpu.max"), "max 100000\n")
	t.Setenv("SLURM_JOB_ID", "999")
	t.Setenv("SLURM_CPUS_ON_NODE", "60")

	// Synthetic job-wide counters include work outside the current task.
	stats := []string{"usage_usec 1000000\n", "usage_usec 4800000\n"}
	statReads := 0
	readFile := func(path string) ([]byte, error) {
		if filepath.Base(path) == "cpu.stat" {
			if path != filepath.Join(jobCgroup, "cpu.stat") {
				t.Fatalf("sampled %q instead of the job's aggregate usage", path)
			}
			if statReads >= len(stats) {
				t.Fatal("read more than two CPU snapshots")
			}
			value := stats[statReads]
			statReads++
			return []byte(value), nil
		}
		return os.ReadFile(path)
	}
	now := time.Unix(0, 0)
	var output bytes.Buffer
	err := cpuUsage(cpuUsageOptions{
		cgroupRoot:     root,
		procSelfCgroup: procSelfCgroup,
		sampleDuration: defaultCPUSampleDuration,
		output:         &output,
		readFile:       readFile,
		now:            func() time.Time { return now },
		sleep: func(duration time.Duration) {
			if duration != 2*time.Second {
				t.Errorf("sample duration = %v, want 2s", duration)
			}
			now = now.Add(duration)
		},
	})
	if err != nil {
		t.Fatalf("cpuUsage() error = %v", err)
	}
	if statReads != 2 {
		t.Errorf("read %d CPU snapshots, want 2", statReads)
	}
	if !strings.Contains(output.String(), "\nUsing 1.9 of 2 CPUs\n") {
		t.Errorf("cpuUsage() output = %q, want job-wide usage and allocation", output.String())
	}
	if strings.Contains(output.String(), "Throttled") || strings.Contains(output.String(), "CPU cap") {
		t.Errorf("cpuUsage() reported CPU-time throttling without a finite cap: %q", output.String())
	}
}

func TestReadCPUStat(t *testing.T) {
	stat, err := readCPUStat("/cgroup", func(string) ([]byte, error) {
		return []byte("usage_usec 123\nuser_usec 100\nsystem_usec 23\nnr_periods 10\nnr_throttled 4\nthrottled_usec 56\n"), nil
	})
	if err != nil {
		t.Fatalf("readCPUStat() error = %v", err)
	}
	if stat.usageUsec != 123 || stat.nrPeriods != 10 || stat.nrThrottled != 4 || stat.throttledUsec != 56 || !stat.hasNrPeriods || !stat.hasNrThrottled || !stat.hasThrottledUsec {
		t.Fatalf("readCPUStat() = %+v", stat)
	}
}

func TestReadCPUStatWithoutOptionalThrottlingFields(t *testing.T) {
	stat, err := readCPUStat("/cgroup", func(string) ([]byte, error) {
		return []byte("usage_usec 123\n"), nil
	})
	if err != nil {
		t.Fatalf("readCPUStat() error = %v", err)
	}
	if stat.hasNrPeriods || stat.hasNrThrottled || stat.hasThrottledUsec {
		t.Fatalf("readCPUStat() = %+v, want absent optional fields", stat)
	}
}

func TestReadCPUStatErrors(t *testing.T) {
	wantReadError := errors.New("read failed")
	tests := []struct {
		name     string
		content  string
		readErr  error
		wantErr  string
		wantWrap error
	}{
		{name: "read failure", readErr: wantReadError, wantWrap: wantReadError},
		{name: "missing usage", content: "user_usec 100\n", wantErr: "does not contain usage_usec"},
		{name: "malformed required line", content: "usage_usec\n", wantErr: "invalid usage_usec line"},
		{name: "invalid value", content: "usage_usec nope\n", wantErr: "invalid usage_usec value"},
		{name: "malformed known optional line", content: "usage_usec 1\nnr_periods\n", wantErr: "invalid nr_periods line"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readCPUStat("/cgroup", func(string) ([]byte, error) {
				return []byte(test.content), test.readErr
			})
			if test.wantWrap != nil {
				if !errors.Is(err, test.wantWrap) {
					t.Fatalf("readCPUStat() error = %v, want wrapped %v", err, test.wantWrap)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("readCPUStat() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestReadCPUStatIgnoresUnknownFields(t *testing.T) {
	stat, err := readCPUStat("/cgroup", func(string) ([]byte, error) {
		return []byte("usage_usec 123\nfuture_field value with another format\nfuture_flag\n"), nil
	})
	if err != nil {
		t.Fatalf("readCPUStat() error = %v", err)
	}
	if stat.usageUsec != 123 {
		t.Fatalf("readCPUStat() usageUsec = %d, want 123", stat.usageUsec)
	}
}

func TestMeasureCPUUsageUsesSnapshotMidpoints(t *testing.T) {
	stats := []cpuStat{{usageUsec: 1_000_000}, {usageUsec: 5_180_000}}
	readIndex := 0
	times := []time.Time{
		time.Unix(0, 0),
		time.Unix(0, 0).Add(200 * time.Millisecond),
		time.Unix(0, 0).Add(2100 * time.Millisecond),
		time.Unix(0, 0).Add(2500 * time.Millisecond),
	}
	timeIndex := 0
	var slept time.Duration

	measurement, err := measureCPUUsage(
		func() (cpuStat, error) {
			stat := stats[readIndex]
			readIndex++
			return stat, nil
		},
		time.Second,
		func() time.Time {
			value := times[timeIndex]
			timeIndex++
			return value
		},
		func(duration time.Duration) { slept = duration },
		false,
	)
	if err != nil {
		t.Fatalf("measureCPUUsage() error = %v", err)
	}
	if math.Abs(measurement.usedCPUs-1.9) > 1e-9 {
		t.Fatalf("measureCPUUsage() usedCPUs = %v, want 1.9", measurement.usedCPUs)
	}
	if slept != time.Second {
		t.Fatalf("measureCPUUsage() slept %s, want 1s", slept)
	}
}

func TestMeasureCPUUsageReportsMeaningfulThrottling(t *testing.T) {
	tests := []struct {
		name                 string
		first                cpuStat
		second               cpuStat
		throttlingApplies    bool
		wantDuringSample     bool
		wantEver             bool
		wantPeriodRatio      bool
		wantThrottledPercent float64
	}{
		{
			name:                 "limited and throttled",
			first:                cpuStat{usageUsec: 1, nrPeriods: 20, nrThrottled: 2, throttledUsec: 3, hasNrPeriods: true, hasNrThrottled: true, hasThrottledUsec: true},
			second:               cpuStat{usageUsec: 2, nrPeriods: 30, nrThrottled: 3, throttledUsec: 4, hasNrPeriods: true, hasNrThrottled: true, hasThrottledUsec: true},
			throttlingApplies:    true,
			wantDuringSample:     true,
			wantEver:             true,
			wantPeriodRatio:      true,
			wantThrottledPercent: 10,
		},
		{
			name:                 "limited with earlier throttling",
			first:                cpuStat{usageUsec: 1, nrPeriods: 10, nrThrottled: 2, hasNrPeriods: true, hasNrThrottled: true},
			second:               cpuStat{usageUsec: 2, nrPeriods: 20, nrThrottled: 2, hasNrPeriods: true, hasNrThrottled: true},
			throttlingApplies:    true,
			wantEver:             true,
			wantPeriodRatio:      true,
			wantThrottledPercent: 10,
		},
		{
			name:              "unlimited omits throttling",
			first:             cpuStat{usageUsec: 1, nrPeriods: 20, nrThrottled: 2, hasNrPeriods: true, hasNrThrottled: true},
			second:            cpuStat{usageUsec: 2, nrPeriods: 30, nrThrottled: 3, hasNrPeriods: true, hasNrThrottled: true},
			throttlingApplies: false,
		},
		{
			name:              "throttled time without period counters",
			first:             cpuStat{usageUsec: 1, throttledUsec: 3, hasThrottledUsec: true},
			second:            cpuStat{usageUsec: 2, throttledUsec: 3, hasThrottledUsec: true},
			throttlingApplies: true,
			wantEver:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stats := []cpuStat{test.first, test.second}
			readIndex := 0
			times := []time.Time{
				time.Unix(0, 0),
				time.Unix(0, 0),
				time.Unix(1, 0),
				time.Unix(1, 0),
			}
			timeIndex := 0
			measurement, err := measureCPUUsage(
				func() (cpuStat, error) {
					stat := stats[readIndex]
					readIndex++
					return stat, nil
				},
				time.Second,
				func() time.Time {
					value := times[timeIndex]
					timeIndex++
					return value
				},
				func(time.Duration) {},
				test.throttlingApplies,
			)
			if err != nil {
				t.Fatalf("measureCPUUsage() error = %v", err)
			}
			if measurement.throttledDuringSample != test.wantDuringSample ||
				measurement.everThrottled != test.wantEver ||
				measurement.hasThrottledPeriodRatio != test.wantPeriodRatio ||
				math.Abs(measurement.throttledPeriodsPercent-test.wantThrottledPercent) > 1e-9 {
				t.Fatalf("measureCPUUsage() = %+v, want during=%v ever=%v ratio=%v percent=%v", measurement, test.wantDuringSample, test.wantEver, test.wantPeriodRatio, test.wantThrottledPercent)
			}
		})
	}
}

func TestMeasureCPUUsageErrors(t *testing.T) {
	tests := []struct {
		name    string
		first   cpuStat
		second  cpuStat
		elapsed time.Duration
		limited bool
		wantErr string
	}{
		{name: "zero elapsed", first: cpuStat{usageUsec: 1}, second: cpuStat{usageUsec: 2}, wantErr: "elapsed time must be positive"},
		{name: "negative elapsed", first: cpuStat{usageUsec: 1}, second: cpuStat{usageUsec: 2}, elapsed: -time.Second, wantErr: "elapsed time must be positive"},
		{name: "usage regression", first: cpuStat{usageUsec: 2}, second: cpuStat{usageUsec: 1}, elapsed: time.Second, wantErr: "usage counter decreased"},
		{
			name:    "period count regression",
			first:   cpuStat{usageUsec: 1, nrPeriods: 2, hasNrPeriods: true},
			second:  cpuStat{usageUsec: 2, nrPeriods: 1, hasNrPeriods: true},
			elapsed: time.Second,
			limited: true,
			wantErr: "period counter decreased",
		},
		{
			name:    "throttle count regression",
			first:   cpuStat{usageUsec: 1, nrThrottled: 2, hasNrThrottled: true},
			second:  cpuStat{usageUsec: 2, nrThrottled: 1, hasNrThrottled: true},
			elapsed: time.Second,
			limited: true,
			wantErr: "throttling counter decreased",
		},
		{
			name:    "throttled time regression",
			first:   cpuStat{usageUsec: 1, throttledUsec: 2, hasThrottledUsec: true},
			second:  cpuStat{usageUsec: 2, throttledUsec: 1, hasThrottledUsec: true},
			elapsed: time.Second,
			limited: true,
			wantErr: "throttled time counter decreased",
		},
		{
			name:    "throttle count exceeds periods",
			first:   cpuStat{usageUsec: 1},
			second:  cpuStat{usageUsec: 2, nrPeriods: 1, nrThrottled: 2, hasNrPeriods: true, hasNrThrottled: true},
			elapsed: time.Second,
			limited: true,
			wantErr: "exceeds the number of CPU periods",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stats := []cpuStat{test.first, test.second}
			readIndex := 0
			times := []time.Time{
				time.Unix(0, 0),
				time.Unix(0, 0),
				time.Unix(0, 0).Add(test.elapsed),
				time.Unix(0, 0).Add(test.elapsed),
			}
			timeIndex := 0
			_, err := measureCPUUsage(
				func() (cpuStat, error) {
					stat := stats[readIndex]
					readIndex++
					return stat, nil
				},
				time.Second,
				func() time.Time {
					value := times[timeIndex]
					timeIndex++
					return value
				},
				func(time.Duration) {},
				test.limited,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("measureCPUUsage() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestMeasureCPUUsageReadErrors(t *testing.T) {
	want := errors.New("read failed")
	_, err := measureCPUUsage(
		func() (cpuStat, error) { return cpuStat{}, want },
		time.Second,
		time.Now,
		func(time.Duration) {},
		false,
	)
	if !errors.Is(err, want) {
		t.Fatalf("first read error = %v, want %v", err, want)
	}

	reads := 0
	_, err = measureCPUUsage(
		func() (cpuStat, error) {
			reads++
			if reads == 2 {
				return cpuStat{}, want
			}
			return cpuStat{usageUsec: 1}, nil
		},
		time.Second,
		time.Now,
		func(time.Duration) {},
		false,
	)
	if !errors.Is(err, want) {
		t.Fatalf("second read error = %v, want %v", err, want)
	}
}

func TestPrintCPUUsage(t *testing.T) {
	tests := []struct {
		name        string
		measurement cpuMeasurement
		context     cpuContext
		want        []string
		doNotWant   []string
	}{
		{
			name:        "allocated",
			measurement: cpuMeasurement{usedCPUs: 1.9},
			context:     cpuContext{allocatedCPUs: 3, hasAllocation: true},
			want:        []string{"CPU Usage", "Using 1.9 of 3 CPUs"},
			doNotWant:   []string{"allocation unavailable", "Limited by"},
		},
		{
			name: "fractional allocation and throttled",
			measurement: cpuMeasurement{
				usedCPUs:                1.25,
				throttledDuringSample:   true,
				everThrottled:           true,
				hasThrottledPeriodRatio: true,
				throttledPeriodsPercent: 12.34,
			},
			context: cpuContext{allocatedCPUs: 1.5, hasAllocation: true},
			want: []string{
				"Using 1.2 of 1.5 CPUs",
				"Throttled in 12.3% of CPU periods for this allocation",
				"CPU throttling occurred during this sample",
			},
		},
		{
			name:        "throttled without period ratio",
			measurement: cpuMeasurement{usedCPUs: 0.5, everThrottled: true},
			context:     cpuContext{allocatedCPUs: 1, hasAllocation: true},
			want:        []string{"Limited by the CPU cap for this allocation"},
		},
		{
			name:        "allocation unavailable",
			measurement: cpuMeasurement{usedCPUs: 0.42},
			context:     cpuContext{},
			want:        []string{"Using 0.4 CPUs (allocation unavailable)"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			printCPUUsage(&output, test.measurement, test.context)
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("printCPUUsage() output %q does not contain %q", output.String(), want)
				}
			}
			for _, doNotWant := range test.doNotWant {
				if strings.Contains(output.String(), doNotWant) {
					t.Errorf("printCPUUsage() output %q unexpectedly contains %q", output.String(), doNotWant)
				}
			}
		})
	}
}

func TestFormatCPUCount(t *testing.T) {
	tests := []struct {
		cpus float64
		want string
	}{
		{cpus: 3, want: "3"},
		{cpus: 1.5, want: "1.5"},
		{cpus: 1.25, want: "1.25"},
		{cpus: 1.0 / 3.0, want: "0.33"},
	}

	for _, test := range tests {
		if got := formatCPUCount(test.cpus); got != test.want {
			t.Errorf("formatCPUCount(%v) = %q, want %q", test.cpus, got, test.want)
		}
	}
}
