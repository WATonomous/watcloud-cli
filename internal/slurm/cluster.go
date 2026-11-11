package slurm

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Node is what a compute node can give a single job.
type Node struct {
	Name string
	// CPUs and MemMiB leave out what Slurm keeps for the node itself
	// (CoreSpecCount, MemSpecLimit).
	CPUs       int
	MemMiB     int64
	TmpDiskMiB int64
	// GPUs and Shards are keyed by GPU type. Shards are MiB of GPU memory.
	GPUs       map[string]int
	Shards     map[string]int64
	Partitions []string
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
	// DefMemPerCPU is the memory a job gets per CPU without --mem, in MiB.
	DefMemPerCPU int64
}

// slurmTimeout bounds how long LoadCluster waits for Slurm. When the
// controller is down, its commands otherwise keep retrying for about 30 s.
var slurmTimeout = 5 * time.Second

// runSlurm runs a Slurm command. Tests replace it.
var runSlurm = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// LoadCluster reads node and partition sizes from Slurm.
func LoadCluster() (*Cluster, error) {
	ctx, cancel := context.WithTimeout(context.Background(), slurmTimeout)
	defer cancel()

	parts, err := runSlurm(ctx, "sinfo", "--noheader", "--format", "%P|%L|%l")
	if err != nil {
		return nil, slurmError(ctx, "sinfo", err)
	}
	nodes, err := runSlurm(ctx, "scontrol", "--oneliner", "show", "node")
	if err != nil {
		return nil, slurmError(ctx, "scontrol", err)
	}
	config, err := runSlurm(ctx, "scontrol", "show", "config")
	if err != nil {
		return nil, slurmError(ctx, "scontrol", err)
	}

	c := &Cluster{Partitions: parsePartitions(string(parts)), DefMemPerCPU: parseDefMemPerCPU(string(config))}
	c.Nodes = visibleNodes(parseNodes(string(nodes)), c.Partitions)
	if len(c.Nodes) == 0 {
		return nil, errors.New("scontrol listed no compute nodes")
	}
	return c, nil
}

// visibleNodes keeps the nodes in a partition users can see (sinfo lists
// only those). Login nodes run slurmd too, in a hidden partition.
func visibleNodes(nodes []Node, parts []Partition) []Node {
	var visible []Node
	for _, n := range nodes {
		for _, p := range parts {
			if slices.Contains(n.Partitions, p.Name) {
				visible = append(visible, n)
				break
			}
		}
	}
	return visible
}

// slurmError says why a Slurm command failed, in words a user can act on.
func slurmError(ctx context.Context, name string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %s", name, slurmTimeout)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s not found", name)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg, _, _ := strings.Cut(strings.TrimSpace(string(exitErr.Stderr)), "\n"); msg != "" {
			return fmt.Errorf("%s failed: %s", name, msg)
		}
	}
	return fmt.Errorf("%s failed: %w", name, err)
}

