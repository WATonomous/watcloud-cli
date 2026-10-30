package quota

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fakeProcSelfCgroup(cgroupPath string) readFileFunc {
	return func(string) ([]byte, error) {
		return []byte("0::" + cgroupPath + "\n"), nil
	}
}

func testCgroupAncestors(t *testing.T, root string, cgroupPath string) []string {
	t.Helper()
	ancestors, err := cgroupAncestors(root, defaultProcSelfCgroup, fakeProcSelfCgroup(cgroupPath))
	if err != nil {
		t.Fatalf("cgroupAncestors(%q) error = %v", cgroupPath, err)
	}
	return ancestors
}

func underRoot(root string, paths ...string) []string {
	joined := make([]string, len(paths))
	for i, path := range paths {
		joined[i] = filepath.Join(root, filepath.FromSlash(path))
	}
	return joined
}

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

	const procSelfCgroup = "/test/proc/self/cgroup"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readFile := func(string) ([]byte, error) {
				return []byte(test.content), nil
			}
			got, err := currentCgroupPath(procSelfCgroup, readFile)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), procSelfCgroup) {
					t.Fatalf("currentCgroupPath() error = %v, want containing %q and %s", err, test.wantErr, procSelfCgroup)
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
	tests := []struct {
		path string
		want []string
	}{
		{path: "/a/b", want: []string{"a/b", "a", ""}},
		{path: "/", want: []string{""}},
	}

	for _, test := range tests {
		got := testCgroupAncestors(t, root, test.path)
		if want := underRoot(root, test.want...); !slices.Equal(got, want) {
			t.Errorf("cgroupAncestors(%q) = %v, want %v", test.path, got, want)
		}
	}
}

func TestCgroupAncestorsRejectsInvalidPath(t *testing.T) {
	_, err := cgroupAncestors(t.TempDir(), defaultProcSelfCgroup, fakeProcSelfCgroup("/../x"))
	if err == nil || !strings.Contains(err.Error(), "invalid cgroup v2 path") {
		t.Fatalf("cgroupAncestors() error = %v, want invalid cgroup v2 path", err)
	}
}

func TestFindSlurmJobCgroup(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_42")
	tests := []struct {
		path string
		want string
	}{
		{path: "/system.slice/slurmstepd.scope/job_42/step_batch/user/task_0", want: job},
		{path: "/system.slice/slurmstepd.scope/job_42/step_0/user/task_0", want: job},
		{path: "/system.slice/slurmstepd.scope/job_42/step_extern/user/task_special", want: job},
		{path: "/system.slice/slurmstepd.scope/job_42", want: job},
		{path: "/system.slice/slurmstepd.scope/job_not-a-number/step_batch"},
		{path: "/system.slice/slurmstepd.scope"},
		{path: "/user.slice/user-2265.slice/session-1.scope"},
		{path: "/user.slice/user-2265.slice/user@2265.service/app.slice/job_1"},
		{path: "/user.slice/user-2265.slice/user@2265.service/slurmstepd.scope/job_1"},
		{path: "/user.slice/user-2265.slice/user@2265.service/system.slice/slurmstepd.scope/job_1"},
		{path: "/"},
	}

	for _, test := range tests {
		got, found := findSlurmJobCgroup(testCgroupAncestors(t, root, test.path))
		if got != test.want || found != (test.want != "") {
			t.Errorf("findSlurmJobCgroup(%q) = (%q, %v), want (%q, %v)", test.path, got, found, test.want, test.want != "")
		}
	}
}

func TestLoginAllocationAncestorsStopsAtUserSlice(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "login session",
			path: "/user.slice/user-2265.slice/session-1.scope",
			want: []string{"user.slice/user-2265.slice/session-1.scope", "user.slice/user-2265.slice"},
		},
		{
			name: "systemd user manager",
			path: "/user.slice/user-2265.slice/user@2265.service/app.slice/tmux.scope",
			want: []string{
				"user.slice/user-2265.slice/user@2265.service/app.slice/tmux.scope",
				"user.slice/user-2265.slice/user@2265.service/app.slice",
				"user.slice/user-2265.slice/user@2265.service",
				"user.slice/user-2265.slice",
			},
		},
		{
			name: "user slice itself",
			path: "/user.slice/user-2265.slice",
			want: []string{"user.slice/user-2265.slice"},
		},
		{
			name: "no user slice",
			path: "/system.slice/example.service",
			want: []string{"system.slice/example.service"},
		},
		{
			name: "root cgroup",
			path: "/",
			want: []string{""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := loginAllocationAncestors(testCgroupAncestors(t, root, test.path))
			if want := underRoot(root, test.want...); !slices.Equal(got, want) {
				t.Fatalf("loginAllocationAncestors() = %v, want %v", got, want)
			}
		})
	}
}

func TestCurrentCgroupScope(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name      string
		path      string
		wantJob   string
		wantLogin []string
	}{
		{
			name:    "Slurm job step",
			path:    "/system.slice/slurmstepd.scope/job_42/step_0/user/task_0",
			wantJob: "system.slice/slurmstepd.scope/job_42",
		},
		{
			name:      "login session",
			path:      "/user.slice/user-2265.slice/session-1.scope",
			wantLogin: []string{"user.slice/user-2265.slice/session-1.scope", "user.slice/user-2265.slice"},
		},
		{
			name: "lookalike job in delegated user subtree",
			path: "/user.slice/user-2265.slice/user@2265.service/slurmstepd.scope/job_1",
			wantLogin: []string{
				"user.slice/user-2265.slice/user@2265.service/slurmstepd.scope/job_1",
				"user.slice/user-2265.slice/user@2265.service/slurmstepd.scope",
				"user.slice/user-2265.slice/user@2265.service",
				"user.slice/user-2265.slice",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, err := currentCgroupScope(root, defaultProcSelfCgroup, fakeProcSelfCgroup(test.path))
			if err != nil {
				t.Fatalf("currentCgroupScope() error = %v", err)
			}
			if want := underRoot(root, test.path)[0]; scope.current != want {
				t.Errorf("current = %q, want %q", scope.current, want)
			}
			wantJob := ""
			if test.wantJob != "" {
				wantJob = underRoot(root, test.wantJob)[0]
			}
			if scope.job != wantJob {
				t.Errorf("job = %q, want %q", scope.job, wantJob)
			}
			if want := underRoot(root, test.wantLogin...); !slices.Equal(scope.login, want) {
				t.Errorf("login = %v, want %v", scope.login, want)
			}
		})
	}

	if _, err := currentCgroupScope(root, defaultProcSelfCgroup, fakeProcSelfCgroup("/../x")); err == nil {
		t.Fatal("currentCgroupScope() with a path above the root returned no error")
	}
}
