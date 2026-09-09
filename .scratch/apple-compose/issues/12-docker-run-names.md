# 12 Name resolution for containers apple-docker creates
Type: prototype
Status: resolved

## Question
Compose services resolve one another through a generated hosts file, but a plain `docker run --network custom` container got none, so containers apple-docker created could not resolve each other or their compose neighbours. How should that gap close without a second source of truth?

## Answer
apple-docker mounts the same generated `/etc/hosts` whenever a container joins a user-defined network or is given `--add-host`, which is where Docker resolves names too, and rewrites every generated file after `run`, `create`, `start`, `stop`, `restart`, `kill` and `rm`. Peers now come from every container the runtime knows, not just one project's, so compose services and plain containers see each other on a shared network. The names a container answers to (hostname, per-network aliases, extra hosts, link aliases) are recorded as labels on the container itself, so any process can rebuild a file without the compose project to hand; a `com.apple-compose.names` marker says the record is complete, and a container created before the labels existed is only rewritten by the project that owns it. `--add-host` and `--network-alias` are honoured rather than ignored, and a container that needs a hosts file is created and started in two steps rather than by execve, so its address reaches its neighbours.
