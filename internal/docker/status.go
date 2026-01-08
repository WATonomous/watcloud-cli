package docker

import (
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
)

// Checks if a path exists, is a valid Unix socket, and accepts connections
func isSocket(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	// check if it's a socket file
	if info.Mode()&os.ModeSocket == 0 {
		return false
	}

	// dial the socket
	conn, err := net.DialTimeout("unix", path, 10*time.Millisecond)
	if err != nil {
		return false
	}

	conn.Close()
	return true
}

// probe represents one daemon we test.
type probe struct {
	name   string
	socket func() string // returns discovered socket or ""
}

// only Docker and Slurm remain.
var allProbes = []probe{
	// {
	// 	name: "Docker (rootful)",
	// 	socket: func() string {
	// 		if isSocket("/var/run/docker.sock") {
	// 			return "/var/run/docker.sock"
	// 		}
	// 		return ""
	// 	},
	// },
	{
		name: "Docker (rootless)",
		socket: func() string {
			if isSocket("/tmp/run/docker.sock") {
				return "/tmp/run/docker.sock"
			}
			return ""
		},
	},
	{
		name: "Slurm",
		socket: func() string {
			paths := []string{
				"/var/run/slurmd.pid",
				"/run/slurmd.pid",
			}
			for _, p := range paths {
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
			return ""
		},
	},
}

// ListDaemons returns (formattedReport, exitCode).
func ListDaemons() (string, int) {
	var b strings.Builder
	header := color.New(color.Bold, color.FgHiCyan).Sprint("Daemon Status")
	divider := color.New(color.Faint).Sprint(strings.Repeat("─", 60))
	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b, divider)

	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	ok := color.New(color.FgGreen).SprintFunc()
	bad := color.New(color.FgRed).SprintFunc()

	exitCode := 0
	for _, p := range allProbes {
		if sock := p.socket(); sock != "" {
			fmt.Fprintf(w, "%s:\t%s\t(%s)\n", p.name, ok("running"), sock)
		} else {
			fmt.Fprintf(w, "%s:\t%s\t(not detected)\n", p.name, bad("stopped"))
			exitCode = 1
		}
	}
	w.Flush()
	return b.String(), exitCode
}
