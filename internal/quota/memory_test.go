package quota

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestSelectMemoryContextForLimitedLoginSession(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/user.slice/user-2265.slice/session-1.scope"
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session-1.scope")
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	writeTestFile(t, filepath.Join(current, "memory.max"), "max\n")
	writeTestFile(t, filepath.Join(userCgroup, "memory.max"), "2147483648\n")
	// Shared limits above the user slice must not select other users' usage.
	writeTestFile(t, filepath.Join(root, "user.slice", "memory.max"), "1073741824\n")

	context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectMemoryContext() error = %v", err)
	}
	if context.cgroupPath != userCgroup || context.allocation != memoryAllocationFinite || context.allocatedBytes != 2*bytesPerGiB {
		t.Fatalf("selectMemoryContext() = %+v, want per-user finite cgroup %q with 2 GiB", context, userCgroup)
	}
}

func TestSelectMemoryContextForSlurmJob(t *testing.T) {
	tests := []struct {
		name           string
		memoryMax      string
		writeMax       bool
		wantAllocation memoryAllocationKind
		wantBytes      uint64
	}{
		{name: "finite", memoryMax: "4294967296\n", writeMax: true, wantAllocation: memoryAllocationFinite, wantBytes: 4 * bytesPerGiB},
		{name: "unlimited", memoryMax: "max\n", writeMax: true, wantAllocation: memoryAllocationUnlimited},
		{name: "unavailable", wantAllocation: memoryAllocationUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			currentPath := "/system.slice/slurmstepd.scope/job_42/step_batch/user/task_0"
			jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")

			writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
			if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch", "user", "task_0"), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.writeMax {
				writeTestFile(t, filepath.Join(jobCgroup, "memory.max"), test.memoryMax)
			}

			context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
			if err != nil {
				t.Fatalf("selectMemoryContext() error = %v", err)
			}
			if context.cgroupPath != jobCgroup || context.allocation != test.wantAllocation || context.allocatedBytes != test.wantBytes {
				t.Fatalf("selectMemoryContext() = %+v, want job cgroup %q allocation %v and %d bytes", context, jobCgroup, test.wantAllocation, test.wantBytes)
			}
		})
	}
}

func TestMemoryUsageUsesTightestLoginLimit(t *testing.T) {
	noColor := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = noColor })

	tests := []struct {
		name         string
		sessionLimit uint64
		userLimit    uint64
		wantUser     bool
		wantRow      string
	}{
		{"tighter user limit", 4 * bytesPerGiB, 2 * bytesPerGiB, true, "2.00 GiB 0.75 GiB 1.25 GiB 38%"},
		{"tighter session limit", bytesPerGiB, 2 * bytesPerGiB, false, "1.00 GiB 0.38 GiB 0.62 GiB 38%"},
		{"equal limits include other sessions", 2 * bytesPerGiB, 2 * bytesPerGiB, true, "2.00 GiB 0.75 GiB 1.25 GiB 38%"},
		{"zero user limit", bytesPerGiB, 0, true, "0.00 GiB 0.75 GiB 0.00 GiB —"},
		{"zero session limit", 0, bytesPerGiB, false, "0.00 GiB 0.38 GiB 0.00 GiB —"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")
			sessionCgroup := filepath.Join(userCgroup, "session.scope")
			writeTestFile(t, procSelfCgroup, "0::/user.slice/user-2265.slice/session.scope\n")
			writeTestFile(t, filepath.Join(sessionCgroup, "memory.max"), strconv.FormatUint(test.sessionLimit, 10))
			writeTestFile(t, filepath.Join(userCgroup, "memory.max"), strconv.FormatUint(test.userLimit, 10))
			writeTestFile(t, filepath.Join(sessionCgroup, "memory.current"), "536870912\n")
			writeTestFile(t, filepath.Join(sessionCgroup, "memory.stat"), "inactive_file 134217728\n")
			writeTestFile(t, filepath.Join(userCgroup, "memory.current"), "1073741824\n")
			writeTestFile(t, filepath.Join(userCgroup, "memory.stat"), "inactive_file 268435456\n")
			writeTestFile(t, filepath.Join(root, "user.slice", "memory.max"), "0\n")
			// CPU and memory limits need not select the same cgroup.
			writeTestFile(t, filepath.Join(sessionCgroup, "cpu.max"), "100000 100000\n")
			writeTestFile(t, filepath.Join(userCgroup, "cpu.max"), "200000 100000\n")

			wantPath, wantLimit := sessionCgroup, test.sessionLimit
			if test.wantUser {
				wantPath, wantLimit = userCgroup, test.userLimit
			}
			context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			if context.cgroupPath != wantPath || context.allocation != memoryAllocationFinite || context.allocatedBytes != wantLimit {
				t.Fatalf("selectMemoryContext() = %+v, want %q with finite limit %d", context, wantPath, wantLimit)
			}

			var output bytes.Buffer
			err = memoryUsage(memoryUsageOptions{cgroupRoot: root, procSelfCgroup: procSelfCgroup, output: &output, readFile: os.ReadFile})
			if err != nil {
				t.Fatal(err)
			}
			if got := memoryOutputRow(t, output.String()); got != test.wantRow {
				t.Fatalf("memoryUsage() row = %q, want %q", got, test.wantRow)
			}
		})
	}
}

