# ActionD Plugin Specification

[English](#1-design-principles) · [简体中文](#规范摘要中文)

Runtime contract for ActionD plugins. **Source of truth:**

| Artifact | Role |
|----------|------|
| `manifest.json` per plugin directory | Discovery & exec config (`internal/plugin/manifest.go`) |
| `schemas/action-result.schema.json` | Preferred structured stdout |
| `schemas/event.schema.json` | Event shapes (see also [EVENT_MODEL.md](EVENT_MODEL.md)) |

There is **no** `plugin.yaml` loader. Optional catalog-style keys (`id`, `capability`, `owner_layer`, …) may appear in manifests for humans/UI; the Go parser only **requires** the fields listed below for registration.

---

## 1. Design principles

1. **Discover, don’t compile-in** — ship a directory with `manifest.json`; ActionD scans plugin roots at startup/reload.  
2. **Exec-first** — `command` + `args` spawn a process; stdin/stdout carry JSON.  
3. **Normalize outputs** — prefer `ActionResult` so Policy Gate and MCP/AI consumers share one shape.  
4. **Fail closed for gates** — decisions like `deny` / `rejected` force job failure even if `status` says success (see `exec_runner.go`).

---

## 2. Manifest (`manifest.json`)

Parsed by `ParseManifest` / `Validate` in `internal/plugin/manifest.go`.

### Required (validation)

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique plugin id |
| `command` | string | Executable (e.g. `python3`) |
| `triggers` | string[] | At least one event type, e.g. `git.push`, `git.tag` |

### Used by the runtime

| Field | Type | Description |
|-------|------|-------------|
| `apiVersion` | string | Convention: `actiond.dev/v1` |
| `version` | string | Plugin version (semver recommended) |
| `description` | string | Human-readable blurb |
| `args` | string[] | Arguments after `command` |
| `languages` | string[] | Language filters; empty → `["*"]` |
| `timeout` | string | Go duration, e.g. `5m`, `30s` (default 5m) |
| `workingDir` | string | CWD relative to the manifest directory (default: that directory) |
| `refFilter` | string | Glob for git refs, e.g. `refs/tags/*` |
| `repoFilter` | string | Glob for repository names |
| `supported_profiles` | string[] | `fast` / `full` / `release`; empty → all profiles |
| `env` | object | Extra environment for the process |
| `artifacts` | string[] | Declared artifact names (documentation / catalog) |

### Optional metadata (ignored by Go struct; safe to keep)

Common in-tree examples: `id`, `capability`, `owner_layer`, `maturity`, `status`, `inputs`, `outputs`. Useful for docs and future tooling; **not** what discovery validates today.

### Minimal example

```json
{
  "apiVersion": "actiond.dev/v1",
  "name": "python-ruff",
  "version": "1.0.0",
  "description": "Run ruff linter for Python projects",
  "command": "python3",
  "args": ["run.py"],
  "triggers": ["git.push"],
  "languages": ["python"],
  "timeout": "3m",
  "artifacts": ["lint-report.json"],
  "supported_profiles": ["fast", "full"]
}
```

---

## 3. Process I/O

### Stdin — `ExecInput`

```json
{
  "event": {
    "id": "uuid",
    "type": "git.push",
    "repo": "my-repo",
    "timestamp": "2025-12-27T21:48:00Z"
  },
  "repo_path": "/path/to/checkout",
  "artifact_dir": "/path/to/job/artifacts",
  "diff": "...",
  "files": ["a.go", "b.py"]
}
```

(`diff` / `files` may be omitted.)

### Stdout — legacy shape (still supported)

```json
{
  "status": "success",
  "error": "",
  "artifacts": ["report.json"],
  "decision": "pass",
  "summary": "one-line summary",
  "hints": [],
  "duration_ms": 1200
}
```

`status`: typically `success` or `error` (structured results also accept `failed` / `skipped` / `blocked` / `warning`).

### Stdout — ActionResult (preferred)

Conform to [`schemas/action-result.schema.json`](../schemas/action-result.schema.json):

- **status**: `success` | `failed` | `skipped` | `blocked` | `warning`  
- **decision**: `pass` | `soft-pass` | `needs-review` | `deny`  
- **summary.message**, optional **signals**, **hints**, **artifacts** (`{name, path, …}`)

Logs belong on **stderr**.

---

## 4. Lifecycle (exec)

1. **Discover** — walk plugin dirs for `manifest.json`; validate; build `ExecPlugin`.  
2. **Match** — event type ∈ `triggers`; language / ref / repo / profile filters.  
3. **Execute** — spawn `command` + `args` with stdin JSON; enforce `timeout`.  
4. **Normalize** — parse stdout JSON into job result / ActionResult.  
5. **Persist** — store status, logs, artifacts under `~/.localgithub/actions/` (SQLite + files).

---

## 5. Errors

- Catch failures in the plugin; return JSON with `status` failed/error and a clear message — do not rely on crashing.  
- Gate-style `decision` values that deny/reject force failure (fail-closed).  
- Non-zero process exit without valid JSON is treated as failure by the runner.

---

## 6. Config overrides

`~/.localgithub/actions/config.json` (or `ACTIOND_CONFIG_PATH`) can enable/disable plugins, override triggers, or define ad-hoc `type: "exec"` entries without a manifest. See README Configuration.

---

## 规范摘要（中文）

- **唯一契约**：各插件目录下的 `manifest.json`（不是 `plugin.yaml`）。  
- **必填**：`name`、`command`、`triggers`。  
- **推荐输出**：符合 `schemas/action-result.schema.json` 的 ActionResult；兼容旧版 `{status, artifacts}`。  
- **日志**：stderr；**结果**：stdout 单一 JSON。  
- 详细字段与示例以上英文章节为准，并与 `plugins/*/manifest.json`、`internal/plugin/` 保持一致。
