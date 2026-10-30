# ActionD Plugin Guide

[English](#overview) · [简体中文](#概述)

How to add plugins to ActionD. The runtime contract is **`manifest.json` + dynamic discovery** — you do **not** edit `cmd/actiond/main.go` to register a plugin.

Canonical code: `internal/plugin/manifest.go`, `internal/plugin/discovery.go`, `internal/app/runtime_paths.go`.  
Schema for structured stdout: [`schemas/action-result.schema.json`](../schemas/action-result.schema.json).  
Also see the README section [Dynamic Plugin Discovery](../README.md#dynamic-plugin-discovery-v107).

---

## Overview

| Kind | How it runs | When to use |
|------|-------------|-------------|
| **Exec plugin (recommended)** | Any language via `command` + `args` in `manifest.json` | Lint, test, build, AI tools — almost everything in `plugins/` |
| **Built-in Go plugin** | Compiled into the binary (`internal/` / `plugins/echo`) | Core/test helpers only; **not** the path for third-party or fork plugins |

New checks: drop a directory under a scanned plugin root with `manifest.json` (+ script). Reload with `actiond_plugins_reload` / the web console, or restart ActionD. **No Go rebuild.**

---

## Plugin directories

ActionD scans existing directories in this order (`DetectPluginDirs`):

1. `<binary-dir>/plugins` — next to the `actiond` executable  
2. `<cwd>/plugins` — current working directory (source checkout)  
3. `~/.localgithub/actiond/plugins`  
4. `~/.localgithub/plugins` — user / `actiond setup` materialization target  

First discovery wins for a given plugin `name`; later duplicates are skipped.

`actiond setup` installs the embedded builtin bundle into `~/.localgithub/plugins`.

---

## Quick start (exec + manifest)

### 1. Create the plugin directory

```
plugins/my-plugin/
├── manifest.json
└── run.py
```

### 2. `manifest.json`

Required for discovery/validation: `name`, `command`, `triggers` (non-empty).

```json
{
  "apiVersion": "actiond.dev/v1",
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "Example plugin",
  "command": "python3",
  "args": ["run.py"],
  "triggers": ["git.push"],
  "languages": ["*"],
  "timeout": "5m",
  "artifacts": ["output.md"]
}
```

See [PLUGIN_SPEC.md](PLUGIN_SPEC.md) for the full field reference.

### 3. `run.py` (stdin JSON → stdout JSON)

```python
#!/usr/bin/env python3
import json
import os
import sys

def main():
    inp = json.load(sys.stdin)
    event = inp.get("event", {})
    repo_path = inp.get("repo_path", "")
    artifact_dir = inp.get("artifact_dir", "")

    print(f"[my-plugin] {event.get('type')} @ {repo_path}", file=sys.stderr)

    os.makedirs(artifact_dir, exist_ok=True)
    out_path = os.path.join(artifact_dir, "output.md")
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(f"# My Plugin\n\nRepo: {repo_path}\n")

    # Minimal legacy shape (still supported):
    print(json.dumps({"status": "success", "artifacts": ["output.md"]}))
    # Prefer ActionResult when you need Policy Gate / AI hints — see PLUGIN_SPEC.md

if __name__ == "__main__":
    main()
```

### 4. Verify

```bash
# From a source tree with ./plugins/my-plugin/
actiond plugins   # or start ActionD and check the web console / MCP plugin list
```

Hot-reload after edits: MCP `actiond_plugins_reload`, or restart the daemon. You do **not** need `ACTIOND_MCP_ALLOW_LIFECYCLE` for reload-only.

---

## Config override (no manifest)

You can also declare an exec plugin in `~/.localgithub/actions/config.json` (see README Configuration). Prefer `manifest.json` for shareable plugins.

---

## Built-in Go plugins (maintainers only)

The in-tree `plugins/echo` package implements `plugin.Plugin` and is wired in `loadPlugins` as a built-in. Interfaces live under `internal/plugin` — **external modules cannot import them**. Do not document `import .../internal/plugin` as a public extension API. For new work, use exec + `manifest.json`.

---

## Example plugins in this repo

| Plugin | Path | Notes |
|--------|------|--------|
| python-ruff | `plugins/python-ruff/` | Typical Python exec + manifest |
| go-lint | `plugins/go-lint/` | Go lint via Python runner |
| env-check | `plugins/env-check/` | Language-agnostic (`languages: ["*"]`) |
| echo | `plugins/echo/` | Built-in Go smoke plugin (no `manifest.json`) |

---

## Best practices

1. **Idempotent** — same event → same outcome  
2. **Timeouts** — set `timeout` in the manifest  
3. **Logs on stderr** — stdout must be JSON only  
4. **Artifacts** — write under `artifact_dir`; list names in the result  
5. **ActionResult** — use structured status/decision/signals when gating matters  

---

## 概述

ActionD 通过 **`manifest.json` 动态发现**加载插件，**不要**去改 `cmd/actiond/main.go` 注册。推荐用任意语言的 Exec 插件；内置 Go 插件仅供核心维护，且 `internal/plugin` 不可被仓外合法 import。

### 快速步骤

1. 在扫描目录下新建 `plugins/my-plugin/{manifest.json,run.py}`  
2. `manifest` 至少填 `name` / `command` / `triggers`  
3. 脚本从 stdin 读 JSON，向 stdout 打 JSON；日志走 stderr  
4. `actiond setup` 会把内置插件落到 `~/.localgithub/plugins`  

完整字段与 `ActionResult` 契约见 [PLUGIN_SPEC.md](PLUGIN_SPEC.md)；英文 README 的 [Dynamic Plugin Discovery](../README.md#dynamic-plugin-discovery-v107) 与本文一致。
