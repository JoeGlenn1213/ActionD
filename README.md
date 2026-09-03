# ActionD

**Local AI Action Execution Engine for LGH (Local Git Hub)**

[English](README.md) | [简体中文](README_CN.md)

ActionD is a lightweight local CI/CD engine designed for AI agents. It listens to Git events from [LGH](https://github.com/JoeGlenn1213/lgh) and automatically triggers plugins to run code checks, tests, builds, and more.

## Features

- 🔌 **Dynamic plugin discovery** — plugins are discovered automatically via `manifest.json`, no code changes required
- 🤖 **MCP integration** — a built-in MCP server lets AI assistants query and control CI/CD directly
- 📡 **Event-driven** — reacts to `git.push`, `git.tag`, and other LGH events
- 🖥️ **Web console** — live monitoring dashboard at `http://localhost:3000`
- 🔄 **Real-time streaming output** — live logs over SSE (Server-Sent Events)
- ⚡ **Hot reload** — reload plugins without a restart
- 🔄 **End-to-end workflow** — `dev_cycle_run` does it all in one call: commit → CI → results
- ⏮️ **Rollback** — automatically roll back to the previous commit on failure

## Installation

### macOS / Linux (Homebrew)

```bash
brew install JoeGlenn1213/tap/actiond
```

Install LGH the same way (`brew install JoeGlenn1213/tap/lgh`) — ActionD listens to its git events.

### Prerequisites

- Go 1.25+ (only when building from source)
- Python 3.8+ (used by plugins)
- [LGH](https://github.com/JoeGlenn1213/lgh) running locally

### Build from source

```bash
git clone https://github.com/JoeGlenn1213/ActionD.git
cd ActionD
make build
```

> Note: ActionD uses SQLite for storage (the pure-Go modernc.org/sqlite build) — no CGO is needed and `make build` cross-compiles freely.

## Quick Start

### 1. One-command setup (recommended)

If this is your first run, use `setup` to check dependencies, create the directory layout, and verify the environment:

```bash
actiond setup
```

To also start the daemon in the background right after setup:

```bash
actiond setup --start
```

### 2. Start the service and check it

```bash
# 1. Make sure LGH is running
lgh serve -d

# 2. Start ActionD (daemon mode; reads LGH mappings automatically)
actiond start -d

# 3. Check status
actiond doctor
```

### 3. Open the web console

```bash
open http://localhost:3000
```

### Default directories and auto-detection

- Plugin directories are probed in order:
  `<binary dir>/plugins` → `<working dir>/plugins` → `~/.localgithub/plugins`
- Web static assets are auto-detected in common locations; the recommended target is publishing `ActionD-Web`'s `out/` to:
  `~/.localgithub/actiond-web/out`
- Without an explicit `--repo-root`, ActionD resolves repositories through LGH mappings first, then falls back to current-directory semantics.

## CLI Commands

| Command | Description |
|------|------|
| `actiond setup` | One-command environment initialization (v1.2+) |
| `actiond start` | Start in the foreground |
| `actiond start -d` | Start as a background daemon |
| `actiond stop` | Stop the daemon |
| `actiond restart` | Restart the service (v1.2+) |
| `actiond status` | Show run status + directory info (v1.2+) |
| `actiond plugins restore-go` | Re-enable the Go verification plugins |
| `actiond log` | View server logs |
| `actiond doctor` | Diagnose system dependencies (tiered checks) |
| `actiond version` | Print version information |
| `actiond mcp` | Start the MCP server |

### One-command setup (v1.2+)

Recommended on first install:

```bash
actiond setup
```

This automatically:
- Creates the directory layout (`~/.localgithub/*`)
- Checks dependencies (Git, Python, Go, Node)
- Detects web assets and plugin directories
- Verifies the LGH connection

### Doctor

```bash
actiond doctor
```

Runs 8 categories of tiered checks:
- 📦 System environment (home/base directories)
- 🔧 Dependencies (Git, Python, Go, Node, golangci-lint)
- 🔌 Service status (LGH, ActionD)
- 🌐 Ports (3000, 8080)
- 📁 Directories (repos, actions, plugins, web, artifacts)
- 💾 Storage (DB writability, config files)
- 🔌 Plugins (directories, core plugin status)
- 🌐 Web assets

Results come in three levels:
- **FATAL** — the system cannot work
- **WARN** — some functionality is affected
- **INFO** — informational only

If `doctor` reports that the Go plugins are disabled, restore them directly:

```bash
actiond plugins restore-go
```

### Status (v1.2+)

```bash
actiond status
```

Shows:
- Service run status and PID
- All directory paths and their status
- LGH connection status
- Web assets and plugin directories

## Build Notes

`make build` uses `CGO_ENABLED=0` (SQLite is the pure-Go modernc.org/sqlite build), so binaries cross-compile freely.

`make release` defaults to the current host platform; override `RELEASE_PLATFORMS="linux/amd64 linux/arm64 darwin/arm64"` to build for other targets.

### Start options

```bash
actiond start --help

Flags:
  -d, --daemon              run in the background
      --repo-root string    repository root directory (optional; LGH mappings take priority when omitted)
      --web-dir string      web console static file directory (optional; auto-detected by default)
```

## Dynamic Plugin Discovery (V1.0.7+)

ActionD supports adding new plugins with **zero code**. Just create a `manifest.json` in a plugin directory:

### Plugin directories

ActionD scans plugins in this order:

1. **System plugins**: `./plugins/` (next to the binary)
2. **Development plugins**: `./plugins/` (current working directory)
3. **User plugins**: `~/.localgithub/plugins/`

### manifest.json format

```json
{
  "apiVersion": "actiond.dev/v1",
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "My custom plugin",
  "command": "python3",
  "args": ["run.py"],
  "triggers": ["git.push"],
  "languages": ["python"],
  "timeout": "5m",
  "artifacts": ["report.json"]
}
```

### Field reference

| Field | Required | Description |
|------|------|------|
| `name` | ✅ | Unique plugin identifier |
| `command` | ✅ | Command to execute |
| `args` | - | Command arguments |
| `triggers` | ✅ | Trigger events: `git.push`, `git.tag` |
| `languages` | - | Supported languages: `go`, `java`, `python`, `web`, `node`, `typescript`, `javascript`, `nextjs`, `*` |
| `timeout` | - | Timeout: `5m`, `30s` |
| `refFilter` | - | Ref matching: `refs/tags/*` |

### Creating a custom plugin

```
plugins/
└── my-plugin/
    ├── manifest.json    # plugin metadata
    └── run.py           # execution script
```

**Example run.py**:

```python
#!/usr/bin/env python3
import json
import sys

# Read stdin input
input_data = json.load(sys.stdin)
event = input_data["event"]
repo_path = input_data["repo_path"]
artifact_dir = input_data.get("artifact_dir")

# Do the work...
print(f"Processing {event['type']} for {repo_path}", file=sys.stderr)

# Output the result (stdout)
result = {
    "status": "success",  # or "error"
    "artifacts": ["report.json"]
}
print(json.dumps(result))
```

## Structured Result Protocol — ActionResult (v1.2+)

Plugins can return a standardized `ActionResult` structure that enables deep AI understanding and downstream decision gating (for example, the Policy Gate plugin reads signals produced by other plugins):

```json
{
  "action_id": "act_8a9b2c1d",
  "plugin_id": "go-test-fast",
  "capability": "test",
  "language": "go",
  "status": "success",
  "decision": "pass",
  "timing": {
    "started_at": "2025-03-16T10:30:00Z",
    "finished_at": "2025-03-16T10:30:02Z",
    "duration_ms": 2300
  },
  "summary": {
    "message": "All 25 tests passed",
    "counts": {
      "tests_run": 25
    }
  },
  "signals": {
    "tests_passed": true
  },
  "hints": [],
  "artifacts": [{"name": "test-report.xml", "path": "test-report.xml"}]
}
```

### Result fields

| Field | Type | Description |
|------|------|------|
| `action_id` | string | Unique execution ID |
| `status` | string | `success`, `failed`, `skipped` |
| `decision` | string | `pass`, `deny` (used for gating and AI decisions) |
| `summary.message` | string | One-line summary |
| `signals` | object | **Core extracted features**, e.g. `tests_passed`, `lint_error_count` |
| `hints` | []string | AI/user-friendly fix suggestions |
| `artifacts` | []object | Artifact file list |

### Returning structured results from a plugin

A plugin can print JSON to stdout — ActionD parses and stores it automatically:

```python
result = {
    "status": "failure",
    "summary": "Test suite failed",
    "hints": ["Run tests locally to reproduce"]
}
print(json.dumps(result))
```

Alternatively, write to `$ARTIFACT_DIR/result.json`.

## Failure Interpreter (v1.2+)

ActionD has built-in failure-pattern recognition that automatically analyzes common errors and suggests fixes:

### Recognized error patterns

| Category | Pattern | Description |
|------|------|------|
| **Dependencies** | `npm_install_failed` | npm install failure |
| | `npm_lockfile_mismatch` | package-lock.json out of sync |
| | `npm_module_not_found` | module not found |
| | `go_mod_tidy` | go.mod needs tidying |
| | `python_module_not_found` | missing Python module |
| | `maven_build_failed` | Maven build failure |
| **Build** | `go_build_failed` | Go compile error |
| | `gradle_build_failed` | Gradle build failure |
| **Tests** | `jest_test_failed` | Jest test failure |
| | `go_test_failed` | Go test failure |
| | `pytest_failed` | pytest failure |
| **Generic** | `permission_denied` | permission error |
| | `timeout` | operation timed out |
| | `out_of_memory` | out of memory |
| | `command_not_found` | command not found |

### Analysis API

Failure analysis is implemented on the Go side in `internal/interpreter` (`failure.go`) and produces the `category`/`type` classification shown above. **There is no `actiond` Python package** — use one of these real interfaces instead:

- **AI side (recommended)**: the MCP tool `actiond_diagnose(job_id=...)`, which returns root cause and fix suggestions.
- **HTTP side**: the REST API (see the "API endpoints" section below).

### Hot-reloading plugins

```bash
# Option 1: API
curl -X POST http://localhost:3000/api/plugins/reload

# Option 2: MCP
# An AI assistant can call the actiond_plugins_reload tool
```

## Built-in Plugins

| Plugin | Trigger | Language | Description |
|------|--------|------|------|
| `echo` | all | * | Debug plugin, echoes event info |
| `go-lint` | `git.push` | Go | golangci-lint code checks |
| `go-test-fast` | `git.push` | Go | Fast unit tests |
| `go-build` | `git.tag` | Go | Cross-platform builds |
| `java-quicktest` | `git.push` | Java | Smart test selection |
| `java-checkstyle` | `git.push` | Java | Checkstyle code style |
| `python-pytest` | `git.push` | Python | pytest + coverage |
| `web-lint` | `git.push` | Web/Node | Frontend lint checks |
| `web-test` | `git.push` | Web/Node | Frontend test script |
| `web-build` | `git.push` | Web/Node | Frontend build validation |

## MCP Server Integration

ActionD ships with an MCP (Model Context Protocol) server so AI assistants (such as Claude) can query and control CI/CD directly.

### Starting the MCP server

```bash
actiond mcp
```

To let the AI start/stop/restart ActionD itself over MCP, set this before launching:

```bash
ACTIOND_MCP_ALLOW_LIFECYCLE=1 actiond mcp
```

### Available tools

| Tool | Description |
|------|------|
| `actiond_status` | Get server status and statistics |
| `actiond_plugins_list` | List all plugins and their configuration |
| `actiond_actions_list` | List recent CI/CD jobs |
| `actiond_action_get` | Get details of a single job |
| `actiond_plugins_reload` | Hot-reload plugins |
| `actiond_plugins_recommend` | Recommend plugins by project profile (language/framework detection + confidence) |
| `actiond_plugin_enable` | Enable a plugin for the current project |
| `actiond_plugin_disable` | Disable a plugin for the current project |
| `actiond_log` | View server logs, filterable by job_id and plugin_name |
| `actiond_profile_get` | Get the current execution profile (fast/full/release) |
| `actiond_profile_set` | Set the execution profile, controlling which plugins each push triggers |
| `actiond_server_start` | Start the ActionD service (requires the lifecycle switch) |
| `actiond_server_stop` | Stop the ActionD service (protects running jobs by default) |
| `actiond_server_restart` | Restart the ActionD service (protects running jobs by default) |
| `actiond_job_wait` | Block until a job finishes and return its result; supports a timeout parameter |
| `actiond_job_cancel` | Cancel a job (validates state; terminal jobs are rejected) |
| `actiond_cancel` | Cancel a job (deprecated: prefer `actiond_job_cancel`) |
| `actiond_job_retry` | Retry a failed job |
| `actiond_diagnose` | **AI failure diagnosis**: root-cause analysis + classification + fix suggestions (the first tool to reach for when CI fails) |
| `dev_cycle_run` | **End-to-end dev loop**: commit → CI → results (V1.0.8+) |

> To approve a blocked job, use the CLI `actiond approve <job_id>` or REST `POST /api/actions/{id}/approve` (there is no MCP tool for this).

### The dev_cycle_run end-to-end workflow (V1.0.8+)

`dev_cycle_run` is an aggregate tool that completes the full development loop in a single MCP call:

```
edit code → lgh up → wait for CI → return structured results
```

**Parameters:**

| Parameter | Required | Description |
|------|------|------|
| `message` | ✅ | Git commit message |
| `path` | - | Repository path (defaults to the current directory) |
| `timeout` | - | Wait timeout in seconds (default 300 = 5 minutes) |
| `auto_rollback` | - | Auto-rollback on failure (default false) |

**Returns:**

```json
{
  "success": true,
  "commit": "abc123",
  "jobs": [
    {"id": "job-1", "plugin": "go-test-fast", "status": "done", "duration": "2.3s"}
  ],
  "summary": "✅ All passed (2 plugins)"
}
```

**Typical usage:**

```
User: AI, please fix the code and test it

AI:  [edits the code...]
     [calls dev_cycle_run(message="fix: address the failing case")]

Result: ✅ All passed (2 plugins)
        - go-lint: ✅ 0.5s
        - go-test-fast: ✅ 2.3s
```

### Available resources

- `actiond://status` — server status
- `actiond://plugins` — plugin list
- `actiond://actions` — execution records

### Configuring Claude Code

Add to `~/.claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "actiond": {
      "command": "/path/to/actiond",
      "args": ["mcp"],
      "env": {
        "ACTIOND_MCP_ALLOW_LIFECYCLE": "1"
      }
    }
  }
}
```

### AI usage example

```
User: Check the recent CI jobs

AI:  [calls actiond_actions_list]
     There are 3 recent jobs:
     - test-python (python-pytest): ✅ success (1.3s)
     - ActionD (go-lint): ⛔ disabled
     - demo-app (java-quicktest): ✅ success (45s)
```

## Configuration

Runtime config file: `~/.localgithub/actions/config.json`

### Disabling a plugin

```json
{
  "plugins": {
    "java-quicktest": {
      "enabled": false
    }
  }
}
```

The core Go verification chain can also be restored directly via CLI:

```bash
actiond plugins restore-go
```

### Overriding triggers

```json
{
  "plugins": {
    "go-lint": {
      "triggers": ["git.tag"]
    }
  }
}
```

### Adding a custom plugin (no manifest.json needed)

```json
{
  "plugins": {
    "my-custom-plugin": {
      "enabled": true,
      "type": "exec",
      "command": "/usr/local/bin/my-script",
      "args": ["--verbose"],
      "triggers": ["git.push"]
    }
  }
}
```

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│    LGH      │────▶│   ActionD   │────▶│   Plugins   │
│  (Events)   │     │  (Engine)   │     │  (Actions)  │
└─────────────┘     └─────────────┘     └─────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │ Web Console │  │  MCP Server │  │    API      │
   │ (Dashboard) │  │(AI-integration)│ │  (RESTful) │
   └─────────────┘  └─────────────┘  └─────────────┘
```

## API Endpoints

| Endpoint | Method | Description |
|------|------|------|
| `/api/plugins` | GET | List all plugins |
| `/api/plugins` | POST | Create a custom plugin |
| `/api/plugins/reload` | POST | Hot-reload plugins |
| `/api/plugins/{name}/toggle` | POST | Enable/disable a plugin |
| `/api/actions` | GET | List execution records |
| `/api/actions/{id}` | GET | Get job details |
| `/api/actions/{id}/stream` | GET | SSE live log stream |
| `/api/actions/{id}/artifacts/{file}` | GET | Download an artifact |
| `/api/actions/{id}/cancel` | POST | Cancel a running job (V1.0.8+) |
| `/api/actions/{id}/retry` | POST | Retry a failed job (V1.0.8+) |
| `/api/actions/{id}/approve` | POST | Manually approve a blocked job |

## Layered Logging (v1.2+)

ActionD uses a layered logging architecture that targets different audiences with different formats:

| Layer | Purpose | Example |
|------|------|------|
| `event` | Event log | `📨 Received: git.push [my-repo]` |
| `dispatch` | Dispatch log | `→ Dispatching to: go-lint` |
| `plugin` | Plugin execution | plugin stdout/stderr output |
| `user` | User summary | `✅ All 3 plugins passed (5.2s)` |
| `ai` | AI structured summary | JSON, consumed by AI |

### AI summary format

```json
{
  "timestamp": "2025-03-16T10:30:00Z",
  "layer": "ai",
  "level": "info",
  "job_id": "abc123",
  "repo": "my-project",
  "plugin": "go-test-fast",
  "message": "Tests passed",
  "data": {
    "status": "success",
    "summary": "All 25 tests passed in 2.3s",
    "hints": [],
    "artifacts": ["test-report.xml"]
  }
}
```

## File Locations

| Path | Description |
|------|------|
| `~/.localgithub/actions/` | Data directory |
| `~/.localgithub/actions/actiond.db` | SQLite job database |
| `~/.localgithub/actions/actiond.pid` | Daemon PID file |
| `~/.localgithub/actions/config.json` | User configuration |
| `~/.localgithub/plugins/` | User-defined plugin directory |
| `~/.localgithub/actions/actiond.log` | Daemon log |

## Development

```bash
# Build
go build ./...

# Run tests
go test ./...

# Install to GOPATH
go install ./cmd/actiond
```

## License

MIT License — see [LICENSE](LICENSE)

## Related Projects

- [LGH](https://github.com/JoeGlenn1213/lgh) — Local Git Hub
- [actiond-web](https://github.com/JoeGlenn1213/actiond-web) — Web console UI
