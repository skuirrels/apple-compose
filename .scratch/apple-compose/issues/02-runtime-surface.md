# 02 Runtime integration surface
Type: research
Status: resolved

## Question
Talk to the daemon over XPC (Swift `ContainerAPIClient`) or drive the `container` CLI?

## Answer
Drive the CLI. It is the documented, versioned public surface; `ls`, `inspect`, `network inspect`, `volume inspect`, `image ls` emit stable JSON. XPC would force Swift and couple the tool to the daemon's internal package API, which has been renamed between minor versions. Verified against 1.3.1: `run -d` prints the id, exit codes propagate from `run --rm`, `stop -t` honours the timeout, `logs -n/-f` exist, repeated `--network` attaches multiple networks, `-c` accepts only whole CPUs, IPs are allocated at start rather than create.
