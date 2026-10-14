package quota

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fatih/color"
)

const bytesPerGiB = 1024 * 1024 * 1024

type memoryAllocationKind int

const (
	memoryAllocationUnavailable memoryAllocationKind = iota
	memoryAllocationUnlimited
	memoryAllocationFinite
)

type memoryContext struct {
	cgroupPath     string
	allocation     memoryAllocationKind
	allocatedBytes uint64
}

type memoryMeasurement struct {
	workingSetBytes uint64
}

type memoryUsageOptions struct {
	cgroupRoot     string
	procSelfCgroup string
	output         io.Writer
	readFile       readFileFunc
}

func MemoryUsage() error {
	return memoryUsage(memoryUsageOptions{
		cgroupRoot:     defaultCgroupRoot,
		procSelfCgroup: defaultProcSelfCgroup,
		output:         os.Stdout,
		readFile:       os.ReadFile,
	})
}

func memoryUsage(options memoryUsageOptions) error {
	context, err := selectMemoryContext(options.cgroupRoot, options.procSelfCgroup, options.readFile)
	if err != nil {
		return err
	}

	measurement, err := readMemoryWorkingSet(context.cgroupPath, options.readFile)
	if err != nil {
		return err
	}

	printMemoryUsage(options.output, measurement, context)
	return nil
}

func selectMemoryContext(cgroupRoot string, procSelfCgroup string, readFile readFileFunc) (memoryContext, error) {
	currentPath, err := currentCgroupPath(procSelfCgroup, readFile)
	if err != nil {
		return memoryContext{}, err
	}
	ancestors, err := cgroupAncestors(cgroupRoot, currentPath)
	if err != nil {
		return memoryContext{}, err
	}
	current := ancestors[0]

	if jobCgroup, found := findSlurmJobCgroup(ancestors); found {
		// A Slurm job cgroup covers this job's subtree on the current node.
		// Multi-node jobs have a separate job cgroup on each allocated node.
		context, err := memoryContextForCgroup(jobCgroup, readFile)
		if err != nil {
			return memoryContext{}, fmt.Errorf("read Slurm job memory limit: %w", err)
		}
		return context, nil
	}

	limitedPath, allocatedBytes, found, err := tightestLimitedMemoryCgroup(loginAllocationAncestors(ancestors), readFile)
	if err != nil {
		return memoryContext{}, err
	}
	if found {
		return memoryContext{
			cgroupPath:     limitedPath,
			allocation:     memoryAllocationFinite,
			allocatedBytes: allocatedBytes,
		}, nil
	}

	context, err := memoryContextForCgroup(current, readFile)
	if err != nil {
		return memoryContext{}, fmt.Errorf("read current cgroup memory limit: %w", err)
	}
	return context, nil
}

func memoryContextForCgroup(cgroupPath string, readFile readFileFunc) (memoryContext, error) {
	allocatedBytes, allocation, err := readMemoryMax(cgroupPath, readFile)
	if errors.Is(err, os.ErrNotExist) {
		return memoryContext{
			cgroupPath: cgroupPath,
			allocation: memoryAllocationUnavailable,
		}, nil
	}
	if err != nil {
		return memoryContext{}, err
	}

	return memoryContext{
		cgroupPath:     cgroupPath,
		allocation:     allocation,
		allocatedBytes: allocatedBytes,
	}, nil
}

func tightestLimitedMemoryCgroup(ancestors []string, readFile readFileFunc) (path string, bytes uint64, found bool, err error) {
	for _, ancestor := range ancestors {
		allocatedBytes, allocation, readErr := readMemoryMax(ancestor, readFile)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return "", 0, false, fmt.Errorf("read memory limit for cgroup %q: %w", ancestor, readErr)
		}
		// Equal limits share the broader cgroup's allowance and usage.
		if allocation == memoryAllocationFinite && (!found || allocatedBytes <= bytes) {
			path, bytes, found = ancestor, allocatedBytes, true
		}
	}

	return path, bytes, found, nil
}

func readMemoryMax(cgroupPath string, readFile readFileFunc) (bytes uint64, allocation memoryAllocationKind, err error) {
	data, err := readFile(filepath.Join(cgroupPath, "memory.max"))
	if err != nil {
		return 0, memoryAllocationUnavailable, err
	}

	fields := strings.Fields(string(data))
	if len(fields) != 1 {
		return 0, memoryAllocationUnavailable, fmt.Errorf("invalid memory.max in %q: expected 1 field, got %d", cgroupPath, len(fields))
	}
	if fields[0] == "max" {
		return 0, memoryAllocationUnlimited, nil
	}

	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, memoryAllocationUnavailable, fmt.Errorf("invalid memory.max value %q in %q: %w", fields[0], cgroupPath, err)
	}
	return value, memoryAllocationFinite, nil
}

