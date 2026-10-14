package quota

import (
	"errors"
	"fmt"
	"io"
	"math"
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
	defaultCPUSampleDuration = 2 * time.Second
)

type cpuStat struct {
	usageUsec        uint64
	nrPeriods        uint64
	nrThrottled      uint64
	throttledUsec    uint64
	hasNrPeriods     bool
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
	usedCPUs                float64
	throttledDuringSample   bool
	everThrottled           bool
	hasThrottledPeriodRatio bool
	throttledPeriodsPercent float64
}

type cpuUsageOptions struct {
	cgroupRoot     string
	procSelfCgroup string
	sampleDuration time.Duration
	output         io.Writer
	readFile       readFileFunc
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
		now:            time.Now,
		sleep:          time.Sleep,
	})
}

func cpuUsage(options cpuUsageOptions) error {
	context, err := selectCPUContext(options.cgroupRoot, options.procSelfCgroup, options.readFile)
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

func selectCPUContext(cgroupRoot string, procSelfCgroup string, readFile readFileFunc) (cpuContext, error) {
	currentPath, err := currentCgroupPath(procSelfCgroup, readFile)
	if err != nil {
		return cpuContext{}, err
	}
	ancestors, err := cgroupAncestors(cgroupRoot, currentPath)
	if err != nil {
		return cpuContext{}, err
	}
	current := ancestors[0]

	if jobCgroup, found := findSlurmJobCgroup(ancestors); found {
		return selectSlurmCPUContext(jobCgroup, readFile)
	}

	limitedPath, allocatedCPUs, found, err := tightestLimitedCPUCgroup(loginAllocationAncestors(ancestors), readFile)
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

func selectSlurmCPUContext(jobCgroup string, readFile readFileFunc) (cpuContext, error) {
	context := cpuContext{cgroupPath: jobCgroup}

	if cpus, err := readEffectiveCPUCount(jobCgroup, readFile); err == nil {
		context.allocatedCPUs = cpus
		context.hasAllocation = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return cpuContext{}, fmt.Errorf("read Slurm job effective CPU set: %w", err)
	}

	if cpus, limited, err := readCPUMax(jobCgroup, readFile); err == nil {
		context.throttlingApplies = limited
		if limited && (!context.hasAllocation || cpus < context.allocatedCPUs) {
			context.allocatedCPUs = cpus
			context.hasAllocation = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cpuContext{}, fmt.Errorf("read Slurm job CPU limit: %w", err)
	}

	return context, nil
}

func readCPUStat(cgroupPath string, readFile readFileFunc) (cpuStat, error) {
	data, err := readFile(filepath.Join(cgroupPath, "cpu.stat"))
	if err != nil {
		return cpuStat{}, fmt.Errorf("read cpu.stat for cgroup %q: %w", cgroupPath, err)
	}

	values := make(map[string]uint64)
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "usage_usec", "nr_periods", "nr_throttled", "throttled_usec":
		default:
			// Ignore fields this command does not use so future kernel additions
			// do not break CPU reporting.
			continue
		}
		if len(fields) != 2 {
			return cpuStat{}, fmt.Errorf("invalid %s line %d in cpu.stat for cgroup %q", fields[0], lineNumber+1, cgroupPath)
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
	stat.nrPeriods, stat.hasNrPeriods = values["nr_periods"]
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

	if second.hasNrPeriods && second.hasNrThrottled {
		if second.nrThrottled > second.nrPeriods {
			return cpuMeasurement{}, errors.New("CPU throttling count exceeds the number of CPU periods")
		}
		measurement.hasThrottledPeriodRatio = true
		if second.nrPeriods > 0 {
			measurement.throttledPeriodsPercent = 100 * float64(second.nrThrottled) / float64(second.nrPeriods)
		}
	}
	measurement.everThrottled = (second.hasNrThrottled && second.nrThrottled > 0) ||
		(second.hasThrottledUsec && second.throttledUsec > 0)

	if first.hasNrPeriods && second.hasNrPeriods && second.nrPeriods < first.nrPeriods {
		return cpuMeasurement{}, errors.New("CPU period counter decreased during sampling")
	}
	if first.hasNrThrottled && second.hasNrThrottled {
		if second.nrThrottled < first.nrThrottled {
			return cpuMeasurement{}, errors.New("CPU throttling counter decreased during sampling")
		}
		measurement.throttledDuringSample = second.nrThrottled > first.nrThrottled
	}
	if first.hasThrottledUsec && second.hasThrottledUsec {
		if second.throttledUsec < first.throttledUsec {
			return cpuMeasurement{}, errors.New("CPU throttled time counter decreased during sampling")
		}
		measurement.throttledDuringSample = measurement.throttledDuringSample || second.throttledUsec > first.throttledUsec
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
		fmt.Fprintf(output, "Using %.1f of %s CPUs\n", measurement.usedCPUs, formatCPUCount(context.allocatedCPUs))
	} else {
		fmt.Fprintf(output, "Using %.1f CPUs (allocation unavailable)\n", measurement.usedCPUs)
	}
	if measurement.everThrottled {
		if measurement.hasThrottledPeriodRatio {
			fmt.Fprintf(output, "Throttled in %.1f%% of CPU periods for this allocation\n", measurement.throttledPeriodsPercent)
		} else {
			fmt.Fprintln(output, "Limited by the CPU cap for this allocation")
		}
	}
	if measurement.throttledDuringSample {
		fmt.Fprintln(output, "CPU throttling occurred during this sample")
	}
	fmt.Fprintln(output)
}

func formatCPUCount(cpus float64) string {
	rounded := math.Round(cpus*100) / 100
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}
