# 05 Plugin packaging and distribution
Type: research
Status: resolved

## Question
Standalone binary, `container` plugin, or both?

## Answer
Both from one binary. CLI plugins are language-agnostic: `<install-root>/libexec/container-plugins/<name>/bin/<name>` plus `config.toml` with an `abstract`; the CLI dispatches `container <name> ...` to it and lists it under PLUGINS. `apple-compose plugin install` copies itself there as `compose`, so `container compose up` works. Distribution via GitHub releases (goreleaser) and a Homebrew tap.