func TestSelectMemoryContextForPamSlurmAdoptSession(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/system.slice/slurmstepd.scope/job_42/step_extern/user/session.scope"
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	writeTestFile(t, filepath.Join(jobCgroup, "memory.max"), "4294967296\n")

	context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectMemoryContext() error = %v", err)
	}
	if context.cgroupPath != jobCgroup || context.allocation != memoryAllocationFinite || context.allocatedBytes != 4*bytesPerGiB {
		t.Fatalf("selectMemoryContext() = %+v, want adopted job cgroup %q with 4 GiB", context, jobCgroup)
	}
}

func TestSelectMemoryContextDoesNotTrustSlurmEnvironment(t *testing.T) {
	t.Setenv("SLURM_JOB_ID", "77")

	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/user.slice/user-2265.slice/session-1.scope"
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	writeTestFile(t, filepath.Join(userCgroup, "memory.max"), "2147483648\n")

	context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
	if err != nil {
		t.Fatalf("selectMemoryContext() error = %v", err)
	}
	if context.cgroupPath != userCgroup || context.allocation != memoryAllocationFinite || context.allocatedBytes != 2*bytesPerGiB {
		t.Fatalf("selectMemoryContext() = %+v, want login allocation %q despite SLURM_JOB_ID", context, userCgroup)
	}
}

func TestSelectMemoryContextMalformedSlurmMemoryLimit(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/system.slice/slurmstepd.scope/job_42/step_batch"
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	writeTestFile(t, filepath.Join(jobCgroup, "memory.max"), "invalid\n")

	_, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
	if err == nil || !strings.Contains(err.Error(), "read Slurm job memory limit") {
		t.Fatalf("selectMemoryContext() error = %v, want malformed Slurm limit error", err)
	}
}

func TestSelectMemoryContextWithoutFiniteLimitUsesCurrentCgroup(t *testing.T) {
	tests := []struct {
		name            string
		writeCurrentMax bool
		wantAllocation  memoryAllocationKind
	}{
		{name: "unlimited", writeCurrentMax: true, wantAllocation: memoryAllocationUnlimited},
		{name: "unavailable", wantAllocation: memoryAllocationUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
			currentPath := "/unlimited.slice/session.scope"
			current := filepath.Join(root, "unlimited.slice", "session.scope")
			writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
			if err := os.MkdirAll(current, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.writeCurrentMax {
				writeTestFile(t, filepath.Join(current, "memory.max"), "max\n")
			}

			context, err := selectMemoryContext(root, procSelfCgroup, os.ReadFile)
			if err != nil {
				t.Fatalf("selectMemoryContext() error = %v", err)
			}
			if context.cgroupPath != current || context.allocation != test.wantAllocation {
				t.Fatalf("selectMemoryContext() = %+v, want current cgroup %q allocation %v", context, current, test.wantAllocation)
			}
		})
	}
}

