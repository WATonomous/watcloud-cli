package slurm

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Node is what a compute node can give a single job.
type Node struct {
	Name       string
	CPUs       int
	MemMiB     int64
	TmpDiskMiB int64
	// GPUs and Shards are keyed by GPU type. Shards are MiB of GPU memory.
	GPUs   map[string]int
	Shards map[string]int64
}

type Partition struct {
	Name    string
	Default bool
	// 0 when unlimited or unknown.
	DefaultMinutes int
	MaxMinutes     int
}

type Cluster struct {
	Nodes      []Node
	Partitions []Partition
}

// LoadCluster reads node and partition sizes from sinfo.
func LoadCluster() (*Cluster, error) {
	nodes, err := exec.Command("sinfo", "--noheader", "--Node", "--format", "%N|%c|%m|%G").Output()
	if err != nil {
		return nil, fmt.Errorf("sinfo: %w", err)
	}
	parts, err := exec.Command("sinfo", "--noheader", "--format", "%P|%L|%l").Output()
	if err != nil {
		return nil, fmt.Errorf("sinfo: %w", err)
	}
	return &Cluster{Nodes: parseNodes(string(nodes)), Partitions: parsePartitions(string(parts))}, nil
}

// parseNodes reads `sinfo -N -o "%N|%c|%m|%G"`. A node appears once per
// partition, so duplicates are dropped.
func parseNodes(out string) []Node {
	var nodes []Node
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 4 || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		n := Node{Name: f[0], GPUs: map[string]int{}, Shards: map[string]int64{}}
		n.CPUs, _ = strconv.Atoi(f[1])
		n.MemMiB, _ = strconv.ParseInt(strings.TrimSuffix(f[2], "+"), 10, 64)
		parseGres(f[3], &n)
		nodes = append(nodes, n)
	}
	return nodes
}

// Socket lists like "(S:0-5)" may contain commas, so they go before splitting.
var gresSockets = regexp.MustCompile(`\([^)]*\)`)

// parseGres reads e.g. "gpu:rtx_3090:2(S:0-59),shard:rtx_3090:48K(S:0-59),tmpdisk:968K".
func parseGres(s string, n *Node) {
	for _, item := range strings.Split(gresSockets.ReplaceAllString(s, ""), ",") {
		f := strings.Split(item, ":")
		if len(f) < 2 {
			continue
		}
		count := gresCount(f[len(f)-1])
		gpuType := ""
		if len(f) == 3 {
			gpuType = f[1]
		}
		switch f[0] {
		case "gpu":
			n.GPUs[gpuType] += int(count)
		case "shard":
			n.Shards[gpuType] += count
		case "tmpdisk":
			n.TmpDiskMiB += count
		}
	}
}

// gresCount reads a GRES count, where Slurm abbreviates with binary suffixes.
func gresCount(s string) int64 {
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K"):
		mult = 1 << 10
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
	}
	n, _ := strconv.ParseInt(strings.TrimRight(s, "KMG"), 10, 64)
	return n * mult
}

// parsePartitions reads `sinfo -o "%P|%L|%l"`. The default partition is
// marked with a trailing "*".
func parsePartitions(out string) []Partition {
	var parts []Partition
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 3 {
			continue
		}
		p := Partition{Name: strings.TrimSuffix(f[0], "*"), Default: strings.HasSuffix(f[0], "*")}
		p.DefaultMinutes, _ = parseSlurmTime(f[1])
		p.MaxMinutes, _ = parseSlurmTime(f[2])
		parts = append(parts, p)
	}
	return parts
}

func (c *Cluster) partition(name string) (Partition, bool) {
	for _, p := range c.Partitions {
		if p.Name == name || (name == "" && p.Default) {
			return p, true
		}
	}
	return Partition{}, false
}

// check catches requests the cluster can never run, which would otherwise
// sit in the queue forever.
func (c *Cluster) check(r *Request) ([]Notice, error) {
	var notices []Notice

	p, ok := c.partition(r.Partition)
	if !ok && r.Partition != "" {
		var names []string
		for _, p := range c.Partitions {
			names = append(names, p.Name)
		}
		return nil, fmt.Errorf("no partition %q; available: %s", r.Partition, strings.Join(names, ", "))
	}
	if ok {
		if r.Minutes > 0 && p.MaxMinutes > 0 && r.Minutes > p.MaxMinutes {
			return nil, fmt.Errorf("the longest job on %s is %s", p.Name, humanTime(p.MaxMinutes))
		}
		if r.Minutes == 0 && p.DefaultMinutes > 0 {
			notices = append(notices, note("No --time given: the job ends after %s, the %s default.", humanTime(p.DefaultMinutes), p.Name))
		}
	}

	if r.GPUType != "" && !c.hasGPUType(r.GPUType) {
		return nil, fmt.Errorf("no GPU type %q; available: %s", r.GPUType, strings.Join(c.gpuTypes(), ", "))
	}

	if len(c.Nodes) == 0 {
		return notices, nil
	}
	for _, n := range c.Nodes {
		if n.fits(r) {
			return notices, nil
		}
	}
	return nil, c.whyNoFit(r)
}

