# Comparison with other compose tools for Apple's container runtime

Ratings come from reading each project's source and documentation on 9 September 2026. Only apple-compose was executed. ✅ supported, ⚠️ partial or with caveats, ❌ missing, ❓ not documented.

| Feature | apple-compose | [mocker](https://github.com/us/mocker) | [Container-Compose](https://github.com/Mcrich23/Container-Compose) |
| --- | --- | --- | --- |
| **Project** | | | |
| Language / licence | Go / Apache-2.0 | Swift / AGPL-3.0 | Swift / MIT |
| Scope | Compose only | Docker CLI clone + compose | Compose only |
| Runtime as source of truth | ✅ | ❌ own state in `~/.mocker` | ✅ |
| Installs as `container compose` plugin | ✅ | ❌ | ❌ |
| Homebrew | ⚠️ tap | ✅ tap | ✅ core formula |
| **Compose file** | | | |
| Parser | ✅ compose-go (Docker's) | ⚠️ hand-rolled | ⚠️ hand-rolled |
| `extends`, `include`, multi-file merge | ✅ | ❌ | ⚠️ partial |
| Profiles, `.env`, interpolation | ✅ | ✅ | ✅ |
| **Service keys** | | | |
| image, command, entrypoint, env, env_file, ports, volumes, networks, labels, user, working_dir | ✅ | ✅ | ✅ |
| build (args, target, secrets, ssh, platforms) | ✅ | ⚠️ args, target | ⚠️ args, target |
| healthcheck | ✅ probed by tool | ❌ | ✅ probed by tool |
| depends_on `service_healthy` | ✅ | ❌ ordering only | ✅ |
| depends_on `service_completed_successfully` | ✅ | ❌ | ❌ |
| secrets / configs (file, env, inline) | ✅ | ❌ | ⚠️ file only |
| extra_hosts, links, network aliases | ✅ | ❌ | ⚠️ extra_hosts |
| dns, dns_search, dns_opt | ✅ | ❌ | ❌ |
| cap_add, cap_drop, read_only, init, tmpfs, shm_size, ulimits | ✅ | ⚠️ shm_size | ⚠️ read_only, privileged |
| stop_signal, stop_grace_period | ✅ | ❌ | ❌ |
| scale / deploy.replicas | ✅ | ❌ planned | ❌ |
| pull_policy | ✅ | ❌ | ❌ |
| restart policies | ⚠️ attached only | ⚠️ own supervisor state | ❌ flag ignored by runtime |
| hostname | ⚠️ hosts alias | ⚠️ | ⚠️ |
| **Commands** | | | |
| up, down, ps, logs | ✅ | ✅ | ⚠️ up, down only |
| build, pull, push | ✅ | ✅ | ⚠️ build |
| exec, run, start, stop, restart, rm, kill | ✅ | ✅ | ❌ |
| config, create, images, top, port, ls | ✅ | ✅ | ❌ |
| cp, wait | ✅ | ❌ | ❌ |
| pause, unpause, events | ❌ runtime lacks it | ❌ stubs | ❌ |
| `--exit-code-from`, `--abort-on-container-exit` | ✅ | ❌ | ❌ |
| `--wait`, `--scale`, `--remove-orphans`, `--no-deps` | ✅ | ⚠️ some | ❌ |
| `--dry-run` | ✅ | ✅ | ❌ |
| `logs --timestamps/--since/--until` | ⚠️ read-time stamps, window by container start | ❓ | ❌ |
| Config-hash recreate on change | ✅ | ✅ | ❌ recreates always |
| **Runtime mechanics** | | | |
| Service discovery | ✅ hosts file mounted before start | ⚠️ exec append after start | ⚠️ exec append after start |
| Works with images lacking `sh` | ✅ | ❌ | ❌ |
| Exit codes of containers | ✅ via attach | ❓ | ⚠️ one process |
| Fresh volume usable by postgres | ✅ empties `lost+found` | ✅ own directories | ❌ |
| Volume shared by two services | ⚠️ host-dir escape hatch | ✅ own directories | ❌ |
| Port publishing | ⚠️ runtime forwarder | ✅ own TCP proxy | ⚠️ runtime forwarder |
| **Quality** | | | |
| Unit tests | ✅ fake-runtime harness | ✅ | ✅ |
| Live end-to-end suite | ✅ | ❓ | ❓ |
| Signed / notarised binaries | ❌ | ❓ | ❓ |