func TestMemoryUsageUsesOneLoginAllocationCgroup(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/user.slice/user-2265.slice/session-1.scope"
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session-1.scope")
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	writeTestFile(t, filepath.Join(current, "memory.max"), "max\n")
	writeTestFile(t, filepath.Join(current, "memory.current"), "9663676416\n")
	writeTestFile(t, filepath.Join(current, "memory.stat"), "inactive_file 0\n")
	writeTestFile(t, filepath.Join(userCgroup, "memory.max"), "2147483648\n")
	writeTestFile(t, filepath.Join(userCgroup, "memory.current"), "1073741824\n")
	writeTestFile(t, filepath.Join(userCgroup, "memory.stat"), "anon 805306368\ninactive_file 268435456\n")

	var output bytes.Buffer
	err := memoryUsage(memoryUsageOptions{
		cgroupRoot:     root,
		procSelfCgroup: procSelfCgroup,
		output:         &output,
		readFile:       os.ReadFile,
	})
	if err != nil {
		t.Fatalf("memoryUsage() error = %v", err)
	}
	for _, want := range []string{"2.00 GiB", "0.75 GiB", "1.25 GiB", "38%"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("memoryUsage() output %q does not contain %q", output.String(), want)
		}
	}
	if strings.Contains(output.String(), "9.00 GiB") {
		t.Fatalf("memoryUsage() output used the child cgroup instead of the allocation cgroup: %q", output.String())
	}
}

func TestMemoryUsageUsesNodeLocalSlurmJobCgroup(t *testing.T) {
	root := t.TempDir()
	procSelfCgroup := filepath.Join(t.TempDir(), "cgroup")
	currentPath := "/system.slice/slurmstepd.scope/job_42/step_batch/user/task_0"
	jobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	otherJobCgroup := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_99")

	writeTestFile(t, procSelfCgroup, "0::"+currentPath+"\n")
	if err := os.MkdirAll(filepath.Join(jobCgroup, "step_batch", "user", "task_0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(jobCgroup, "memory.max"), "4294967296\n")
	writeTestFile(t, filepath.Join(jobCgroup, "memory.current"), "3221225472\n")
	writeTestFile(t, filepath.Join(jobCgroup, "memory.stat"), "inactive_file 1073741824\n")
	writeTestFile(t, filepath.Join(otherJobCgroup, "memory.max"), "17179869184\n")
	writeTestFile(t, filepath.Join(otherJobCgroup, "memory.current"), "12884901888\n")
	writeTestFile(t, filepath.Join(otherJobCgroup, "memory.stat"), "inactive_file 0\n")

	var output bytes.Buffer
	err := memoryUsage(memoryUsageOptions{
		cgroupRoot:     root,
		procSelfCgroup: procSelfCgroup,
		output:         &output,
		readFile:       os.ReadFile,
	})
	if err != nil {
		t.Fatalf("memoryUsage() error = %v", err)
	}
	for _, want := range []string{"4.00 GiB", "2.00 GiB", "50%"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("memoryUsage() output %q does not contain %q", output.String(), want)
		}
	}
	if strings.Contains(output.String(), "16.00 GiB") || strings.Contains(output.String(), "12.00 GiB") {
		t.Fatalf("memoryUsage() output included another job instead of the current node-local job cgroup: %q", output.String())
	}
}

func TestReadMemoryMax(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		wantBytes      uint64
		wantAllocation memoryAllocationKind
		wantErr        string
	}{
		{name: "finite", content: "2147483648\n", wantBytes: 2 * bytesPerGiB, wantAllocation: memoryAllocationFinite},
		{name: "zero finite limit", content: "0\n", wantAllocation: memoryAllocationFinite},
		{name: "unlimited", content: "max\n", wantAllocation: memoryAllocationUnlimited},
		{name: "empty", wantErr: "expected 1 field"},
		{name: "multiple fields", content: "1 2\n", wantErr: "expected 1 field"},
		{name: "invalid", content: "unlimited\n", wantErr: "invalid memory.max value"},
		{name: "negative", content: "-1\n", wantErr: "invalid memory.max value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, allocation, err := readMemoryMax("/cgroup", func(string) ([]byte, error) {
				return []byte(test.content), nil
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("readMemoryMax() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readMemoryMax() error = %v", err)
			}
			if value != test.wantBytes || allocation != test.wantAllocation {
				t.Fatalf("readMemoryMax() = (%d, %v), want (%d, %v)", value, allocation, test.wantBytes, test.wantAllocation)
			}
		})
	}
}

