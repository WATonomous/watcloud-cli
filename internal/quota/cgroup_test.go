package quota

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentCgroupPath(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr string
	}{
		{
			name:    "unified hierarchy",
			content: "11:memory:/legacy\n0::/user.slice/user-2265.slice/session-1.scope\n",
			want:    "/user.slice/user-2265.slice/session-1.scope",
		},
		{
			name:    "root cgroup",
			content: "0::/\n",
			want:    "/",
		},
		{
			name:    "missing v2 entry",
			content: "11:memory:/legacy\n",
			wantErr: "cgroup v2 entry not found",
		},
		{
			name:    "relative path",
			content: "0::user.slice/session.scope\n",
			wantErr: "invalid cgroup v2 path",
		},
		{
			name:    "path traversal",
			content: "0::/user.slice/../system.slice\n",
			wantErr: "invalid cgroup v2 path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readFile := func(string) ([]byte, error) {
				return []byte(test.content), nil
			}
			got, err := currentCgroupPath("/proc/self/cgroup", readFile)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("currentCgroupPath() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("currentCgroupPath() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("currentCgroupPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCurrentCgroupPathReadError(t *testing.T) {
	want := errors.New("read failed")
	_, err := currentCgroupPath("/proc/self/cgroup", func(string) ([]byte, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("currentCgroupPath() error = %v, want wrapped %v", err, want)
	}
}

func TestCgroupAncestorsRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	_, err := cgroupAncestors(root, "/user.slice/../system.slice")
	if err == nil || !strings.Contains(err.Error(), "invalid cgroup v2 path") {
		t.Fatalf("cgroupAncestors() error = %v, want path validation error", err)
	}
}

func TestCgroupAncestors(t *testing.T) {
	root := t.TempDir()
	got, err := cgroupAncestors(root, "/a/b")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	want := []string{
		filepath.Join(root, "a", "b"),
		filepath.Join(root, "a"),
		root,
	}
	if len(got) != len(want) {
		t.Fatalf("cgroupAncestors() length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cgroupAncestors()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFindSlurmJobCgroup(t *testing.T) {
	root := t.TempDir()
	ancestors, err := cgroupAncestors(root, "/system.slice/slurmstepd.scope/job_42/step_batch/user/task_0")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	got, found := findSlurmJobCgroup(ancestors)
	want := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	if got != want || !found {
		t.Fatalf("findSlurmJobCgroup() = (%q, %v), want (%q, true)", got, found, want)
	}

	for _, path := range []string{
		"/user.slice/user-2265.slice/session.scope",
		"/system.slice/slurmstepd.scope/job_not-a-number/step_batch",
	} {
		ancestors, err := cgroupAncestors(root, path)
		if err != nil {
			t.Fatalf("cgroupAncestors(%q) error = %v", path, err)
		}
		if got, found := findSlurmJobCgroup(ancestors); got != "" || found {
			t.Fatalf("findSlurmJobCgroup(%q) = (%q, %v), want not found", path, got, found)
		}
	}
}

func TestLoginAllocationAncestorsStopsAtUserSlice(t *testing.T) {
	root := t.TempDir()
	ancestors, err := cgroupAncestors(root, "/user.slice/user-2265.slice/session-1.scope")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	got := loginAllocationAncestors(ancestors)
	if len(got) != 2 || filepath.Base(got[len(got)-1]) != "user-2265.slice" {
		t.Fatalf("loginAllocationAncestors() = %v, want current session through user-2265.slice", got)
	}

	ancestors, err = cgroupAncestors(root, "/system.slice/example.service")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	got = loginAllocationAncestors(ancestors)
	if len(got) != 1 || filepath.Base(got[0]) != "example.service" {
		t.Fatalf("loginAllocationAncestors() without user slice = %v, want current cgroup only", got)
	}
}

func TestReadCPUMax(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantCPUs    float64
		wantLimited bool
		wantErr     string
	}{
		{name: "unlimited", content: "max 100000\n"},
		{name: "one CPU", content: "100000 100000\n", wantCPUs: 1, wantLimited: true},
		{name: "fractional CPU", content: "150000 100000\n", wantCPUs: 1.5, wantLimited: true},
		{name: "wrong field count", content: "100000\n", wantErr: "expected 2 fields"},
		{name: "bad quota", content: "bad 100000\n", wantErr: "invalid CPU quota"},
		{name: "bad period", content: "100000 bad\n", wantErr: "invalid CPU period"},
		{name: "bad unlimited period", content: "max bad\n", wantErr: "invalid CPU period"},
		{name: "zero quota", content: "0 100000\n", wantErr: "zero CPU quota"},
		{name: "zero period", content: "100000 0\n", wantErr: "zero CPU period"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cpus, limited, err := readCPUMax("/cgroup", func(string) ([]byte, error) {
				return []byte(test.content), nil
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("readCPUMax() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readCPUMax() error = %v", err)
			}
			if cpus != test.wantCPUs || limited != test.wantLimited {
				t.Fatalf("readCPUMax() = (%v, %v), want (%v, %v)", cpus, limited, test.wantCPUs, test.wantLimited)
			}
		})
	}
}

func TestTightestLimitedCPUCgroup(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session.scope")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(current, "cpu.max"), "400000 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "user-2265.slice", "cpu.max"), "100000 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "cpu.max"), "50000 100000\n")

	ancestors, err := cgroupAncestors(root, "/user.slice/user-2265.slice/session.scope")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	gotPath, gotCPUs, found, err := tightestLimitedCPUCgroup(loginAllocationAncestors(ancestors), os.ReadFile)
	if err != nil {
		t.Fatalf("tightestLimitedCPUCgroup() error = %v", err)
	}
	wantPath := filepath.Join(root, "user.slice", "user-2265.slice")
	if gotPath != wantPath || gotCPUs != 1 || !found {
		t.Fatalf("tightestLimitedCPUCgroup() = (%q, %v, %v), want (%q, 1, true)", gotPath, gotCPUs, found, wantPath)
	}
}

func TestTightestLimitedCPUCgroupPrefersBroaderEqualLimit(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session.scope")
	writeTestFile(t, filepath.Join(current, "cpu.max"), "100000 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "user-2265.slice", "cpu.max"), "100000 100000\n")

	ancestors, err := cgroupAncestors(root, "/user.slice/user-2265.slice/session.scope")
	if err != nil {
		t.Fatal(err)
	}
	gotPath, gotCPUs, found, err := tightestLimitedCPUCgroup(loginAllocationAncestors(ancestors), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, "user.slice", "user-2265.slice")
	if gotPath != wantPath || gotCPUs != 1 || !found {
		t.Fatalf("tightestLimitedCPUCgroup() = (%q, %v, %v), want broader equal limit at %q", gotPath, gotCPUs, found, wantPath)
	}
}

func TestTightestLimitedCPUCgroupUnlimited(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "scope")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(current, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")

	ancestors, err := cgroupAncestors(root, "/scope")
	if err != nil {
		t.Fatalf("cgroupAncestors() error = %v", err)
	}
	path, cpus, found, err := tightestLimitedCPUCgroup(ancestors, os.ReadFile)
	if err != nil {
		t.Fatalf("tightestLimitedCPUCgroup() error = %v", err)
	}
	if path != "" || cpus != 0 || found {
		t.Fatalf("tightestLimitedCPUCgroup() = (%q, %v, %v), want no limit", path, cpus, found)
	}
}

func TestReadEffectiveCPUCount(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    float64
		wantErr string
	}{
		{name: "single CPU", content: "3\n", want: 1},
		{name: "ranges", content: "0-3,8,10-11\n", want: 7},
		{name: "overlap counted once", content: "0-2,2-3\n", want: 4},
		{name: "empty", content: "\n", wantErr: "empty cpuset"},
		{name: "empty component", content: "0-1,,3\n", wantErr: "invalid CPU set"},
		{name: "bad value", content: "0-a\n", wantErr: "invalid CPU set value"},
		{name: "descending range", content: "3-1\n", wantErr: "descending"},
		{name: "too many bounds", content: "1-2-3\n", wantErr: "invalid CPU set range"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readEffectiveCPUCount("/cgroup", func(string) ([]byte, error) {
				return []byte(test.content), nil
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("readEffectiveCPUCount() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readEffectiveCPUCount() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("readEffectiveCPUCount() = %v, want %v", got, test.want)
			}
		})
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
