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

func TestResolveCgroupPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	_, err := resolveCgroupPath(root, "/user.slice/../system.slice")
	if err == nil || !strings.Contains(err.Error(), "invalid cgroup v2 path") {
		t.Fatalf("resolveCgroupPath() error = %v, want path validation error", err)
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

func TestFindAncestorCgroup(t *testing.T) {
	root := t.TempDir()
	got, err := findAncestorCgroup(root, "/system.slice/slurmstepd.scope/job_42/step_batch", "job_42")
	if err != nil {
		t.Fatalf("findAncestorCgroup() error = %v", err)
	}
	want := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	if got != want {
		t.Fatalf("findAncestorCgroup() = %q, want %q", got, want)
	}

	_, err = findAncestorCgroup(root, "/system.slice/slurmstepd.scope/job_42/step_batch", "job_99")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("findAncestorCgroup() missing error = %v", err)
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

func TestNearestLimitedCPUCgroup(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "user.slice", "user-2265.slice", "session.scope")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "user.slice", "user-2265.slice", "cpu.max"), "100000 100000\n")

	gotPath, gotCPUs, found, err := nearestLimitedCPUCgroup(root, "/user.slice/user-2265.slice/session.scope", os.ReadFile)
	if err != nil {
		t.Fatalf("nearestLimitedCPUCgroup() error = %v", err)
	}
	wantPath := filepath.Join(root, "user.slice", "user-2265.slice")
	if gotPath != wantPath || gotCPUs != 1 || !found {
		t.Fatalf("nearestLimitedCPUCgroup() = (%q, %v, %v), want (%q, 1, true)", gotPath, gotCPUs, found, wantPath)
	}
}

func TestNearestLimitedCPUCgroupUnlimited(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "scope")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(current, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")

	path, cpus, found, err := nearestLimitedCPUCgroup(root, "/scope", os.ReadFile)
	if err != nil {
		t.Fatalf("nearestLimitedCPUCgroup() error = %v", err)
	}
	if path != "" || cpus != 0 || found {
		t.Fatalf("nearestLimitedCPUCgroup() = (%q, %v, %v), want no limit", path, cpus, found)
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
