# 06 Build the core product
Type: task
Status: claimed

## Progress
Core implemented and verified end to end on 1.3.1: up/down/ps/logs/exec/run/start/stop/restart/rm/kill/port/images/cp/config/ls/wait/version/plugin, dependency conditions, healthchecks, hosts-file discovery, config-hash recreation, attached mode with exit codes. Remaining: end-to-end test harness, Homebrew formula, release pipeline verification.
Blocked by: 01, 02, 03, 04, 05

## Question
Implement: project loading, translation of compose services to `container` invocations, up/down/ps/logs/start/stop/restart/rm/exec/run/pull/build/config/ls/images/kill/port/cp/version/plugin, dependency ordering with all three `depends_on` conditions, healthchecks during `up`, hosts-file discovery, config-hash recreate detection, orphan removal, attached log multiplexing, and unit plus end-to-end tests against the real runtime.
