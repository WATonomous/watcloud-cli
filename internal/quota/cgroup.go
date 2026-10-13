package quota

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type readFileFunc func(string) ([]byte, error)

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

func resolveCgroupPath(cgroupRoot string, cgroupPath string) (string, error) {
	root, err := filepath.Abs(cgroupRoot)
	if err != nil {
		return "", fmt.Errorf("resolve cgroup root: %w", err)
	}

	if cgroupPath == "" || !strings.HasPrefix(cgroupPath, "/") {
		return "", fmt.Errorf("invalid cgroup v2 path %q", cgroupPath)
	}
	for _, component := range strings.Split(cgroupPath, "/") {
		if component == ".." {
			return "", fmt.Errorf("invalid cgroup v2 path %q", cgroupPath)
		}
	}

	resolved := filepath.Join(root, strings.TrimPrefix(filepath.Clean(cgroupPath), string(filepath.Separator)))
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve cgroup path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cgroup path %q escapes root %q", cgroupPath, cgroupRoot)
	}

	return resolved, nil
}

func cgroupAncestors(cgroupRoot string, cgroupPath string) ([]string, error) {
	root, err := filepath.Abs(cgroupRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve cgroup root: %w", err)
	}
	current, err := resolveCgroupPath(root, cgroupPath)
	if err != nil {
		return nil, err
	}

	var ancestors []string
	for {
		ancestors = append(ancestors, current)
		if current == root {
			break
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("cgroup path %q is not below root %q", current, root)
		}
		current = parent
	}

	return ancestors, nil
}

func findAncestorCgroup(cgroupRoot string, cgroupPath string, baseName string) (string, error) {
	ancestors, err := cgroupAncestors(cgroupRoot, cgroupPath)
	if err != nil {
		return "", err
	}

	for _, ancestor := range ancestors {
		if filepath.Base(ancestor) == baseName {
			return ancestor, nil
		}
	}

	return "", fmt.Errorf("cgroup %q not found in current cgroup ancestry", baseName)
}

func readCPUMax(path string, readFile readFileFunc) (cpus float64, limited bool, err error) {
	data, err := readFile(filepath.Join(path, "cpu.max"))
	if err != nil {
		return 0, false, err
	}

	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, false, fmt.Errorf("invalid cpu.max in %q: expected 2 fields, got %d", path, len(fields))
	}
	period, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid CPU period %q in %q: %w", fields[1], path, err)
	}
	if period == 0 {
		return 0, false, fmt.Errorf("invalid zero CPU period in %q", path)
	}
	if fields[0] == "max" {
		return 0, false, nil
	}

	quota, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid CPU quota %q in %q: %w", fields[0], path, err)
	}
	if quota == 0 {
		return 0, false, fmt.Errorf("invalid zero CPU quota in %q", path)
	}

	return float64(quota) / float64(period), true, nil
}

func nearestLimitedCPUCgroup(cgroupRoot string, cgroupPath string, readFile readFileFunc) (path string, cpus float64, found bool, err error) {
	ancestors, err := cgroupAncestors(cgroupRoot, cgroupPath)
	if err != nil {
		return "", 0, false, err
	}

	for _, ancestor := range ancestors {
		cpus, limited, readErr := readCPUMax(ancestor, readFile)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return "", 0, false, fmt.Errorf("read CPU limit for cgroup %q: %w", ancestor, readErr)
		}
		if limited {
			return ancestor, cpus, true, nil
		}
	}

	return "", 0, false, nil
}
