# 07 Exit codes of detached containers
Type: prototype
Status: resolved

## Question
`container inspect` never reports an exit status, yet `depends_on: service_completed_successfully`, `up --exit-code-from`, `wait` and `ps` need one. Where does it come from?

## Answer
`container start --attach` returns the init process's exit code and separates stdout from stderr, with no progress noise. apple-compose attaches whenever it needs the code: always in foreground `up`, and in detached `up` for any service another service waits on with `service_completed_successfully`. Codes are written to `<state>/projects/<project>/exit/<container>` so later `ps`, `wait` and dependency checks can read them. Attached children run in their own process group so Ctrl-C reaches apple-compose alone, which then stops the project gracefully. `container logs --follow` never returns on its own, so followers are cancelled by a state poller.
