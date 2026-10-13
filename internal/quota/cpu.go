package quota

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
)

const (
	defaultCgroupRoot        = "/sys/fs/cgroup"
	defaultProcSelfCgroup    = "/proc/self/cgroup"
	defaultCPUSampleDuration = time.Second
)

type cpuStat struct {
	usageUsec        uint64
	nrThrottled      uint64
	throttledUsec    uint64
	hasNrThrottled   bool
	hasThrottledUsec bool
}

type cpuContext struct {
	cgroupPath        string
	allocatedCPUs     float64
	hasAllocation     bool
	throttlingApplies bool
}

type cpuMeasurement struct {
	usedCPUs  float64
	throttled bool
}

type cpuUsageOptions struct {
	cgroupRoot     string
	procSelfCgroup string
	sampleDuration time.Duration
	output         io.Writer
	readFile       readFileFunc
	getenv         func(string) string
	now            func() time.Time
	sleep          func(time.Duration)
}

func CPUUsage() error {
	return cpuUsage(cpuUsageOptions{
		cgroupRoot:     defaultCgroupRoot,
		procSelfCgroup: defaultProcSelfCgroup,
		sampleDuration: defaultCPUSampleDuration,
		output:         os.Stdout,
		readFile:       os.ReadFile,
		getenv:         os.Getenv,
		now:            time.Now,
		sleep:          time.Sleep,
	})
}

func cpuUsage(options cpuUsageOptions) error {
	context, err := selectCPUContext(options.cgroupRoot, options.procSelfCgroup, options.readFile, options.getenv)
	if err != nil {
		return err
	}

	measurement, err := measureCPUUsage(
		func() (cpuStat, error) {
			return readCPUStat(context.cgroupPath, options.readFile)
		},
		options.sampleDuration,
		options.now,
		options.sleep,
		context.throttlingApplies,
	)
	if err != nil {
		return err
	}

	printCPUUsage(options.output, measurement, context)
	return nil
}

func selectCPUContext(cgroupRoot string, procSelfCgroup string, readFile readFileFunc, getenv func(string) string) (cpuContext, error) {
	currentPath, err := currentCgroupPath(procSelfCgroup, readFile)
	if err != nil {
		return cpuContext{}, err
	}
	current, err := resolveCgroupPath(cgroupRoot, currentPath)
	if err != nil {
		return cpuContext{}, err
	}

	jobID := strings.TrimSpace(getenv("SLURM_JOB_ID"))
	if jobID != "" {
		jobCgroup, err := findAncestorCgroup(cgroupRoot, currentPath, "job_"+jobID)
		if err != nil {
			return cpuContext{}, fmt.Errorf("locate Slurm job cgroup: %w", err)
		}

		allocatedCPUs, hasAllocation, err := parseSlurmCPUAllocation(getenv("SLURM_CPUS_ON_NODE"))
		if err != nil {
			return cpuContext{}, err
		}

		_, limited, err := readCPUMax(jobCgroup, readFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return cpuContext{}, fmt.Errorf("read Slurm job CPU limit: %w", err)
		}

		return cpuContext{
			cgroupPath:        jobCgroup,
			allocatedCPUs:     allocatedCPUs,
			hasAllocation:     hasAllocation,
			throttlingApplies: limited,
		}, nil
	}

	limitedPath, allocatedCPUs, found, err := nearestLimitedCPUCgroup(cgroupRoot, currentPath, readFile)
	if err != nil {
		return cpuContext{}, err
	}
	if found {
		return cpuContext{
			cgroupPath:        limitedPath,
			allocatedCPUs:     allocatedCPUs,
			hasAllocation:     true,
			throttlingApplies: true,
		}, nil
	}

	return cpuContext{cgroupPath: current}, nil
}

func parseSlurmCPUAllocation(value string) (cpus float64, found bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}

	allocated, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid SLURM_CPUS_ON_NODE value %q: %w", value, err)
	}
	if allocated == 0 {
		return 0, false, errors.New("SLURM_CPUS_ON_NODE must be greater than zero")
	}

	return float64(allocated), true, nil
}

