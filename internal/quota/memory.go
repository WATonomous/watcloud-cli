package quota

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/fatih/color"
)

func MemoryUsage() error {
	// Get actual memory usage using ps command
	usedMiB, err := getMemoryUsage()
	if err != nil {
		return err
	}

	// Get total allocated memory
	totalMiB, err := getAllocatedMemory()
	if err != nil {
		return err
	}

	total := float64(totalMiB) / 1024.0 // Convert MiB to GiB
	used := float64(usedMiB) / 1024.0
	free := math.Max(total-used, 0)

	var percent float64
	if totalMiB > 0 {
		percent = (used / total) * 100
	} else {
		percent = 100
	}

	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()
	fmt.Println(skyBlue("Memory Usage"))
	fmt.Println(faint(strings.Repeat("─", 60)))
	fmt.Printf("%-12s %-12s %-12s %-12s\n", "Allocated", "Used", "Free", "Used %")
	fmt.Println(faint(strings.Repeat("-", 12) + " " + strings.Repeat("-", 12) + " " + strings.Repeat("-", 12) + " " + strings.Repeat("-", 12)))
	var percentStr string
	switch {
	case percent <= 60:
		percentStr = color.New(color.FgGreen).Sprintf("%.0f%%", percent)
	case percent >= 80:
		percentStr = color.New(color.FgRed).Sprintf("%.0f%%", percent)
	default:
		percentStr = color.New(color.FgYellow).Sprintf("%.0f%%", percent)
	}
	fmt.Printf("%-12s %-12s %-12s %-12s\n",
		fmt.Sprintf("%.2f GiB", total),
		fmt.Sprintf("%.2f GiB", used),
		fmt.Sprintf("%.2f GiB", free),
		percentStr)
	fmt.Println()
	return nil
}

// Get memory usage in MiB by summing RSS of all user processes
func getMemoryUsage() (float64, error) {
	cmd := exec.Command("sh", "-c", "ps -u $USER -o rss= | awk '{sum+=$1} END {print sum/1024}'")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	usedMiB, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, err
	}

	return usedMiB, nil
}

// Returns the total allocated memory in MiB
func getAllocatedMemory() (float64, error) {
	// Check if we're on a login node
	hostname, err := os.Hostname()
	if err == nil && strings.Contains(hostname, "wato-login") {
		return 2048, nil // Login nodes have 2048 MiB allocation
	}

	// Check if we're in a SLURM job
	jobID := os.Getenv("SLURM_JOB_ID")
	if jobID == "" {
		return 0, nil // Not in a SLURM job, default to 0
	}

	// Get memory allocation from SLURM
	cmd := exec.Command("sh", "-c", fmt.Sprintf("scontrol show job %s | grep \"AllocTRES=\"", jobID))
	output, err := cmd.Output()
	if err != nil {
		return 0, nil
	}

	// Parse memory from output "mem=36G" or "mem=4096M"
	re := regexp.MustCompile(`(?i)mem(?:ory)?=?(\d+)([MG])`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) > 2 {
		memoryValue, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			// Convert to MiB based on unit
			if matches[2] == "G" || matches[2] == "g" {
				return memoryValue * 1024, nil // Convert GiB to MiB
			}
			return memoryValue, nil // Already in MiB
		}
	}

	return 0, nil
}