func readMemoryWorkingSet(cgroupPath string, readFile readFileFunc) (memoryMeasurement, error) {
	current, err := readMemoryCurrent(cgroupPath, readFile)
	if err != nil {
		return memoryMeasurement{}, err
	}
	inactiveFile, err := readInactiveFile(cgroupPath, readFile)
	if err != nil {
		return memoryMeasurement{}, err
	}

	workingSet := uint64(0)
	if current > inactiveFile {
		workingSet = current - inactiveFile
	}
	return memoryMeasurement{workingSetBytes: workingSet}, nil
}

func readMemoryCurrent(cgroupPath string, readFile readFileFunc) (uint64, error) {
	data, err := readFile(filepath.Join(cgroupPath, "memory.current"))
	if err != nil {
		return 0, fmt.Errorf("read memory.current for cgroup %q: %w", cgroupPath, err)
	}

	fields := strings.Fields(string(data))
	if len(fields) != 1 {
		return 0, fmt.Errorf("invalid memory.current in cgroup %q: expected 1 field, got %d", cgroupPath, len(fields))
	}
	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory.current value %q in cgroup %q: %w", fields[0], cgroupPath, err)
	}
	return value, nil
}

func readInactiveFile(cgroupPath string, readFile readFileFunc) (uint64, error) {
	data, err := readFile(filepath.Join(cgroupPath, "memory.stat"))
	if err != nil {
		return 0, fmt.Errorf("read memory.stat for cgroup %q: %w", cgroupPath, err)
	}

	var inactiveFile uint64
	found := false
	for lineNumber, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] != "inactive_file" {
			continue
		}
		if len(fields) != 2 {
			return 0, fmt.Errorf("invalid memory.stat line %d in cgroup %q", lineNumber+1, cgroupPath)
		}
		if found {
			return 0, fmt.Errorf("memory.stat for cgroup %q contains duplicate inactive_file", cgroupPath)
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid inactive_file value %q in cgroup %q: %w", fields[1], cgroupPath, err)
		}
		inactiveFile = value
		found = true
	}

	if !found {
		return 0, fmt.Errorf("memory.stat for cgroup %q does not contain inactive_file", cgroupPath)
	}
	return inactiveFile, nil
}

func printMemoryUsage(output io.Writer, measurement memoryMeasurement, context memoryContext) {
	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()

	fmt.Fprintln(output, skyBlue("Memory Usage"))
	fmt.Fprintln(output, faint(strings.Repeat("─", 60)))
	fmt.Fprintf(output, "%-12s %-12s %-12s %-12s\n", "Allocated", "Used", "Free", "Used %")
	fmt.Fprintln(output, faint(strings.Repeat("-", 12)+" "+strings.Repeat("-", 12)+" "+strings.Repeat("-", 12)+" "+strings.Repeat("-", 12)))

	allocated := "unavailable"
	used := formatMemoryGiB(measurement.workingSetBytes)
	free := "—"
	percent := "—"

	switch context.allocation {
	case memoryAllocationFinite:
		allocated = formatMemoryGiB(context.allocatedBytes)
		freeBytes := uint64(0)
		if context.allocatedBytes > measurement.workingSetBytes {
			freeBytes = context.allocatedBytes - measurement.workingSetBytes
		}
		free = formatMemoryGiB(freeBytes)
		if context.allocatedBytes > 0 {
			usedPercent := float64(measurement.workingSetBytes) / float64(context.allocatedBytes) * 100
			switch {
			case usedPercent <= 60:
				percent = color.New(color.FgGreen).Sprintf("%.0f%%", usedPercent)
			case usedPercent >= 80:
				percent = color.New(color.FgRed).Sprintf("%.0f%%", usedPercent)
			default:
				percent = color.New(color.FgYellow).Sprintf("%.0f%%", usedPercent)
			}
		}
	case memoryAllocationUnlimited:
		allocated = "unlimited"
	}

	fmt.Fprintf(output, "%-12s %-12s %-12s %-12s\n", allocated, used, free, percent)
	fmt.Fprintln(output)
}

func formatMemoryGiB(bytes uint64) string {
	return fmt.Sprintf("%.2f GiB", float64(bytes)/bytesPerGiB)
}