func (n Node) fitsCPUs(r *Request) bool { return r.CPUs <= n.CPUs }
func (n Node) fitsMem(r *Request) bool  { return r.MemMiB <= n.MemMiB }
func (n Node) fitsDisk(r *Request) bool { return r.DiskMiB <= n.TmpDiskMiB }

func (n Node) fitsGPUs(r *Request) bool {
	total := 0
	for t, count := range n.GPUs {
		if r.GPUType == "" || t == r.GPUType {
			total += count
		}
	}
	return r.GPUs <= total
}

// A job's shards on a node all come from one GPU (SelectTypeParameters has no
// MULTIPLE_SHARING_GRES_PJ), so --gpu-mem has to fit on a single card.
func (n Node) fitsGPUMem(r *Request) bool { return r.GPUMemMiB <= n.maxGPUMem(r.GPUType) }

func (n Node) maxGPUMem(gpuType string) int64 {
	var best int64
	for t, shards := range n.Shards {
		if gpuType != "" && t != gpuType {
			continue
		}
		if gpus := n.GPUs[t]; gpus > 0 && shards/int64(gpus) > best {
			best = shards / int64(gpus)
		}
	}
	return best
}

func (n Node) fits(r *Request) bool {
	return n.fitsCPUs(r) && n.fitsMem(r) && n.fitsDisk(r) && n.fitsGPUs(r) && n.fitsGPUMem(r)
}

// whyNoFit names the resources no node has enough of. If each fits somewhere
// but never all on one node, it says that instead.
func (c *Cluster) whyNoFit(r *Request) error {
	var maxCPUs, maxGPUs int
	var maxMem, maxDisk, maxGPUMem int64
	for _, n := range c.Nodes {
		maxCPUs = max(maxCPUs, n.CPUs)
		maxMem = max(maxMem, n.MemMiB)
		maxDisk = max(maxDisk, n.TmpDiskMiB)
		maxGPUMem = max(maxGPUMem, n.maxGPUMem(r.GPUType))
		gpus := 0
		for t, count := range n.GPUs {
			if r.GPUType == "" || t == r.GPUType {
				gpus += count
			}
		}
		maxGPUs = max(maxGPUs, gpus)
	}

	onType := ""
	if r.GPUType != "" {
		onType = " " + r.GPUType
	}
	var reasons []string
	if r.CPUs > maxCPUs {
		reasons = append(reasons, fmt.Sprintf("the most CPUs on a node is %d", maxCPUs))
	}
	if r.MemMiB > maxMem {
		reasons = append(reasons, fmt.Sprintf("the most memory on a node is %s", humanSize(maxMem)))
	}
	if r.DiskMiB > maxDisk {
		reasons = append(reasons, fmt.Sprintf("the most scratch disk on a node is %s", humanSize(maxDisk)))
	}
	if r.GPUs > maxGPUs {
		reasons = append(reasons, fmt.Sprintf("the most%s GPUs on a node is %d", onType, maxGPUs))
	}
	if r.GPUMemMiB > maxGPUMem {
		reasons = append(reasons, fmt.Sprintf("--gpu-mem must fit on one%s GPU, and the largest has %s", onType, humanSize(maxGPUMem)))
	}
	if len(reasons) == 0 {
		return fmt.Errorf("no single node has all of this at once; see `watcloud slurm capacity` for what each node has")
	}
	return fmt.Errorf("no node can ever run this job: %s", strings.Join(reasons, "; "))
}

func (c *Cluster) hasGPUType(t string) bool {
	for _, n := range c.Nodes {
		if _, ok := n.GPUs[t]; ok {
			return true
		}
	}
	return false
}

func (c *Cluster) gpuTypes() []string {
	set := map[string]bool{}
	for _, n := range c.Nodes {
		for t := range n.GPUs {
			if t != "" {
				set[t] = true
			}
		}
	}
	types := make([]string, 0, len(set))
	for t := range set {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}