// nodeField reads Key=value fields of `scontrol --oneliner show node`. Values
// like OS= contain spaces, but none of the ones read here do.
func nodeField(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// parseNodes reads `scontrol --oneliner show node`, one node per line.
func parseNodes(out string) []Node {
	var nodes []Node
	for _, line := range strings.Split(out, "\n") {
		name := nodeField(line, "NodeName")
		if name == "" {
			continue
		}
		n := Node{Name: name, GPUs: map[string]int{}, Shards: map[string]int64{}}
		// CPUEfctv already leaves out the CPUs kept for the node itself.
		n.CPUs, _ = strconv.Atoi(nodeField(line, "CPUEfctv"))
		if n.CPUs == 0 {
			n.CPUs, _ = strconv.Atoi(nodeField(line, "CPUTot"))
		}
		total, _ := strconv.ParseInt(nodeField(line, "RealMemory"), 10, 64)
		kept, _ := strconv.ParseInt(nodeField(line, "MemSpecLimit"), 10, 64)
		n.MemMiB = total - kept
		parseGres(nodeField(line, "Gres"), &n)
		if p := nodeField(line, "Partitions"); p != "" {
			n.Partitions = strings.Split(p, ",")
		}
		nodes = append(nodes, n)
	}
	return nodes
}

var defMemPerCPU = regexp.MustCompile(`(?m)^DefMemPerCPU\s*=\s*(\d+)`)

// parseDefMemPerCPU reads DefMemPerCPU from `scontrol show config`.
func parseDefMemPerCPU(out string) int64 {
	m := defMemPerCPU.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
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

// nodesIn returns the nodes a job in partition p can land on.
func (c *Cluster) nodesIn(p string) []Node {
	var nodes []Node
	for _, n := range c.Nodes {
		if slices.Contains(n.Partitions, p) {
			nodes = append(nodes, n)
		}
	}
	return nodes
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
	nodes := c.Nodes
	if ok {
		if r.Minutes > 0 && p.MaxMinutes > 0 && r.Minutes > p.MaxMinutes {
			return nil, fmt.Errorf("the longest job on %s is %s", p.Name, humanTime(p.MaxMinutes))
		}
		if r.Minutes == 0 && p.DefaultMinutes > 0 {
			notices = append(notices, note("No --time given: the job ends after %s, the %s default.", humanTime(p.DefaultMinutes), p.Name))
		}
		nodes = c.nodesIn(p.Name)
	}

	if r.GPUType != "" && !hasGPUType(nodes, r.GPUType) {
		return nil, fmt.Errorf("no GPU type %q; available: %s", r.GPUType, strings.Join(gpuTypes(nodes), ", "))
	}

	if len(nodes) == 0 {
		return notices, nil
	}
	for _, n := range nodes {
		if n.fits(r) {
			return notices, nil
		}
	}
	return nil, whyNoFit(nodes, r)
}

func (n Node) fitsCPUs(r *Request) bool { return r.CPUs <= n.CPUs }
func (n Node) fitsMem(r *Request) bool  { return r.MemMiB <= n.MemMiB }
func (n Node) fitsDisk(r *Request) bool { return r.DiskMiB <= n.TmpDiskMiB }

func (n Node) fitsGPUs(r *Request) bool { return r.GPUs <= n.gpus(r.GPUType) }

// gpus counts the node's GPUs of gpuType, or of any type when it's "".
func (n Node) gpus(gpuType string) int {
	total := 0
	for t, count := range n.GPUs {
		if gpuType == "" || t == gpuType {
			total += count
		}
	}
	return total
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
func whyNoFit(nodes []Node, r *Request) error {
	var maxCPUs, maxGPUs int
	var maxMem, maxDisk, maxGPUMem int64
	for _, n := range nodes {
		maxCPUs = max(maxCPUs, n.CPUs)
		maxMem = max(maxMem, n.MemMiB)
		maxDisk = max(maxDisk, n.TmpDiskMiB)
		maxGPUMem = max(maxGPUMem, n.maxGPUMem(r.GPUType))
		maxGPUs = max(maxGPUs, n.gpus(r.GPUType))
	}

	onType := ""
	if r.GPUType != "" {
		onType = " " + r.GPUType
	}
	var reasons []string
	if r.CPUs > maxCPUs {
		reasons = append(reasons, fmt.Sprintf("the most CPUs a job can get on a node is %d", maxCPUs))
	}
	if r.MemMiB > maxMem {
		reasons = append(reasons, fmt.Sprintf("the most memory a job can get on a node is %s", humanSize(maxMem)))
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

func hasGPUType(nodes []Node, t string) bool {
	for _, n := range nodes {
		if _, ok := n.GPUs[t]; ok {
			return true
		}
	}
	return false
}

func gpuTypes(nodes []Node) []string {
	set := map[string]bool{}
	for _, n := range nodes {
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
