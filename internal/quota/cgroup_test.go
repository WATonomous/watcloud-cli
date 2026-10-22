package quota

import (
	"errors"
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
