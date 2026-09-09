# 06 Build the core product
Type: task
Status: resolved

## Progress
Core implemented and verified end to end on 1.3.1: up/down/ps/logs/exec/run/start/stop/restart/rm/kill/port/images/cp/config/ls/wait/version/plugin, dependency conditions, healthchecks, hosts-file discovery, config-hash recreation, attached mode with exit codes. Remaining: end-to-end test harness, Homebrew formula, release pipeline verification.
Blocked by: 01, 02, 03, 04, 05

## Question
Implement: project loading, translation of compose services to `container` invocations, up/down/ps/logs/start/stop/restart/rm/exec/run/pull/build/config/ls/images/kill/port/cp/version/plugin, dependency ordering with all three `depends_on` conditions, healthchecks during `up`, hosts-file discovery, config-hash recreate detection, orphan removal, attached log multiplexing, and unit plus end-to-end tests against the real runtime.

## Answer
Built and verified on container 1.3.1. Unit tests cover translation, hosts rendering, engine JSON decoding and stop grouping; `test/e2e` drives the binary against the live runtime (peer resolution both ways, healthy and completed-successfully dependencies, config-hash recreation, exec and run exit codes, attached `--exit-code-from`, dry run). The fullstack example additionally exercised build args, environment secrets, an internal network, multi-network attachment, profiles from `COMPOSE_PROFILES`, host-backed shared volumes, scaling up and down, and `down -v`. Published on github.com/skuirrels/apple-compose with CI on macOS runners, a goreleaser release workflow, and a Homebrew formula.
