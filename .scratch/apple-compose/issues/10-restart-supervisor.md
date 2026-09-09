# 10 Restart policies for detached containers
Type: prototype
Status: resolved

## Question
The runtime has no restart policies and no daemon. Foreground `up` already re-runs `container start --attach` when a service's `restart:` asks for it; what should honour the policy after `up -d` returns?

## Answer
A per-project background supervisor. `up -d`, `start` and `restart` launch the hidden `apple-compose supervise` command as a new session when any running container carries the `com.apple-compose.restart` label; a `flock` on `<state>/projects/<project>/supervisor.lock` keeps it to one per project and lets `down` and foreground `up` find and terminate it. It polls `container ls` every two seconds, restarts exited containers with `container start --attach` (the only way to learn exit codes), records the codes, honours `on-failure[:N]`, and exits when nothing is left to supervise. Containers stopped by `stop`, `kill`, `down`, replica retirement or the foreground session get a marker file under `<state>/projects/<project>/stopped/`, which the supervisor respects and `up`/`start` clear. The first exit of a container started detached has no known code and is treated as a failure; later exits are exact. No launchd agent: the supervisor lives as long as the login session, matching the runtime's own containers, which do not survive a reboot either.
