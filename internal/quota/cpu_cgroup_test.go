package quota

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
