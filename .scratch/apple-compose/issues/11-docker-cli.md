# 11 Docker CLI front end
Type: prototype
Status: resolved

## Question
mocker ships a Docker CLI clone alongside compose. Should apple-compose, and how should it map `docker` onto the runtime without keeping its own state?

## Answer
A second binary, `apple-docker` (internal/dockercli), that treats the runtime as the source of truth. Docker flags are translated onto `container` arguments; attached commands (`run`, `exec`, `logs -f`, `build`, `pull`, `push`, `start -a`) replace the process with the runtime CLI via execve so TTYs, signals and exit codes pass through untouched. Listing commands parse the runtime's JSON and render Docker's tables, `--format json`, Go templates and `--filter`s. `inspect` presents Docker's shape (`.State`, `.Config`, `.NetworkSettings`, `.Mounts`, `.HostConfig`) with the raw record under `.Runtime`. Flags the runtime cannot honour are accepted and warned about so scripts keep running; impossible ones (`--network host`, ephemeral ports, `pause`, `rename`, `commit`, `events`) fail with the reason. `docker compose` delegates to apple-compose in-process. No manifest commands.
