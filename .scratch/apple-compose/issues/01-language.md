# 01 Language and compose parsing library
Type: research
Status: resolved

## Question
Go, Rust or Swift for apple-compose, given the need for maximum compose-file fidelity?

## Answer
Go. `github.com/compose-spec/compose-go/v2` (v2.15.0) is the reference loader used by Docker Compose itself: interpolation, `.env`, `extends`, `include`, profiles, multi-file merge, normalisation, dependency graph. No Rust or Swift equivalent exists; the Swift route (Mcrich23/Container-Compose) hand-writes a partial schema with Yams. Go also gives static binaries, goreleaser and Homebrew packaging for free.