func readCPUStat(cgroupPath string, readFile readFileFunc) (cpuStat, error) {
	data, err := readFile(filepath.Join(cgroupPath, "cpu.stat"))
	if err != nil {
		return cpuStat{}, fmt.Errorf("read cpu.stat for cgroup %q: %w", cgroupPath, err)
	}

	values := make(map[string]uint64)
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return cpuStat{}, fmt.Errorf("invalid cpu.stat line %d in cgroup %q", lineNumber+1, cgroupPath)
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return cpuStat{}, fmt.Errorf("invalid %s value %q in cgroup %q: %w", fields[0], fields[1], cgroupPath, err)
		}
		values[fields[0]] = value
	}

	usageUsec, ok := values["usage_usec"]
	if !ok {
		return cpuStat{}, fmt.Errorf("cpu.stat for cgroup %q does not contain usage_usec", cgroupPath)
	}

	stat := cpuStat{usageUsec: usageUsec}
	stat.nrThrottled, stat.hasNrThrottled = values["nr_throttled"]
	stat.throttledUsec, stat.hasThrottledUsec = values["throttled_usec"]
	return stat, nil
}

func measureCPUUsage(readStat func() (cpuStat, error), sampleDuration time.Duration, now func() time.Time, sleep func(time.Duration), throttlingApplies bool) (cpuMeasurement, error) {
	firstReadStarted := now()
	first, err := readStat()
	if err != nil {
		return cpuMeasurement{}, err
	}
	firstReadFinished := now()

	sleep(sampleDuration)

	secondReadStarted := now()
	second, err := readStat()
	if err != nil {
		return cpuMeasurement{}, err
	}
	secondReadFinished := now()

	// Approximate when each cpu.stat snapshot was taken by using the midpoint
	// of its read, then compare those snapshot times.
	firstSnapshot := firstReadStarted.Add(firstReadFinished.Sub(firstReadStarted) / 2)
	secondSnapshot := secondReadStarted.Add(secondReadFinished.Sub(secondReadStarted) / 2)
	elapsed := secondSnapshot.Sub(firstSnapshot)
	if elapsed <= 0 {
		return cpuMeasurement{}, fmt.Errorf("CPU sample elapsed time must be positive, got %s", elapsed)
	}
	if second.usageUsec < first.usageUsec {
		return cpuMeasurement{}, errors.New("CPU usage counter decreased during sampling")
	}

	elapsedUsec := float64(elapsed) / float64(time.Microsecond)
	measurement := cpuMeasurement{
		usedCPUs: float64(second.usageUsec-first.usageUsec) / elapsedUsec,
	}
	if !throttlingApplies {
		return measurement, nil
	}

	if first.hasNrThrottled && second.hasNrThrottled {
		if second.nrThrottled < first.nrThrottled {
			return cpuMeasurement{}, errors.New("CPU throttling counter decreased during sampling")
		}
		measurement.throttled = second.nrThrottled > first.nrThrottled
	}
	if first.hasThrottledUsec && second.hasThrottledUsec {
		if second.throttledUsec < first.throttledUsec {
			return cpuMeasurement{}, errors.New("CPU throttled time counter decreased during sampling")
		}
		measurement.throttled = measurement.throttled || second.throttledUsec > first.throttledUsec
	}

	return measurement, nil
}

func printCPUUsage(output io.Writer, measurement cpuMeasurement, context cpuContext) {
	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()

	fmt.Fprintln(output, skyBlue("CPU Usage"))
	fmt.Fprintln(output, faint(strings.Repeat("─", 60)))
	if context.hasAllocation {
		fmt.Fprintf(output, "Using %.2f of %s CPUs\n", measurement.usedCPUs, formatCPUCount(context.allocatedCPUs))
	} else {
		fmt.Fprintf(output, "Using %.2f CPUs (allocation unavailable)\n", measurement.usedCPUs)
	}
	if measurement.throttled {
		fmt.Fprintln(output, "Limited by the CPU cap during this sample")
	}
	fmt.Fprintln(output)
}

func formatCPUCount(cpus float64) string {
	return strconv.FormatFloat(cpus, 'f', -1, 64)
}
