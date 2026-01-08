

# WATcloud CLI


## Features


## Setup & Installation

**Requirements:** Go 1.22+

Clone the repository:
```sh
git clone https://github.com/vin-jl/watcloud-cli.git
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

---

For help and usage examples, run:
```
./watcloud -h
./watcloud quota -h
./watcloud <command> -h
```
