# 03 Service discovery between services
Type: prototype
Status: resolved

## Question
How do services reach each other by bare service name when the runtime cannot resolve bare names on custom networks (apple/container#1809)?

## Answer
Bind-mount a tool-managed hosts file over `/etc/hosts` in every service container (virtiofs single-file mounts work, verified on 1.3.1). The tool rewrites each container's file whenever an IP becomes known, and the container sees the change immediately without a shell, exec, or restart. Entries: `localhost`, the container's own hostname, every peer service name plus aliases and `container_name`, `extra_hosts`, and `host.docker.internal`/`gateway.docker.internal` pointing at the gateway. Daemon DNS via `[dns] domain` and `container system dns` was tested and did not resolve peers in any configuration without admin setup, so it is not used.