func TestReadMemoryMaxPreservesReadError(t *testing.T) {
	want := errors.New("read failed")
	_, _, err := readMemoryMax("/cgroup", func(string) ([]byte, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("readMemoryMax() error = %v, want wrapped %v", err, want)
	}
}

func TestTightestLimitedMemoryCgroupStopsAtUserSlice(t *testing.T) {
	root := t.TempDir()
	currentPath := "/user.slice/user-2265.slice/session.scope"
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session.scope")
	userCgroup := filepath.Join(root, "user.slice", "user-2265.slice")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(userCgroup, "memory.max"), "max\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "memory.max"), "3221225472\n")

	ancestors, err := cgroupAncestors(root, currentPath)
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	path, limit, found, err := tightestLimitedMemoryCgroup(loginAllocationAncestors(ancestors), os.ReadFile)
	if err != nil {
		t.Fatalf("tightestLimitedMemoryCgroup() error = %v", err)
	}
	if path != "" || limit != 0 || found {
		t.Fatalf("tightestLimitedMemoryCgroup() = (%q, %d, %v), want no per-user finite limit", path, limit, found)
	}
}

func TestTightestLimitedMemoryCgroupPreservesBytePrecision(t *testing.T) {
	const childLimit uint64 = 1 << 53
	path, limit, found, err := tightestLimitedMemoryCgroup([]string{"/user/session", "/user"}, func(path string) ([]byte, error) {
		value := childLimit
		if path == "/user/memory.max" {
			value++
		}
		return []byte(strconv.FormatUint(value, 10)), nil
	})
	if err != nil || !found || path != "/user/session" || limit != childLimit {
		t.Fatalf("tightestLimitedMemoryCgroup() = (%q, %d, %v, %v), want exact smaller child limit", path, limit, found, err)
	}
}

func TestTightestLimitedMemoryCgroupChecksParentErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		data    string
		readErr error
	}{
		{name: "malformed parent", data: "invalid"},
		{name: "unreadable parent", readErr: os.ErrPermission},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := tightestLimitedMemoryCgroup([]string{"/user/session", "/user"}, func(path string) ([]byte, error) {
				if path == "/user/session/memory.max" {
					return []byte("4096"), nil
				}
				return []byte(test.data), test.readErr
			})
			if err == nil || !strings.Contains(err.Error(), "read memory limit for cgroup") {
				t.Fatalf("tightestLimitedMemoryCgroup() error = %v, want parent limit error", err)
			}
			if test.readErr != nil && !errors.Is(err, test.readErr) {
				t.Fatalf("error = %v, want wrapped %v", err, test.readErr)
			}
		})
	}
}

func TestReadMemoryWorkingSet(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		stat        string
		wantWorking uint64
	}{
		{name: "subtracts inactive file", current: "1000\n", stat: "anon 750\ninactive_file 250\n", wantWorking: 750},
		{name: "equal values", current: "250\n", stat: "inactive_file 250\n", wantWorking: 0},
		{name: "inactive file exceeds current", current: "100\n", stat: "inactive_file 250\n", wantWorking: 0},
		{name: "ignores unknown fields", current: "1000\n", stat: "future_counter not-a-number\ninactive_file 400\n", wantWorking: 600},
		{name: "ignores unknown field formats", current: "1000\n", stat: "future_counter has extra values\ninactive_file 400\n", wantWorking: 600},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			measurement, err := readMemoryWorkingSet("/cgroup", func(path string) ([]byte, error) {
				switch filepath.Base(path) {
				case "memory.current":
					return []byte(test.current), nil
				case "memory.stat":
					return []byte(test.stat), nil
				default:
					return nil, os.ErrNotExist
				}
			})
			if err != nil {
				t.Fatalf("readMemoryWorkingSet() error = %v", err)
			}
			if measurement.workingSetBytes != test.wantWorking {
				t.Fatalf("readMemoryWorkingSet() = %d, want %d", measurement.workingSetBytes, test.wantWorking)
			}
		})
	}
}

