# ActionD — Agent Guide

Guidance for AI coding agents (and humans in a hurry) working in this repo.

## Project in one line

ActionD is a local CI/CD engine for AI agents: it listens to [LGH](https://github.com/JoeGlenn1213/lgh) git events and runs plugin jobs (lint, test, build, ...) with results exposed via CLI, REST, SSE, and MCP.

## Dev Quickstart

- **Language**: Go 1.25 (`go.mod`, `CGO_ENABLED=0` — SQLite is pure-Go modernc.org/sqlite). Plugins are Python 3.8+.
- **Entry**: `cmd/actiond/main.go` (cobra subcommands: `setup`/`start`/`stop`/`status`/`restart`/`doctor`/`mcp`/`approve`/`wait`/`plugins`/`version`/`log`).
- **Build**: `make build` (artifact `dist/actiond`).
- **Test**: `make test` (`go test ./... -v`).
- **Lint**: `make lint` (`golangci-lint run ./...`).
- **Package map** (`internal/`): `app` orchestration core; `dispatcher`/`worker`/`queue`/`pubsub` scheduling and execution; `plugin` plugin system; `interpreter` failure interpreter; `mcp` MCP server; `server`/`api` REST and web; `store` (SQLite) / `event` / `job` / `log` / `metrics` / `artifact` / `repopath`.
- **Runtime prerequisites (important)**:
  - Requires the **LGH daemon running** (`lgh serve -d`); repo paths are resolved through it. Without LGH, `start`/`dev_cycle_run` will not work.
  - Runtime state lives outside the repo: `~/.localgithub/{actions,plugins,actiond-web}/` (SQLite, pid, log, config). Debug "why didn't my job run" there.
  - `actiond setup` initializes the directory layout; `actiond doctor` runs health checks.
- **Version**: canonical version lives in `internal/version/version.go` (the Makefile reads it from there).
- **LGH endpoint**: defaults to `http://localhost:9418`; override with `ACTIOND_LGH_URL` when LGH runs elsewhere.

## Conventions

- Keep ActionD a reusable verification and execution backbone: prefer reproducible jobs, explicit status transitions, and traceable outputs.
- Avoid ad-hoc scripts when an ActionD flow or plugin should own the behavior.
- New plugin? See any `plugins/*/manifest.json` — plugins are discovered dynamically, no code changes needed.
