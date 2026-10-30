package quota

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultCgroupRoot     = "/sys/fs/cgroup"
	defaultProcSelfCgroup = "/proc/self/cgroup"
)

type readFileFunc func(string) ([]byte, error)

var (
	slurmJobCgroupName = regexp.MustCompile(`^job_[0-9]+$`)
	userSliceName      = regexp.MustCompile(`^user-[0-9]+\.slice$`)
)

type cgroupScope struct {
	current string
	job     string
	login   []string
}

func currentCgroupScope(cgroupRoot string, procSelfCgroup string, readFile readFileFunc) (cgroupScope, error) {
	ancestors, err := cgroupAncestors(cgroupRoot, procSelfCgroup, readFile)
	if err != nil {
		return cgroupScope{}, err
	}

	scope := cgroupScope{current: ancestors[0]}
	if job, found := findSlurmJobCgroup(ancestors); found {
		scope.job = job
	} else {
		scope.login = loginAllocationAncestors(ancestors)
	}
	return scope, nil
}

func currentCgroupPath(procSelfCgroup string, readFile readFileFunc) (string, error) {
	data, err := readFile(procSelfCgroup)
	if err != nil {
		return "", fmt.Errorf("read current cgroup: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[0] != "0" || parts[1] != "" {
			continue
		}

		cgroupPath := strings.TrimSpace(parts[2])
		if cgroupPath == "" || !strings.HasPrefix(cgroupPath, "/") {
			return "", fmt.Errorf("invalid cgroup v2 path %q", cgroupPath)
		}
		for _, component := range strings.Split(cgroupPath, "/") {
			if component == ".." {
				return "", fmt.Errorf("invalid cgroup v2 path %q", cgroupPath)
			}
		}

		return filepath.Clean(cgroupPath), nil
	}

	return "", errors.New("cgroup v2 entry not found in /proc/self/cgroup")
}

// Reads the path itself: the walk below never ends for a path above the root.
func cgroupAncestors(cgroupRoot string, procSelfCgroup string, readFile readFileFunc) ([]string, error) {
	cgroupPath, err := currentCgroupPath(procSelfCgroup, readFile)
	if err != nil {
		return nil, err
	}

	root, err := filepath.Abs(cgroupRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve cgroup root: %w", err)
	}

	current := filepath.Join(root, strings.TrimPrefix(cgroupPath, "/"))

	var ancestors []string
	for {
		ancestors = append(ancestors, current)
		if current == root {
			break
		}

		current = filepath.Dir(current)
	}

	return ancestors, nil
}

func findSlurmJobCgroup(ancestors []string) (string, bool) {
	for _, ancestor := range ancestors {
		if slurmJobCgroupName.MatchString(filepath.Base(ancestor)) {
			return ancestor, true
		}
	}

	return "", false
}

func loginAllocationAncestors(ancestors []string) []string {
	for i, ancestor := range ancestors {
		if userSliceName.MatchString(filepath.Base(ancestor)) {
			return ancestors[:i+1]
		}
	}

	// Without a per-user slice boundary, do not walk into shared parent
	// cgroups and accidentally report another scope's allocation or usage.
	return ancestors[:1]
}
