# apple-compose

`docker compose` and a `docker` command line for [Apple's `container` runtime](https://github.com/apple/container) on macOS.

Two binaries ship together: `apple-compose`, the Compose implementation, and `apple-docker`, a Docker CLI front end that translates `docker run`, `ps`, `build`, `network`, `volume` and the rest onto the runtime, so `alias docker=apple-docker` keeps scripts and habits working.

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
- [Apple `container`](https://github.com/apple/container/releases) 1.3 or later, with its services started (`container system start`). Tested against 1.3.1 and 1.4.1.

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
VERSION=0.3.4
curl -fsSLO "https://github.com/skuirrels/apple-compose/releases/download/v${VERSION}/apple-compose_${VERSION}_darwin_arm64.tar.gz"
curl -fsSLO "https://github.com/skuirrels/apple-compose/releases/download/v${VERSION}/checksums.txt"
grep "apple-compose_${VERSION}_darwin_arm64.tar.gz" checksums.txt | shasum -a 256 -c -
tar -xzf "apple-compose_${VERSION}_darwin_arm64.tar.gz" apple-compose apple-docker
sudo install -m 0755 apple-compose apple-docker /usr/local/bin/
```

If you downloaded the archive with a browser rather than `curl`, macOS quarantines it; clear that before running:

```bash
xattr -d com.apple.quarantine /usr/local/bin/apple-compose /usr/local/bin/apple-docker
```

Release binaries carry Go's ad-hoc signature but are not notarised: that needs an Apple Developer Program membership, which this project does not have. Neither install path above trips Gatekeeper: Homebrew compiles from source on your machine, and `curl` downloads carry no quarantine attribute. The `xattr` step is only for archives fetched with a browser. The release workflow is ready to sign and notarise should Developer ID secrets ever be added.

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

When the runtime is installed system-wide under `/usr/local`, the plugin directory is root-owned, so run `sudo apple-compose plugin install` once.

### Use it as `docker`

`apple-docker` speaks Docker's CLI. Alias it, or symlink it as `docker`, and existing scripts run unchanged:

```bash
alias docker=apple-docker
docker run -d --name web -p 8080:80 nginx:alpine
docker ps --format '{{.Names}} {{.Status}}'
docker exec -it web sh
docker compose up -d
```

## Usage

The command surface mirrors Docker Compose. Global options go before the subcommand, exactly as with `docker compose`:

```bash
apple-compose -f compose.yaml -f compose.override.yaml -p myproject --profile debug up -d
```

| Command | Notes |
| --- | --- |
| `up`, `down`, `start`, `stop`, `restart`, `kill`, `rm`, `create` | Dependency ordering, `depends_on` conditions (`service_started`, `service_healthy`, `service_completed_successfully`), `--wait`, `--scale`, `--remove-orphans`, `--force-recreate`, `--exit-code-from`, `--abort-on-container-exit`, `--attach-dependencies`, `--no-deps`, `--build`, `--pull`. Changed services are recreated using a config hash, unchanged ones are left alone. |
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
| Ephemeral host ports (`ports: ["80"]`) | The runtime cannot allocate host ports; set a published port. A host range with one container port (`"8000-8010:80"`) publishes the first free port in the range, as Docker does. |
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

## The Docker CLI front end

`apple-docker` accepts Docker's commands, flags and `--format` templates and translates them onto the `container` CLI. Attached commands (`run`, `exec`, `logs -f`, `build`, `pull`, `push`, `start -a`) hand the terminal straight to the runtime, so TTYs, signals and exit codes behave as with Docker. Listing commands read the runtime's JSON and print Docker's tables, `--format json`, and Go templates such as `{{.Names}}` or `{{.State.Status}}`.

| Command group | Supported | Notes |
| --- | --- | --- |
| `run`, `create` | `-d`, `--rm`, `-it`, `--name`, `-p`, `-v`, `--mount`, `--tmpfs`, `-e`, `--env-file`, `-w`, `-u`, `-l`, `--network`, `--dns*`, `--entrypoint`, `--platform`, `--cpus`, `-m`, `--cap-add/drop`, `--privileged`, `--read-only`, `--init`, `--shm-size`, `--ulimit`, `--cidfile`, `--pull`, `--restart` | `--cpus` rounds up to whole CPUs; `--network host`, `-p 80` without a host port, and `--publish-all` are refused; `--restart` on a detached container is honoured by apple-compose's supervisor (the containers form the pseudo project `apple-docker`); `--hostname` warns; cgroup, device, healthcheck and logging flags are accepted and ignored with a warning. |
| `ps`, `container ls` | `-a`, `-q`, `-n`, `-l`, `--no-trunc`, `--filter name/id/status/label/ancestor/network/volume`, `--format table/json/template` | `--size` is unavailable. |
| `start`, `stop`, `restart`, `kill`, `rm`, `wait`, `port`, `top`, `cp`, `export`, `stats`, `logs`, `exec`, `attach` | Docker's flags | `wait` prints 0 because the runtime reports no exit code for detached containers; `attach` works only on stopped containers; `logs --since/--until` are ignored. |
| `inspect` | containers, images, networks, volumes; `--format`, `--type` | Docker-shaped JSON (`.State`, `.Config`, `.NetworkSettings`, `.Mounts`, `.HostConfig`) with the runtime's full record under `.Runtime`. |
| `images`, `pull`, `push`, `tag`, `rmi`, `build`, `image ls/rm/inspect/prune/save/load` | Docker's flags; `--filter reference=`, `--digests`, `--format` | `build -q` uses plain progress because the runtime's quiet mode hangs; `history` and `import` are unavailable. |
| `network ls/create/rm/inspect/prune` | `--subnet`, `--internal`, `--label`, `-o`, filters, `--format` | `connect`/`disconnect` are impossible: the runtime attaches networks at create time. |
| `volume ls/create/rm/inspect/prune` | `--label`, `--opt` (`size=` maps to the runtime's size), filters, `--format` | |
| `system df/prune/info`, `info`, `version`, `login`, `logout` | | `prune` asks for confirmation like Docker; `login -p` feeds the runtime's stdin. |
| `container clean`, `system clean` | apple-docker extension | Runs the runtime's `clean` (1.4 or later), which trims unused blocks from container disks to give space back to the host. |
| `compose` | everything apple-compose does | Runs in-process. |
| `pause`, `unpause`, `rename`, `commit`, `diff`, `events`, `update` | ❌ | Each explains why the runtime cannot do it. |

Global Docker flags (`-H`, `--context`, `--config`, `-l`, `--tls*`) are accepted and ignored. `--dry-run` prints the translated `container` command instead of running it.

## Environment variables

| Variable | Effect |
| --- | --- |
| `APPLE_COMPOSE_DNS` | Comma-separated nameservers given to every container and image build that sets none of its own. Use it when `nslookup` inside a container fails while the host resolves fine: the runtime's NAT resolver on the network gateway is then being blocked, typically by the macOS application firewall or a VPN client. Example: `export APPLE_COMPOSE_DNS=1.1.1.1`. |
| `CONTAINER_BIN` | Path to the `container` executable when it is not on `PATH`. |
| `APPLE_COMPOSE_HOME` | State directory (default `~/Library/Application Support/apple-compose`). |
| `COMPOSE_FILE`, `COMPOSE_PROJECT_NAME`, `COMPOSE_PROFILES`, `COMPOSE_PATH_SEPARATOR` | Honoured as by Docker Compose. |

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
