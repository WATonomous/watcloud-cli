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

func DiskUsage() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Try to get Ceph quota
	quotaBytes, usedBytes, err := getCephQuota(homeDir)

	// Default to 0 if quota can't be found
	if err != nil {
		quotaBytes = 0
		usedBytes = 0
	}

	total := float64(quotaBytes) / (1 << 30)
	used := float64(usedBytes) / (1 << 30)
	free := math.Max((total - used), 0)

	var percent float64
	if quotaBytes > 0 {
		percent = (used / total) * 100
	} else {
		percent = 100
	}

	// Try to check /tmp for size and usage
	tempSize, tempUsage, err := getTempQuota()

	// Default to 0 if quota can't be found
	if err != nil {
		tempSize = 0
		tempUsage = 0
	}

	// tempSize and tempUsage are in MiB, convert to GiB
	tempTotal := float64(tempSize) / 1024.0
	tempUsed := float64(tempUsage) / 1024.0
	tempFree := math.Max((tempTotal - tempUsed), 0)

	var tempPercent float64
	if tempTotal > 0 {
		tempPercent = (tempUsed / tempTotal) * 100
	} else {
		tempPercent = 100
	}

	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()
	fmt.Println(skyBlue("Disk Usage"))
	fmt.Println(faint(strings.Repeat("─", 60)))
	fmt.Println(skyBlue("↳ HOME") + " — $HOME")
	printUsageBlock(total, used, free, percent)

	fmt.Println(skyBlue("↳ TEMP") + " — /tmp/")
	printUsageBlock(tempTotal, tempUsed, tempFree, tempPercent)

	return nil
}

func printUsageBlock(total float64, used float64, free float64, percent float64) {
	faint := color.New(color.Faint).SprintFunc()
	fmt.Printf("%-12s %-12s %-12s %-12s\n", "Allocated", "Used", "Free", "Used %")
	fmt.Println(faint(strings.Repeat("-", 12) + " " + strings.Repeat("-", 12) + " " + strings.Repeat("-", 12) + " " + strings.Repeat("-", 12)))
	var percentStr string
	switch {
	case percent <= 70:
		percentStr = color.New(color.FgGreen).Sprintf("%.0f%%", percent)
	case percent >= 90:
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
}

func getTempQuota() (quotaBytes uint64, usedBytes uint64, err error) {
	// Check if we can get size of /tmp directory
	size, err := getTempUsage()
	if err != nil {
		return 0, 0, err //tmp doesn't exist
	}
	usedBytes = size

	// Check if we're in a SLURM job
	jobID := os.Getenv("SLURM_JOB_ID")
	if jobID == "" {
		return 0, usedBytes, nil // Not in a SLURM job, return usage but no quota
	}

	// Get disk allocation from SLURM
	cmd := exec.Command("sh", "-c", fmt.Sprintf("scontrol show job %s | grep \"AllocTRES=\"", jobID))
	output, err := cmd.Output()
	if err != nil {
		return 0, usedBytes, nil
	}

	// Parse tmpdsk from output "gres/tmpdisk="
	re := regexp.MustCompile(`gres/tmpdisk=(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) > 1 {
		diskValue, err := strconv.ParseUint(matches[1], 10, 64)
		if err == nil {
			return diskValue, usedBytes, nil
		}
	}

	return 0, usedBytes, nil // default - return usage even if quota not found
}

func getTempUsage() (uint64, error) {
	cmd := exec.Command("sh", "-c", "du -sb /tmp | awk '{print $1}'")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	usedMiB, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, err
	}

	return (usedMiB / (2 ^ 20)), nil
}

func getCephQuota(path string) (quotaBytes uint64, usedBytes uint64, err error) {
	// Get quota using getfattr
	cmd := exec.Command("getfattr", "-n", "ceph.quota", "--only-values", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	// Parse max_bytes from output "max_bytes=21474836480 max_files=0"
	fields := strings.Fields(string(output))
	for _, field := range fields {
		if strings.HasPrefix(field, "max_bytes=") {
			valueStr := strings.TrimPrefix(field, "max_bytes=")
			quotaBytes, err = strconv.ParseUint(valueStr, 10, 64)
			if err != nil {
				return 0, 0, err
			}
			break
		}
	}

	// Get usage using getfattr for ceph.dir.rbytes
	cmd = exec.Command("getfattr", "-n", "ceph.dir.rbytes", "--only-values", path)
	output, err = cmd.Output()
	if err == nil {
		usedStr := strings.TrimSpace(string(output))
		usedBytes, _ = strconv.ParseUint(usedStr, 10, 64)
	}

	return quotaBytes, usedBytes, nil
}
