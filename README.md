

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
### watcloud du

Browse your home folder, largest items first, with sizes and percentages of your
home storage limit. Hidden files are included.

| Key | Action |
|-----|--------|
| Up/Down | Select an item |
| Enter/Right | Open a folder |
| Left/Backspace | Go back |
| `r` | Refresh |
| `q` / Escape / Ctrl+C | Quit |

Requires an interactive terminal and a CephFS-backed home on Linux.
Symlinks are not followed, and other mounted filesystems are excluded.
Unavailable sizes show `?`; percentages are omitted when the home limit is unavailable or unlimited.

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
