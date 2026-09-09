# apple-compose

`docker compose` for [Apple's `container` runtime](https://github.com/apple/container) on macOS.

apple-compose reads standard Compose files (`compose.yaml`, `docker-compose.yml`, overrides, `.env`, profiles, `extends`, `include`) and runs them on Apple's native, VM-isolated Linux containers. It keeps Docker Compose's command names, flags, container names and labels, so existing projects and muscle memory carry over.

```console
$ apple-compose up -d
 ✔ Network shop_default        Created
 ✔ Volume shop_pgdata          Created
 ✔ Container shop-db-1         Started
 ⠿ Waiting for shop-db-1 to be healthy
 ✔ Container shop-db-1         Healthy
 ✔ Container shop-api-1        Started
 ✔ Container shop-web-1        Started
$ apple-compose ps
NAME         IMAGE            COMMAND                 SERVICE   CREATED          STATUS          PORTS
shop-api-1   shop-api         "node server.js"        api       12 seconds ago   Up 10 seconds   0.0.0.0:3000->3000/tcp
shop-db-1    postgres:16      "docker-entrypoint.s…"  db        14 seconds ago   Up 13 seconds
shop-web-1   nginx:alpine     "nginx -g daemon off;"  web       11 seconds ago   Up 9 seconds    0.0.0.0:8080->80/tcp
```

## Requirements

- Apple silicon Mac running **macOS 26** or later (the runtime's custom networks need it).
- [Apple `container`](https://github.com/apple/container/releases) 1.3 or later, with its services started (`container system start`).

macOS asks once whether the runtime's helper (`container-runtime-linux`) may use the local network. Published ports stay unreachable until that is allowed under System Settings › Privacy & Security › Local Network.

## Install

### Homebrew

The formula is served from this repository as a tap while the project is being tested. homebrew-core requires a project to be notable (roughly 75 stars, 30 forks or 30 watchers) before accepting a formula, so submission waits until the repository qualifies.

```bash
brew tap skuirrels/apple-compose https://github.com/skuirrels/apple-compose
brew trust skuirrels/apple-compose   # Homebrew 6 refuses third-party taps until trusted
brew install skuirrels/apple-compose/apple-compose
```

Upgrade later with `brew upgrade apple-compose`.

### Direct from GitHub

Each [release](https://github.com/skuirrels/apple-compose/releases) ships a `darwin_arm64` archive and a `checksums.txt`. Pick a version, verify it, and place the binary on your `PATH`:

```bash
VERSION=0.1.5
curl -fsSLO "https://github.com/skuirrels/apple-compose/releases/download/v${VERSION}/apple-compose_${VERSION}_darwin_arm64.tar.gz"
curl -fsSLO "https://github.com/skuirrels/apple-compose/releases/download/v${VERSION}/checksums.txt"
grep "apple-compose_${VERSION}_darwin_arm64.tar.gz" checksums.txt | shasum -a 256 -c -
tar -xzf "apple-compose_${VERSION}_darwin_arm64.tar.gz" apple-compose
sudo install -m 0755 apple-compose /usr/local/bin/apple-compose
```

If you downloaded the archive with a browser rather than `curl`, macOS quarantines it; clear that before running:

```bash
xattr -d com.apple.quarantine /usr/local/bin/apple-compose
```

Release binaries are not yet signed. The release workflow signs and notarises them automatically once these repository secrets exist: `MACOS_SIGN_P12` (base64 Developer ID Application certificate), `MACOS_SIGN_PASSWORD`, and an App Store Connect API key as `MACOS_NOTARY_ISSUER_ID`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_KEY`.

### From source

Requires Go 1.27 or later:

```bash
go install github.com/skuirrels/apple-compose/cmd/apple-compose@latest
```

or clone the repository and run `make build`, which produces `bin/apple-compose`.

Whichever route you take, confirm the binary sees the runtime:

```bash
apple-compose version
```

### Use it as `container compose`

The `container` CLI discovers plugins, so apple-compose can install itself as a subcommand:

```bash
apple-compose plugin install
container compose up -d
```

## Usage

The command surface mirrors Docker Compose. Global options go before the subcommand, exactly as with `docker compose`:

```bash
apple-compose -f compose.yaml -f compose.override.yaml -p myproject --profile debug up -d
```

| Command | Notes |
| --- | --- |
| `up`, `down`, `start`, `stop`, `restart`, `kill`, `rm`, `create` | Dependency ordering, `depends_on` conditions (`service_started`, `service_healthy`, `service_completed_successfully`), `--wait`, `--scale`, `--remove-orphans`, `--force-recreate`, `--exit-code-from`, `--abort-on-container-exit`, `--no-deps`, `--build`, `--pull`. Changed services are recreated using a config hash, unchanged ones are left alone. |
| `ps`, `ls`, `images`, `port`, `top`, `logs` | `ps --format json`, `ps --health` (probes healthchecks on demand), `logs -f --tail --since --until -t`. |
| `exec`, `run`, `cp`, `wait` | `run --rm` one-off containers with dependencies started first. |
| `pull`, `push`, `build` | `build` uses the runtime's BuildKit builder with `args`, `target`, `platforms`, `secrets`, `ssh`, `labels`, `no_cache`, `pull`. |
| `config` | Renders the resolved project (`--services`, `--images`, `--hash`, `--format json`). |
| `version`, `plugin` | `plugin install` registers `container compose`. |

Add `--dry-run` to any command to print the `container` invocations without running them, and `--debug` to echo them as they run.

## How it maps Compose onto the runtime

Apple's runtime runs each container in its own lightweight VM and exposes a CLI with JSON output. apple-compose drives that CLI; it keeps no state of its own beyond generated hosts files. The runtime is always the source of truth, so `container ls` shows exactly what apple-compose created, labelled with the usual `com.docker.compose.*` labels.

### Service discovery

The runtime cannot resolve bare container names on custom networks ([apple/container#1809](https://github.com/apple/container/issues/1809)). apple-compose therefore bind-mounts a generated hosts file over `/etc/hosts` in every service container and rewrites it as peers start. Names resolve without a shell in the image, without DNS setup, and immediately after a peer is (re)created:

- every service name, container name, `container_name`, `hostname` and network alias of peers on a shared network;
- `extra_hosts`, including `host-gateway`;
- `host.docker.internal`, `gateway.docker.internal` and `host.containers.internal` for the Mac itself.

Disable it per service with `x-apple-compose: {hosts_file: false}`.

### Supported Compose attributes

`image`, `build`, `command`, `entrypoint`, `environment`, `env_file`, `working_dir`, `user`, `ports` (including ranges), `expose`, `volumes` (bind, named, anonymous, tmpfs, read-only), `tmpfs`, `networks` (several per service, aliases, `mac_address`), `network_mode: none`, `dns`, `dns_search`, `dns_opt`, `extra_hosts`, `links`, `depends_on`, `healthcheck`, `labels`, `container_name`, `scale` and `deploy.replicas`, `cpus` and `deploy.resources.limits.cpus` (rounded up to whole CPUs), `mem_limit` and `deploy.resources.limits.memory`, `shm_size`, `ulimits`, `cap_add`, `cap_drop`, `read_only`, `init`, `platform`, `stop_signal`, `stop_grace_period`, `tty`, `stdin_open`, `profiles`, `pull_policy`, `secrets` and `configs` (file, environment and inline content, mounted read-only), `extends`, `include`, `x-*` extensions.

### Where the runtime differs from Docker

| Compose feature | Behaviour on Apple's runtime |
| --- | --- |
| `restart` | The runtime has no restart policies, so apple-compose supervises them. `up` in the foreground restarts exited services itself; `up -d` and `start` launch a per-project background supervisor that restarts them, honours `on-failure[:N]`, and leaves containers stopped by `stop`, `kill`, or `down` alone. The first exit of a detached container has no known exit code and counts as a failure; later exits are exact. The supervisor logs to `~/Library/Application Support/apple-compose/projects/<project>/supervisor.log` and exits when nothing is left to restart; `ps` shows `Up 5 seconds (restarted 2)` for containers it has restarted. |
| `healthcheck` | No daemon runs checks continuously. apple-compose probes during `up` (for `depends_on` and `--wait`) and on `ps --health`. |
| Named volumes | Runtime volumes are ext4 disk images that start with a `lost+found` directory. apple-compose empties a freshly created volume with the first image that mounts it, so database images such as `postgres` initialise as they do on Docker. |
| Named volumes shared by several services | A named volume is a disk image attached to one running container at a time. Use a bind mount, Docker's `driver_opts: {type: none, o: bind, device: ./path}`, or `x-apple-compose: {shared: true}` on the volume to back it with a host directory. |
| `hostname` | The runtime derives the hostname from the container name; the requested hostname becomes a hosts-file alias. |
| Ephemeral host ports (`ports: ["80"]`) | The runtime cannot allocate host ports; set a published port. |
| `network_mode: host`, `service:`, `container:` | Not possible for VM-isolated containers; an error is raised. |
| `privileged` | Mapped to `cap_add: [ALL]` inside the VM. |
| `devices`, `sysctls`, `security_opt`, `pid`, `ipc`, `userns_mode`, `cgroup*`, `group_add`, `volumes_from`, `gpus`, `pids_limit`, `cpuset`, `logging` | Ignored with a warning. |
| `cp` with a bind-mounted path | The runtime copies from the container's root filesystem only. |
| `pause`, `unpause`, `events` | Not supported by the runtime. |
| `logs --since/--until/--timestamps` | The runtime stores lines without times. `--timestamps` stamps each line with the time it was read; `--since`/`--until` include a container's stored output when it started inside the window and filter live output line by line. |

### The `x-apple-compose` extension

```yaml
services:
  api:
    image: ghcr.io/example/api
    x-apple-compose:
      args: ["--ssh"]          # extra `container create` arguments, verbatim
      hosts_file: true          # generated /etc/hosts (default true)
      rosetta: false            # force Rosetta translation on
      virtualization: false     # expose nested virtualisation
      kernel: /path/to/vmlinux  # custom guest kernel
volumes:
  assets:
    x-apple-compose:
      shared: true              # host-directory backed, mountable by several services
```

## Environment variables

- `CONTAINER_BIN`: path to the `container` executable when it is not on `PATH`.
- `APPLE_COMPOSE_HOME`: state directory (default `~/Library/Application Support/apple-compose`).
- `COMPOSE_FILE`, `COMPOSE_PROJECT_NAME`, `COMPOSE_PROFILES`, `COMPOSE_PATH_SEPARATOR`: honoured as by Docker Compose.

## Development

```bash
make build      # bin/apple-compose
make test       # unit tests
make cover      # unit tests with a coverage summary (coverage.out)
make e2e        # end-to-end tests against a running container runtime
```

Unit tests drive the orchestration code against a fake `container` executable (`internal/enginetest`), so they need no runtime; they cover `up` planning, recreation, scaling, dependency conditions, the restart supervisor, and every lifecycle command. `make e2e` boots real containers.

A feature matrix against the other compose tools for this runtime is in [docs/COMPARISON.md](docs/COMPARISON.md). Decisions taken while charting the project live in `.scratch/apple-compose/`.

## Licence

Apache-2.0.
