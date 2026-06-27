

# WATcloud CLI


## Features


## Setup & Installation

**Requirements:** Go 1.22+

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
./watcloud status
./watcloud quota list
./watcloud docker status
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

| Subcommand | Description                                      |
|------------|--------------------------------------------------|
| start/run  | Starts the rootless Docker Daemon.                             |
| status     | Lists all non-interactive background user processes (daemons). |

### watcloud subscription <job_id> [email]

Get notified when a SLURM job finishes.

| Usage | Description |
|-------|-------------|
| `watcloud subscription <job_id> <email>` | Email notification when the job completes |
| `watcloud subscription <job_id> --discord` | Discord notification using your saved webhook |
| `watcloud subscription <job_id> --discord <webhook_url>` | Discord notification with an explicit webhook |

Save your Discord webhook once so you don't have to paste it every time. It's stored per-user at `~/.config/watcloud/config.json` with `0600` permissions (readable only by you):

```sh
watcloud config set discord-webhook <webhook_url>
```

To get a webhook URL, in your Discord channel: **Edit Channel → Integrations → Webhooks → New Webhook → Copy Webhook URL**.

### watcloud config <args>

| Subcommand | Description |
|------------|-------------|
| `set <key> <value>` | Save a config value (supported: `discord-webhook`) |
| `get <key>` | Print a saved config value |

---

For help and usage examples, run:
```
./watcloud -h
./watcloud quota -h
./watcloud <command> -h
```
