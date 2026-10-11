

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

### watcloud docker <args>

| Subcommand | Description                        |
|------------|------------------------------------|
| start/run  | Starts the rootless Docker Daemon. |

### watcloud slurm <args>

| Subcommand | Description |
|------------|-------------|
| capacity | Shows what the cluster can give you right now. |
| usage [job_id] | Shows a job's peak GPU memory and utilisation next to the GPU memory it requested. Without a job ID, shows the current job inside a job, or your GPU jobs from the last 7 days. |
| usage --all | Shows every user's GPU jobs from the last 7 days that went over their request. Other users' jobs are only visible to root and Slurm operators/administrators. |

GPU sharing isn't enforced, so a job can use more GPU memory than its `shard` request and crowd out other jobs on the same GPU. `watcloud slurm usage` shows when that happens:

```
job 700512 (train, completed): requested 1.0 GiB of GPU memory, peaked at 1.7 GiB, utilisation 99%
```

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
