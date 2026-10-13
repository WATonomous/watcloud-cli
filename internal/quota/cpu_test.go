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

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(string) string { return "" })
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
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"SLURM_JOB_ID":       "42",
		"SLURM_CPUS_ON_NODE": "3",
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(name string) string {
		return environment[name]
	})
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.allocatedCPUs != 3 || !context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want unlimited Slurm job cgroup with 3 allocated CPUs", context)
	}
}

func TestSelectCPUContextForSlurmJobWithCPULimit(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/system.slice/slurmstepd.scope/job_42/step_batch\n")
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "200000 100000\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch"), 0o755); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"SLURM_JOB_ID":       "42",
		"SLURM_CPUS_ON_NODE": "3",
	}

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(name string) string {
		return environment[name]
	})
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if !context.throttlingApplies {
		t.Fatal("selectCPUContext() throttlingApplies = false, want true for finite Slurm cpu.max")
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

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(name string) string {
		if name == "SLURM_JOB_ID" {
			return "42"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want Slurm job cgroup with unavailable allocation", context)
	}
}

func TestSelectCPUContextWithoutAllocation(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	writeTestFile(t, procSelfCgroup, "0::/unlimited.scope\n")
	current := filepath.Join(root, "unlimited.scope")
	writeTestFile(t, filepath.Join(current, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")

	context, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(string) string { return "" })
	if err != nil {
		t.Fatalf("selectCPUContext() error = %v", err)
	}
	if context.cgroupPath != current || context.hasAllocation || context.throttlingApplies {
		t.Fatalf("selectCPUContext() = %+v, want current unlimited cgroup without allocation", context)
	}
}

func TestSelectCPUContextSlurmErrors(t *testing.T) {
	tests := []struct {
		name       string
		cgroupPath string
		cpus       string
		wantErr    string
	}{
		{name: "missing job ancestor", cgroupPath: "/system.slice/other.scope", cpus: "2", wantErr: "locate Slurm job cgroup"},
		{name: "zero allocation", cgroupPath: "/system.slice/job_42/step_batch", cpus: "0", wantErr: "must be greater than zero"},
		{name: "invalid allocation", cgroupPath: "/system.slice/job_42/step_batch", cpus: "two", wantErr: "invalid SLURM_CPUS_ON_NODE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			writeTestFile(t, procSelfCgroup, "0::"+test.cgroupPath+"\n")
			jobCgroup := filepath.Join(root, "system.slice", "job_42")
			writeTestFile(t, filepath.Join(jobCgroup, "cpu.max"), "max 100000\n")
			if err := os.MkdirAll(filepath.Join(root, strings.TrimPrefix(test.cgroupPath, "/")), 0o755); err != nil {
				t.Fatal(err)
			}
			environment := map[string]string{
				"SLURM_JOB_ID":       "42",
				"SLURM_CPUS_ON_NODE": test.cpus,
			}

			_, err := selectCPUContext(root, procSelfCgroup, os.ReadFile, func(name string) string {
				return environment[name]
			})
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
		[]byte("usage_usec 1000000\nnr_throttled 4\nthrottled_usec 100\n"),
		[]byte("usage_usec 4800000\nnr_throttled 5\nthrottled_usec 200\n"),
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
		getenv:         func(string) string { return "" },
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
		"Using 1.90 of 1 CPUs",
		"Limited by the CPU cap during this sample",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("cpuUsage() output %q does not contain %q", output.String(), want)
		}
	}
}

func TestReadCPUStat(t *testing.T) {
	stat, err := readCPUStat("/cgroup", func(string) ([]byte, error) {
		return []byte("usage_usec 123\nuser_usec 100\nsystem_usec 23\nnr_throttled 4\nthrottled_usec 56\n"), nil
	})
	if err != nil {
		t.Fatalf("readCPUStat() error = %v", err)
	}
	if stat.usageUsec != 123 || stat.nrThrottled != 4 || stat.throttledUsec != 56 || !stat.hasNrThrottled || !stat.hasThrottledUsec {
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
	if stat.hasNrThrottled || stat.hasThrottledUsec {
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
		{name: "malformed line", content: "usage_usec\n", wantErr: "invalid cpu.stat line"},
		{name: "invalid value", content: "usage_usec nope\n", wantErr: "invalid usage_usec value"},
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
		name              string
		first             cpuStat
		second            cpuStat
		throttlingApplies bool
		wantThrottled     bool
	}{
		{
			name:              "limited and throttled",
			first:             cpuStat{usageUsec: 1, nrThrottled: 2, throttledUsec: 3, hasNrThrottled: true, hasThrottledUsec: true},
			second:            cpuStat{usageUsec: 2, nrThrottled: 3, throttledUsec: 4, hasNrThrottled: true, hasThrottledUsec: true},
			throttlingApplies: true,
			wantThrottled:     true,
		},
		{
			name:              "limited but not throttled",
			first:             cpuStat{usageUsec: 1, nrThrottled: 2, hasNrThrottled: true},
			second:            cpuStat{usageUsec: 2, nrThrottled: 2, hasNrThrottled: true},
			throttlingApplies: true,
		},
		{
			name:              "unlimited omits throttling",
			first:             cpuStat{usageUsec: 1, nrThrottled: 2, hasNrThrottled: true},
			second:            cpuStat{usageUsec: 2, nrThrottled: 3, hasNrThrottled: true},
			throttlingApplies: false,
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
			if measurement.throttled != test.wantThrottled {
				t.Fatalf("measureCPUUsage() throttled = %v, want %v", measurement.throttled, test.wantThrottled)
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
			want:        []string{"CPU Usage", "Using 1.90 of 3 CPUs"},
			doNotWant:   []string{"allocation unavailable", "Limited by"},
		},
		{
			name:        "fractional allocation and throttled",
			measurement: cpuMeasurement{usedCPUs: 1.25, throttled: true},
			context:     cpuContext{allocatedCPUs: 1.5, hasAllocation: true},
			want:        []string{"Using 1.25 of 1.5 CPUs", "Limited by the CPU cap during this sample"},
		},
		{
			name:        "allocation unavailable",
			measurement: cpuMeasurement{usedCPUs: 0.42},
			context:     cpuContext{},
			want:        []string{"Using 0.42 CPUs (allocation unavailable)"},
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
