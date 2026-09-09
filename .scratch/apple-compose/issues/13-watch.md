# 13 Compose watch mode
Type: prototype
Status: resolved

## Question
Compose's `develop.watch` keeps a running container in step with the source tree. Should apple-compose implement it, and how should changes be detected on macOS?

## Answer
Yes: `apple-compose watch` and `up --watch` apply every trigger action (`sync`, `rebuild`, `restart`, `sync+restart`, `sync+exec`). Changes are found by rescanning the watched trees on an interval (500 ms by default, `--interval` to change) rather than with fsnotify: kqueue, which fsnotify uses on macOS, charges a file descriptor per watched file and exhausts the limit on a large source tree, while a walk costs nothing but time and reports deletions for free. Sync copies each changed file with the runtime's `cp` and creates missing parent directories with an exec, and `--prune` removes files the host no longer has. `ignore` and `include` patterns match a whole relative path when they contain a slash and a single path segment otherwise, so `node_modules` excludes it at any depth; `.git` is always excluded. `up --watch` runs the services detached and watches in the foreground, leaving them running when the watch stops, as Compose does.
