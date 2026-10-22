package quota

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

func tightestLimitedCPUCgroup(ancestors []string, readFile readFileFunc) (path string, cpus float64, found bool, err error) {
	for _, ancestor := range ancestors {
		candidateCPUs, limited, readErr := readCPUMax(ancestor, readFile)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return "", 0, false, fmt.Errorf("read CPU limit for cgroup %q: %w", ancestor, readErr)
		}
		// Prefer the broader cgroup when equal limits are nested. Its cpu.stat
		// includes all work sharing the same effective allocation.
		if limited && (!found || candidateCPUs <= cpus) {
			path = ancestor
			cpus = candidateCPUs
			found = true
		}
	}

	return path, cpus, found, nil
}

func readEffectiveCPUCount(path string, readFile readFileFunc) (float64, error) {
	data, err := readFile(filepath.Join(path, "cpuset.cpus.effective"))
	if err != nil {
		return 0, err
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, fmt.Errorf("empty cpuset.cpus.effective in %q", path)
	}

	seen := make(map[uint64]struct{})
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("invalid CPU set %q in %q", value, path)
		}

		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return 0, fmt.Errorf("invalid CPU set range %q in %q", part, path)
		}
		start, parseErr := strconv.ParseUint(bounds[0], 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("invalid CPU set value %q in %q: %w", bounds[0], path, parseErr)
		}
		end := start
		if len(bounds) == 2 {
			end, parseErr = strconv.ParseUint(bounds[1], 10, 64)
			if parseErr != nil {
				return 0, fmt.Errorf("invalid CPU set value %q in %q: %w", bounds[1], path, parseErr)
			}
			if end < start {
				return 0, fmt.Errorf("invalid descending CPU set range %q in %q", part, path)
			}
		}

		for cpu := start; ; cpu++ {
			seen[cpu] = struct{}{}
			if cpu == end {
				break
			}
		}
	}

	if len(seen) == 0 {
		return 0, fmt.Errorf("CPU set %q in %q contains no CPUs", value, path)
	}
	return float64(len(seen)), nil
}
