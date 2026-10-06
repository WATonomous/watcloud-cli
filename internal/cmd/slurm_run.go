package cmd

import (
	"fmt"
	"os"
	"watcloud-cli/internal/slurm"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	runReq    slurm.Request
	runMem    string
	runDisk   string
	runGPUMem string
	runTime   string
	runDryRun bool
)

var slurmRunCmd = &cobra.Command{
	Use:   "run [flags] [-- command...]",
	Short: "Request resources and start a job shell (or run a command)",
	Long: `Builds the srun request from what you need, prints it, and runs it.

Scratch disk (/tmp) and memory are fixed when the job starts and can't be raised
afterwards, so ask for enough here. Without --disk, /tmp is only 100 MiB.

Sizes need a unit (512M, 20G, 1T). Times take 30m, 4h, 1d12h or Slurm's 2:00:00.

With --docker, the Docker daemon is started inside the job before your shell
opens, and the job gets 20 GiB of scratch disk unless --disk says otherwise.`,
	Example: `  watcloud slurm run --cpus 4 --mem 16G --time 2h
  watcloud slurm run --gpu-mem 8G --disk 40G --mem 16G --time 4h --docker
  watcloud slurm run --gpus 1 --gpu-type rtx_3090 --time 1h -- python train.py
  watcloud slurm run --disk 50G --dry-run`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
			return fmt.Errorf("put the command after --, e.g. watcloud slurm run --time 1h -- python train.py")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := slurmRun(cmd, args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func slurmRun(cmd *cobra.Command, args []string) error {
	if id := os.Getenv("SLURM_JOB_ID"); id != "" && !runDryRun {
		return fmt.Errorf("already inside job %s; exit it first, then request what you need in a new job", id)
	}

	var err error
	sizes := []struct {
		flag  string
		value string
		dest  *int64
	}{
		{"--mem", runMem, &runReq.MemMiB},
		{"--disk", runDisk, &runReq.DiskMiB},
		{"--gpu-mem", runGPUMem, &runReq.GPUMemMiB},
	}
	for _, s := range sizes {
		if s.value == "" {
			continue
		}
		if *s.dest, err = slurm.ParseSize(s.value); err != nil {
			return fmt.Errorf("%s: %w", s.flag, err)
		}
	}
	if runTime != "" {
		if runReq.Minutes, err = slurm.ParseTime(runTime); err != nil {
			return fmt.Errorf("--time: %w", err)
		}
	}

	runReq.Command = args
	runReq.Shell = os.Getenv("SHELL")
	if runReq.Shell == "" {
		runReq.Shell = "bash"
	}

	// Without sinfo, the cluster checks are skipped and srun has the last word.
	cluster, _ := slurm.LoadCluster()
	notices, err := runReq.Check(cluster)
	if err != nil {
		return err
	}

	for _, n := range notices {
		if n.Warning {
			fmt.Fprintln(os.Stderr, color.YellowString("warning:"), n.Text)
		} else {
			fmt.Fprintln(os.Stderr, color.New(color.Faint).Sprint("note:"), n.Text)
		}
	}

	srun := runReq.Args()
	if runDryRun {
		fmt.Println(slurm.ShellJoin(srun))
		return nil
	}
	fmt.Fprintln(os.Stderr, color.New(color.Bold).Sprint("$ "+slurm.ShellJoin(srun)))
	return slurm.Exec(srun)
}

func init() {
	f := slurmRunCmd.Flags()
	f.IntVar(&runReq.CPUs, "cpus", 0, "CPUs")
	f.StringVar(&runMem, "mem", "", "memory, e.g. 16G")
	f.StringVar(&runDisk, "disk", "", "scratch disk at /tmp, e.g. 40G")
	f.StringVar(&runGPUMem, "gpu-mem", "", "a share of one GPU's memory, e.g. 8G")
	f.IntVar(&runReq.GPUs, "gpus", 0, "whole GPUs, not shared with other jobs")
	f.StringVar(&runReq.GPUType, "gpu-type", "", "GPU model, e.g. rtx_3090")
	f.StringVar(&runTime, "time", "", "time limit, e.g. 4h")
	f.StringVar(&runReq.Partition, "partition", "", "partition (default: the cluster default)")
	f.BoolVar(&runReq.Docker, "docker", false, "start the Docker daemon in the job")
	f.BoolVar(&runDryRun, "dry-run", false, "print the srun command instead of running it")
	slurmCmd.AddCommand(slurmRunCmd)
}