func TestReadMemoryWorkingSetErrors(t *testing.T) {
	readFailure := errors.New("read failed")
	tests := []struct {
		name       string
		current    string
		currentErr error
		stat       string
		statErr    error
		wantErr    string
		wantWrap   error
	}{
		{name: "missing memory.current", currentErr: readFailure, wantWrap: readFailure},
		{name: "empty memory.current", stat: "inactive_file 0\n", wantErr: "invalid memory.current"},
		{name: "malformed memory.current", current: "not-a-number\n", stat: "inactive_file 0\n", wantErr: "invalid memory.current value"},
		{name: "extra memory.current field", current: "1 2\n", stat: "inactive_file 0\n", wantErr: "invalid memory.current"},
		{name: "missing memory.stat", current: "100\n", statErr: readFailure, wantWrap: readFailure},
		{name: "malformed memory.stat line", current: "100\n", stat: "inactive_file 1 extra\n", wantErr: "invalid memory.stat line"},
		{name: "invalid inactive_file", current: "100\n", stat: "inactive_file invalid\n", wantErr: "invalid inactive_file value"},
		{name: "missing inactive_file", current: "100\n", stat: "anon 100\n", wantErr: "does not contain inactive_file"},
		{name: "duplicate inactive_file", current: "100\n", stat: "inactive_file 1\ninactive_file 2\n", wantErr: "duplicate inactive_file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readMemoryWorkingSet("/cgroup", func(path string) ([]byte, error) {
				switch filepath.Base(path) {
				case "memory.current":
					return []byte(test.current), test.currentErr
				case "memory.stat":
					return []byte(test.stat), test.statErr
				default:
					return nil, os.ErrNotExist
				}
			})
			if test.wantWrap != nil {
				if !errors.Is(err, test.wantWrap) {
					t.Fatalf("readMemoryWorkingSet() error = %v, want wrapped %v", err, test.wantWrap)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("readMemoryWorkingSet() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestPrintMemoryUsage(t *testing.T) {
	noColor := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = noColor })

	tests := []struct {
		name        string
		measurement memoryMeasurement
		context     memoryContext
		wantRow     string
	}{
		{
			name:        "finite allocation",
			measurement: memoryMeasurement{workingSetBytes: bytesPerGiB},
			context:     memoryContext{allocation: memoryAllocationFinite, allocatedBytes: 2 * bytesPerGiB},
			wantRow:     "2.00 GiB 1.00 GiB 1.00 GiB 50%",
		},
		{
			name:        "usage above finite allocation",
			measurement: memoryMeasurement{workingSetBytes: 3 * bytesPerGiB},
			context:     memoryContext{allocation: memoryAllocationFinite, allocatedBytes: 2 * bytesPerGiB},
			wantRow:     "2.00 GiB 3.00 GiB 0.00 GiB 150%",
		},
		{
			name:        "zero finite allocation",
			measurement: memoryMeasurement{},
			context:     memoryContext{allocation: memoryAllocationFinite},
			wantRow:     "0.00 GiB 0.00 GiB 0.00 GiB —",
		},
		{
			name:        "usage above zero allocation",
			measurement: memoryMeasurement{workingSetBytes: bytesPerGiB},
			context:     memoryContext{allocation: memoryAllocationFinite},
			wantRow:     "0.00 GiB 1.00 GiB 0.00 GiB —",
		},
		{
			name:        "unlimited allocation",
			measurement: memoryMeasurement{workingSetBytes: bytesPerGiB},
			context:     memoryContext{allocation: memoryAllocationUnlimited},
			wantRow:     "unlimited 1.00 GiB — —",
		},
		{
			name:        "unavailable allocation",
			measurement: memoryMeasurement{workingSetBytes: bytesPerGiB},
			context:     memoryContext{allocation: memoryAllocationUnavailable},
			wantRow:     "unavailable 1.00 GiB — —",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			printMemoryUsage(&output, test.measurement, test.context)
			if got := memoryOutputRow(t, output.String()); got != test.wantRow {
				t.Errorf("printMemoryUsage() row = %q, want %q", got, test.wantRow)
			}
		})
	}
}

func memoryOutputRow(t *testing.T, output string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 5 || strings.Join(strings.Fields(lines[2]), " ") != "Allocated Used Free Used %" {
		t.Fatalf("unexpected memory table: %q", output)
	}
	return strings.Join(strings.Fields(lines[4]), " ")
}
