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
	"github.com/shirou/gopsutil/v3/disk"
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

	// Only a job's scratch /tmp has an allocation: its XFS project quota.
	var temp *disk.UsageStat
	if mountinfo, err := os.ReadFile("/proc/self/mountinfo"); err == nil && isJobScratchTmp(string(mountinfo)) {
		if u, err := disk.Usage("/tmp"); err == nil && u.Total > 0 {
			temp = u
		}
	}

	skyBlue := func(s string) string {
		return "\x1b[1m\x1b[38;2;16;128;255m" + s + "\x1b[0m"
	}
	faint := color.New(color.Faint).SprintFunc()

	username := os.Getenv("USER")
	homeDisplay := "$HOME"
	// username found, otherwise default to $HOME
	if username != "" {
		homeDisplay = fmt.Sprintf("/home/%s", username)
	}

	fmt.Println(skyBlue("Disk Usage"))
	fmt.Println(faint(strings.Repeat("─", 60)))
	fmt.Println(skyBlue("↳ HOME") + " — " + homeDisplay)
	printUsageBlock(total, used, free, percent)

	if temp != nil {
		tempTotal := float64(temp.Total) / (1 << 30)
		tempUsed := float64(temp.Used) / (1 << 30)
		tempFree := float64(temp.Free) / (1 << 30)
		fmt.Println(skyBlue("↳ TEMP") + " — /tmp")
		printUsageBlock(tempTotal, tempUsed, tempFree, (tempUsed/tempTotal)*100)
	}

	return nil
}

// job_container/tmpfs: <BasePath>/<job id>/.<job id>
var jobScratchRoot = regexp.MustCompile(`/(\d+)/\.(\d+)(/|$)`)

// isJobScratchTmp reports whether /tmp is a SLURM job's scratch disk.
func isJobScratchTmp(mountinfo string) bool {
	found := false
	for _, line := range strings.Split(mountinfo, "\n") {
		// fields[3]: root, fields[4]: mount point
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != "/tmp" {
			continue
		}
		// The last mount on /tmp wins.
		m := jobScratchRoot.FindStringSubmatch(fields[3])
		found = m != nil && m[1] == m[2]
	}
	return found
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
