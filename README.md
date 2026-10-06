

# WATcloud CLI


## Features


## Setup & Installation

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
### watcloud quota <args>

| Subcommand | Description |
|------------|--------------------------------------------------|
| list       | Lists all quota usage (disk, memory, CPU).       |
| disk       | Shows your disk usage percentage and free space. |
| cpu        | Displays CPU usage percentage.                   |
| memory     | Shows memory usage statistics.                   |

### watcloud slurm run [flags] [-- command...]

Builds the `srun` request from what you need, prints it, and runs it. With no command you land in an interactive shell in the job.

| Flag | Description |
|------|-------------|
| `--cpus N` | CPUs |
| `--mem 16G` | Memory |
| `--disk 40G` | Scratch disk at `/tmp`. Without it `/tmp` is only 100 MiB. |
| `--gpu-mem 8G` | A share of one GPU's memory |
| `--gpus N` | Whole GPUs, not shared with other jobs |
| `--gpu-type rtx_3090` | GPU model, with `--gpus` or `--gpu-mem` |
| `--time 4h` | Time limit (`30m`, `4h`, `1d12h`, or Slurm's `2:00:00`) |
| `--partition` | Partition |
| `--docker` | Start the Docker daemon in the job. Defaults `--disk` to 20 GiB. |
| `--dry-run` | Print the `srun` command instead of running it |

Scratch disk and memory are fixed when the job starts, so ask for enough up front. Sizes need a unit, so `--disk 20` is an error rather than 20 MiB. Requests no node could ever run, like more scratch disk than any node has, are rejected before submitting.

```sh
watcloud slurm run --gpu-mem 8G --disk 40G --mem 16G --time 4h --docker
watcloud slurm run --gpus 1 --time 1h -- python train.py
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
