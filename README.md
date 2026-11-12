

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
