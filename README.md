

# WATcloud CLI


## Features


## Setup & Installation

### Build with Docker (recommended for cluster testing)

The [`Dockerfile`](./Dockerfile) is the single source of truth for building the
binary; CI and releases build through it. It produces a static `linux/amd64`
binary, which is what the cluster runs.

BuildKit can use a Git URL as the build context, so you can build any branch,
tag, or commit without cloning:

```sh
# <ref> is a branch, tag, or commit SHA
docker buildx build --platform linux/amd64 --target binary --output type=local,dest=dist \
  "https://github.com/WATonomous/watcloud-cli.git#<ref>"
```

The binary lands at `dist/watcloud`. From a local checkout, pass `.` as the
context instead of the Git URL.

#### On WATcloud

Start [rootless Docker inside a Slurm job](https://cloud.watonomous.ca/docs/compute-cluster/slurm#using-docker)
(see the [Slurm getting-started docs](https://cloud.watonomous.ca/docs/compute-cluster/slurm)),
then build a given PR branch straight from GitHub — no clone, nothing left
behind but `dist/watcloud`:

```sh
# Start the rootless Docker daemon (prints the DOCKER_HOST line to export)
slurm-start-dockerd.sh   # or: watcloud docker start
export DOCKER_HOST=unix://${XDG_RUNTIME_DIR:-/tmp/run}/docker.sock

# Build the PR branch (the cluster is already linux/amd64, so --platform is optional)
docker buildx build --target binary --output type=local,dest=dist \
  "https://github.com/WATonomous/watcloud-cli.git#<branch>"

./dist/watcloud quota list
```

To test a PR without building at all, download the `watcloud-linux-amd64`
artifact that CI attaches to every PR run (the CI bot comments a direct link and
checksum on the PR).

### Build from source

**Requirements:** Go 1.23+

Clone the repository:
```sh
git clone https://github.com/WATonomous/watcloud-cli.git
cd watcloud-cli
```

Build:
```sh
go build -o watcloud ./cmd/watcloud
```

Run:
```sh
./watcloud quota list
```

## Project Structure

- `cmd/` - CLI entrypoints
- `internal/` - Command implementations

## Commands
### watcloud du

| Key | Action |
|-----|--------|
| Up/Down | Select an item |
| Enter/Right | Open a folder |
| Left/Backspace | Go back |
| `r` | Refresh |
| `q` / Escape / Ctrl+C | Quit |

Hidden files are included. Unavailable sizes show `?`; percentages are omitted when the home limit is unavailable or unlimited.

### watcloud quota <args>

| Subcommand | Description |
|------------|--------------------------------------------------|
| list       | Lists all quota usage (disk, memory, CPU).       |
| disk       | Shows your disk usage percentage and free space. |
| cpu        | Shows CPUs in use against the current allocation. |
| memory     | Shows memory usage statistics.                   |

`watcloud quota cpu` samples CPU time over about two seconds and reports, for
example, `Using 1.9 of 3 CPUs`. On a login node, it reports usage against the
user's CPU cap, including recorded throttling. In a Slurm job, it reports the
job's aggregate usage and allocation on the current node, including its steps.
It identifies the scope from the process's cgroup rather than Slurm environment
variables; an `salloc` shell that remains on a login node still uses the login cap.

### watcloud slurm run [flags] [-- command...]

Builds the `srun` request from what you need, prints it, and runs it. With no command you land in an interactive shell in the job. With `--batch` it submits with `sbatch` instead.

| Flag | Description |
|------|-------------|
| `--cpus N` | CPUs |
| `--mem 16G` | Memory. Without it a job gets 512 MiB per CPU. |
| `--disk 40G` | Scratch disk at `/tmp`. Without it `/tmp` is only 100 MiB. |
| `--gpu-mem 8G` | A share of one GPU's memory |
| `--gpus N` | Whole GPUs, not shared with other jobs |
| `--gpu-type rtx_3090` | GPU model, with `--gpus` or `--gpu-mem` |
| `--time 4h` | Time limit (`30m`, `4h`, `1d12h`, or Slurm's `2:00:00`) |
| `--partition` | Partition |
| `--docker` | Start the Docker daemon in the job. Defaults `--disk` to 20 GiB. |
| `--batch` | Submit the command or script with `sbatch`. Needs a time limit, from `--time` or the script. |
| `--dry-run` | Print the `srun` or `sbatch` command instead of running it |

Scratch disk and memory are fixed when the job starts, so ask for enough up front. Sizes need a unit, so `--disk 20` is an error rather than 20 MiB. Requests no node could ever run, like more scratch disk than any node has, are rejected before submitting.

```sh
watcloud slurm run --gpu-mem 8G --disk 40G --mem 16G --time 4h --docker
watcloud slurm run --gpus 1 --mem 16G --time 1h -- python train.py
```

Batch jobs print the job ID and output file (`slurm-<id>.out` in the directory you submitted from, unless the script sets `--output`). The `compute_dense` partition only accepts batch jobs.

A script's `#SBATCH` lines still apply and count in the checks, so a script that sets its own `--partition` and `--time` needs no flags. Flags override them one at a time: `--disk` replaces only the script's tmpdisk and keeps its GPU, and `--gpus` or `--gpu-mem` replace only its GPU request. When the script runs inside the Docker wrapper or as `bash train.sh`, sbatch never reads it, so `watcloud` passes its `#SBATCH` lines on the command line instead. With `--docker` the script must be executable.

sbatch runs a copy of the script from Slurm's spool directory, so it should find other files by absolute path or relative to the directory you submit from (`$SLURM_SUBMIT_DIR`), not relative to the script, e.g. with `$(dirname "$0")`.

```sh
watcloud slurm run --batch --gpu-mem 8G --mem 16G --time 12h -- python train.py
watcloud slurm run --batch -- ./train.sh
```

### watcloud docker <args>

| Subcommand | Description                        |
|------------|------------------------------------|
| start/run  | Starts the rootless Docker Daemon. |

### watcloud subscription <job_id> [email]

Get notified when a SLURM job finishes.

| Usage | Description |
|-------|-------------|
| `watcloud subscription <job_id> <email>` | Email notification when the job completes |
| `watcloud subscription <job_id> --discord` | Discord notification using `$WATCLOUD_DISCORD_WEBHOOK` |
| `watcloud subscription <job_id> --discord <webhook_url>` | Discord notification with an explicit webhook |

To avoid pasting your Discord webhook every time, export it from your shell profile (e.g. `~/.bashrc`). Anyone who can read the webhook can post to your channel, so keep that file readable only by you:

```sh
export WATCLOUD_DISCORD_WEBHOOK=<webhook_url>
```

To get a webhook URL, in your Discord channel: **Edit Channel → Integrations → Webhooks → New Webhook → Copy Webhook URL**.

---

For help and usage examples, run:
```
./watcloud -h
./watcloud quota -h
./watcloud <command> -h
```
