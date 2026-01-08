package quota

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/shirou/gopsutil/v3/cpu"
)

func CPUUsage() error {
	percent, err := cpu.Percent(time.Second, true)
	if err != nil || len(percent) == 0 {
		return err
	}
	info, _ := cpu.Info()
	hostname, _ := os.Hostname()
	logical, _ := cpu.Counts(true)

	// Get allocated CPUs from SLURM if in a job
	allocatedCPUs, err := getAllocatedCPUs()
	if err != nil {
		return err
	}

	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()
	fmt.Println(skyBlue("CPU Usage"))
	fmt.Println(faint(strings.Repeat("─", 60)))
	if hostname != "" {
		fmt.Printf("System: %s\n", hostname)
	}
	if len(info) > 0 {
		fmt.Printf("Model: %s\n", info[0].ModelName)
	}

	// Display cores with allocated count highlighted
	allocatedStr := color.New(color.FgCyan, color.Bold).Sprintf("%d allocated", allocatedCPUs)
	fmt.Printf("Cores: %s / %d logical\n", allocatedStr, logical)

	fmt.Println(faint(strings.Repeat("-", 60)))
	// Print per-core usage
	fmt.Printf("%-8s %-8s\n", "Core", "Usage %")
	fmt.Println(faint(strings.Repeat("-", 8) + " " + strings.Repeat("-", 8)))
	for i, p := range percent {
		var percentStr string
		switch {
		case p < 50:
			percentStr = color.New(color.FgGreen).Sprintf("%.0f%%", p)
		case p <= 70:
			percentStr = color.New(color.FgYellow).Sprintf("%.0f%%", p)
		default:
			percentStr = color.New(color.FgRed).Sprintf("%.0f%%", p)
		}
		fmt.Printf("%-8d %-8s\n", i, percentStr)
	}
	fmt.Println()
	return nil
}

// Total allocated CPUs
func getAllocatedCPUs() (int, error) {
	// Check if we're on a login node
	hostname, err := os.Hostname()
	if err == nil && strings.Contains(hostname, "wato-login") {
		return 1, nil // Login nodes have 1 core quota
	}

	// Check if we're in a SLURM job
	jobID := os.Getenv("SLURM_JOB_ID")
	if jobID == "" {
		return 0, nil // Not in a SLURM job, default to 0
	}

	// Get CPU allocation from SLURM
	cmd := exec.Command("sh", "-c", fmt.Sprintf("scontrol show job %s | grep \"AllocTRES=\"", jobID))
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// Parse CPU from output "cpu=1"
	re := regexp.MustCompile(`\bcpu=(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) > 1 {
		cpuCount, err := strconv.Atoi(matches[1])
		if err == nil {
			return cpuCount, nil
		}
	}

	return 0, nil
}
