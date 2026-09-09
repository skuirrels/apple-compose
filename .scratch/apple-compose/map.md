# Wayfinder map: apple-compose

## Destination

A polished, public, installable `docker compose` equivalent for Apple's `container` runtime (github.com/apple/container): `apple-compose up|down|ps|logs|...` reading standard compose files, published at github.com/skuirrels/apple-compose with releases and a Homebrew tap.

## Notes

- Domain: macOS 26 container orchestration. Runtime is Apple `container` 1.3.1 (Swift, XPC daemon, CLI with `--format json`).
- Override of "plan, don't do": the user asked for continuous execution to a polished product, so tickets here carry execution, not only decisions.
- Skills to consult per session: research (runtime facts), domain-modeling (terminology in CONTEXT.md).
- Standing preferences: British English in prose; compose syntax kept as close to Docker Compose as possible; never degrade the product for compatibility's sake; latest Go and latest module versions.
- Runtime probing environment: `container` is relocated to `~/.local/apple-container/bin` (installer payload extracted without admin rights); service started via launchd user agent.

## Decisions so far

- [01 Language and parsing library](issues/01-language.md): Go with compose-go v2; Rust and Swift rejected.
- [02 Runtime integration surface](issues/02-runtime-surface.md): drive the `container` CLI via subprocess and parse `--format json`; no XPC or Swift dependency.
- [03 Service discovery](issues/03-service-discovery.md): per-container hosts file bind-mounted over `/etc/hosts`, regenerated live; daemon DNS not relied upon.
- [04 Naming, labels and state](issues/04-naming-labels-state.md): Docker Compose names and `com.docker.compose.*` labels; tool state under `~/Library/Application Support/apple-compose`.
- [05 Plugin packaging](issues/05-plugin-packaging.md): standalone `apple-compose` binary, installable as `container compose` CLI plugin.
- [07 Exit codes](issues/07-exit-codes.md): the runtime reports exit codes only to an attached client, so apple-compose attaches when it needs them and records them under its state directory.
- [06 Core build](issues/06-core-build.md): product implemented, tested end to end, and published; release tooling in place.
- [08 Shared named volumes](issues/08-shared-volumes.md): runtime volumes are single-attach disk images; multi-service volumes get a warning and can be backed by a host directory via Docker's `driver_opts` bind syntax or `x-apple-compose: {shared: true}`.

## Not yet specified

- Restart policies: the runtime has none; whether the attached `up` loop should emulate `restart:` for foreground sessions.
- Continuous health monitoring outside `up` (no daemon exists to run checks); `ps --health` probes on demand for now.
- Port publishing could not be verified on the charting machine: the launchd-spawned runtime helper gets "no route to host" when forwarding, consistent with macOS Local Network privacy for a relocated install.
- Rosetta / amd64 image handling defaults when a compose file sets `platform`.
- Secrets and configs beyond file-backed bind mounts (environment-backed secrets).
- Compose `develop.watch` file sync.

## Out of scope

- Implementing a Docker Engine API socket shim (a different product; see appautomaton/docker-for-apple-container).
- macOS 15 support: the runtime has no custom networks or container-to-container networking there.
- Kubernetes (`container k8s`) integration.
