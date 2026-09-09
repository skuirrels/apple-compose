# 03 Service discovery between services
Type: prototype
Status: resolved

## Question
How do services reach each other by bare service name when the runtime cannot resolve bare names on custom networks (apple/container#1809)?

## Answer
Bind-mount a tool-managed hosts file over `/etc/hosts` in every service container (virtiofs single-file mounts work, verified on 1.3.1). The tool rewrites each container's file whenever an IP becomes known, and the container sees the change immediately without a shell, exec, or restart. Entries: `localhost`, the container's own hostname, every peer service name plus aliases and `container_name`, `extra_hosts`, and `host.docker.internal`/`gateway.docker.internal` pointing at the gateway. Daemon DNS via `[dns] domain` and `container system dns` was tested and did not resolve peers in any configuration without admin setup, so it is not used.

## Comments
- 2026-09-09: a service that resolves a peer in its first milliseconds (nginx upstreams, apps dialling their database) raced the post-start refresh. The hosts file is now filled with running peers between `create` and `start`, matching peers on the networks the container was created with, and the container's own name maps to 127.0.1.1 until its address is known. Verified with an nginx `proxy_pass` upstream and a python client dialling postgres and redis at boot.
