# ActionD

**Local AI Action Execution Engine for LGH (Local Git Hub)**

[English](README.md) | 简体中文

ActionD 是一个轻量级本地 CI/CD 引擎，专为 AI Agent 设计。它监听 [LGH](https://github.com/JoeGlenn1213/lgh) 的 Git 事件，自动触发插件执行代码检查、测试、构建等任务。

## 特性

- 🔌 **动态插件发现** - 通过 `manifest.json` 自动发现插件，无需修改代码
- 🤖 **MCP 集成** - 内置 MCP 服务器，让 AI 助手直接查询和控制 CI/CD
- 📡 **事件驱动** - 响应 `git.push`、`git.tag` 等 LGH 事件
- 🖥️ **Web 控制台** - 实时监控面板 `http://localhost:3000`
- 🔄 **实时流输出** - SSE (Server-Sent Events) 实时日志
- ⚡ **热加载** - 无需重启即可重载插件
- 🔄 **端到端工作流** - `dev_cycle_run` 一键完成：提交 → CI → 结果返回
- ⏮️ **可回滚** - 失败时自动回滚到上一个 commit

## 安装

### 前置要求

- Go 1.25+
- Python 3.8+ (用于插件)
- [LGH](https://github.com/JoeGlenn1213/lgh) 本地运行

### 从源码构建

```bash
git clone https://github.com/JoeGlenn1213/ActionD.git
cd ActionD
make build
```

> 说明：ActionD 使用 SQLite 存储（modernc.org/sqlite 纯 Go 实现），无需 CGO，`make build` 可直接跨平台编译。

## 快速开始

### 1. 一键初始化环境 (推荐)

如果你是第一次使用，我们推荐使用 `setup` 命令自动检查依赖、创建目录结构，并验证环境：

```bash
actiond setup
```
如果你希望在初始化完成后自动在后台启动，可以使用：
```bash
actiond setup --start
```

### 2. 启动服务与检查

```bash
# 1. 确保 LGH 正在运行
lgh serve -d

# 2. 启动 ActionD（后台模式，默认自动读取 LGH mappings）
actiond start -d

# 3. 检查状态
actiond doctor
```

### 3. 打开 Web 控制台

```bash
open http://localhost:3000
```

### 默认目录与自动探测

- 插件目录会按顺序自动探测：
  `二进制同目录/plugins` -> `当前目录/plugins` -> `~/.localgithub/plugins`
- Web 静态资源会按常见位置自动探测，推荐将 `ActionD-Web` 的 `out/` 发布到：
  `~/.localgithub/actiond-web/out`
- 未显式传入 `--repo-root` 时，ActionD 会优先使用 `LGH mappings` 解析仓库，再回退到当前目录语义

## CLI 命令

| 命令 | 描述 |
|------|------|
| `actiond setup` | 一键初始化环境 (v1.2+) |
| `actiond start` | 前台启动 |
| `actiond start -d` | 后台守护进程启动 |
| `actiond stop` | 停止守护进程 |
| `actiond restart` | 重启服务 (v1.2+) |
| `actiond status` | 检查运行状态 + 目录信息 (v1.2+) |
| `actiond plugins restore-go` | 重新启用 Go 校验插件 |
| `actiond log` | 查看服务器日志 |
| `actiond doctor` | 诊断系统依赖 (分级检查) |
| `actiond version` | 打印版本信息 |
| `actiond mcp` | 启动 MCP 服务器 |

### 一键初始化 (v1.2+)

首次安装推荐执行：

```bash
actiond setup
```

这会自动完成：
- 创建目录结构 (`~/.localgithub/*`)
- 检查依赖 (Git, Python, Go, Node)
- 检测 Web 资源和插件目录
- 验证 LGH 连接

### 诊断系统

```bash
actiond doctor
```

会进行 8 大类、分级检查：
- 📦 系统环境 (Home/Base 目录)
- 🔧 依赖检查 (Git, Python, Go, Node, golangci-lint)
- 🔌 服务状态 (LGH, ActionD)
- 🌐 端口检查 (3000, 8080)
- 📁 目录检查 (repos, actions, plugins, web, artifacts)
- 💾 存储检查 (DB 可写性, 配置文件)
- 🔌 插件检查 (目录, 核心插件状态)
- 🌐 Web 资源检查

检查结果分为三级：
- **FATAL** - 系统无法工作
- **WARN** - 部分功能受影响
- **INFO** - 仅供参考

如果 `doctor` 提示 Go 插件被禁用，可直接恢复：

```bash
actiond plugins restore-go
```

### 状态查看 (v1.2+)

```bash
actiond status
```

显示：
- 服务运行状态和 PID
- 所有目录路径及状态
- LGH 连接状态
- Web 资源和插件目录

## Build Notes

`make build` uses `CGO_ENABLED=0` (SQLite is the pure-Go modernc.org/sqlite build), so binaries cross-compile freely.

`make release` defaults to the current host platform; override `RELEASE_PLATFORMS="linux/amd64 linux/arm64 darwin/arm64"` to build for other targets.

### 启动选项

```bash
actiond start --help

Flags:
  -d, --daemon              后台运行
      --repo-root string    仓库根目录（可选；未提供时优先使用 LGH mappings）
      --deepwiki-path       DeepWiki MCP 目录路径（可选）
      --web-dir string      Web 控制台静态文件目录（可选；默认自动探测）
```

## 动态插件发现 (V1.0.7+)

ActionD 支持**零代码**添加新插件。只需在插件目录创建 `manifest.json` 文件：

### 插件目录

ActionD 按以下顺序扫描插件：

1. **系统插件**: `./plugins/` (与二进制文件同目录)
2. **开发插件**: `./plugins/` (当前工作目录)
3. **用户插件**: `~/.localgithub/plugins/`

### manifest.json 格式

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

### 字段说明

| 字段 | 必填 | 描述 |
|------|------|------|
| `name` | ✅ | 插件唯一标识 |
| `command` | ✅ | 执行命令 |
| `args` | - | 命令参数 |
| `triggers` | ✅ | 触发事件: `git.push`, `git.tag` |
| `languages` | - | 支持的语言: `go`, `java`, `python`, `web`, `node`, `typescript`, `javascript`, `nextjs`, `*` |
| `timeout` | - | 超时时间: `5m`, `30s` |
| `refFilter` | - | Ref 匹配: `refs/tags/*` |

### 创建自定义插件

```
plugins/
└── my-plugin/
    ├── manifest.json    # 插件元数据
    └── run.py           # 执行脚本
```

**run.py 示例**:

```python
#!/usr/bin/env python3
import json
import sys

# 读取 stdin 输入
input_data = json.load(sys.stdin)
event = input_data["event"]
repo_path = input_data["repo_path"]
artifact_dir = input_data.get("artifact_dir")

# 执行任务...
print(f"Processing {event['type']} for {repo_path}", file=sys.stderr)

# 输出结果 (stdout)
result = {
    "status": "success",  # 或 "error"
    "artifacts": ["report.json"]
}
print(json.dumps(result))
```

## 结构化结果协议 ActionResult (v1.2+)

ActionD 支持插件返回标准化的 `ActionResult` 结构，便于 AI 深度理解和后续决策拦截（例如 Policy Gate 插件会直接读取其他插件产生的 signals）：

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

### 结果字段

| 字段 | 类型 | 描述 |
|------|------|------|
| `action_id` | string | 唯一执行 ID |
| `status` | string | `success`, `failed`, `skipped` |
| `decision` | string | `pass`, `deny` (用于门禁和 AI 决策) |
| `summary.message` | string | 一句话摘要 |
| `signals` | object | **核心特征提取**，如 `tests_passed`, `lint_error_count` 等 |
| `hints` | []string | AI/用户友好的修复建议 |
| `artifacts` | []object | 产物文件列表 |

### 插件输出结构化结果

插件可以在 stdout 输出 JSON，ActionD 会自动解析并存储：

```python
result = {
    "status": "failure",
    "summary": "Test suite failed",
    "hints": ["Run tests locally to reproduce"]
}
print(json.dumps(result))
```

或者写入 `$ARTIFACT_DIR/result.json` 文件。

## 失败解释器 (v1.2+)

ActionD 内置失败模式识别，自动分析常见错误并提供修复建议：

### 支持的错误模式

| 类别 | 模式 | 描述 |
|------|------|------|
| **依赖** | `npm_install_failed` | npm 安装失败 |
| | `npm_lockfile_mismatch` | package-lock.json 不同步 |
| | `npm_module_not_found` | 模块未找到 |
| | `go_mod_tidy` | go.mod 需要整理 |
| | `python_module_not_found` | Python 模块缺失 |
| | `maven_build_failed` | Maven 构建失败 |
| **构建** | `go_build_failed` | Go 编译错误 |
| | `gradle_build_failed` | Gradle 构建失败 |
| **测试** | `jest_test_failed` | Jest 测试失败 |
| | `go_test_failed` | Go 测试失败 |
| | `pytest_failed` | pytest 失败 |
| **通用** | `permission_denied` | 权限错误 |
| | `timeout` | 操作超时 |
| | `out_of_memory` | 内存不足 |
| | `command_not_found` | 命令未找到 |

### 分析 API

失败分析由 Go 端 `internal/interpreter`（`failure.go`）实现，产出上表所列的 `category`/`type` 分类。**仓库内不存在 `actiond` Python 包**——调用方请使用以下真实方式之一：

- **AI 侧（推荐）**：MCP 工具 `actiond_diagnose(job_id=...)`，返回根因与修复建议。
- **HTTP 侧**：REST API（见下文「API 端点」小节）。

### 热加载插件

```bash
# 方式 1: API
curl -X POST http://localhost:3000/api/plugins/reload

# 方式 2: MCP
# AI 助手可以调用 actiond_plugins_reload 工具
```

## 内置插件

| 插件 | 触发器 | 语言 | 描述 |
|------|--------|------|------|
| `echo` | 全部 | * | 调试插件，回显事件信息 |
| `go-lint` | `git.push` | Go | golangci-lint 代码检查 |
| `go-test-fast` | `git.push` | Go | 快速单元测试 |
| `go-build` | `git.tag` | Go | 跨平台构建 |
| `java-quicktest` | `git.push` | Java | 智能测试选择 |
| `java-checkstyle` | `git.push` | Java | Checkstyle 代码风格 |
| `python-pytest` | `git.push` | Python | pytest + coverage |
| `web-lint` | `git.push` | Web/Node | 前端 lint 检查 |
| `web-test` | `git.push` | Web/Node | 前端 test 脚本 |
| `web-build` | `git.push` | Web/Node | 前端 build 校验 |

## MCP 服务器集成

ActionD 内置 MCP (Model Context Protocol) 服务器，让 AI 助手（如 Claude）可以直接查询和控制 CI/CD。

### 启动 MCP 服务器

```bash
actiond mcp
```

如果你希望 AI 通过 MCP 控制 ActionD 启停（`start/stop/restart`），启动前设置：

```bash
ACTIOND_MCP_ALLOW_LIFECYCLE=1 actiond mcp
```

### 可用工具

| 工具 | 描述 |
|------|------|
| `actiond_status` | 获取服务器状态和统计 |
| `actiond_plugins_list` | 列出所有插件及配置 |
| `actiond_actions_list` | 列出最近执行的 CI/CD 任务 |
| `actiond_action_get` | 获取单个任务详情 |
| `actiond_plugins_reload` | 热加载插件 |
| `actiond_plugins_recommend` | 按项目特征智能推荐插件（语言/框架检测 + 置信度） |
| `actiond_plugin_enable` | 为当前项目启用插件 |
| `actiond_plugin_disable` | 为当前项目禁用插件 |
| `actiond_log` | 查看服务器运行日志，支持按 job_id 和 plugin_name 过滤 |
| `actiond_profile_get` | 获取当前执行 profile（fast/full/release） |
| `actiond_profile_set` | 设置执行 profile，控制每次 push 触发哪些插件 |
| `actiond_server_start` | 启动 ActionD 服务（需开启生命周期开关） |
| `actiond_server_stop` | 停止 ActionD 服务（默认保护运行中任务） |
| `actiond_server_restart` | 重启 ActionD 服务（默认保护运行中任务） |
| `actiond_job_wait` | 阻塞等待指定任务完成并返回结果，支持 timeout 参数 |
| `actiond_job_cancel` | 取消任务（校验状态，终态任务会被拒绝） |
| `actiond_cancel` | 取消任务（Deprecated：优先用 `actiond_job_cancel`） |
| `actiond_job_retry` | 重试失败的任务 |
| `actiond_diagnose` | **AI 失败诊断**：根因分析 + 分类 + 修复建议（CI 失败时的首选工具） |
| `dev_cycle_run` | **端到端开发循环**：提交 → CI → 结果（V1.0.8+） |

> 审批卡住的任务用 CLI `actiond approve <job_id>` 或 REST `POST /api/actions/{id}/approve`（无 MCP 工具）。

### dev_cycle_run 端到端工作流 (V1.0.8+)

`dev_cycle_run` 是一个聚合工具，单个 MCP 调用完成完整开发循环：

```
改代码 → lgh up → 等待 CI → 返回结构化结果
```

**参数：**

| 参数 | 必填 | 描述 |
|------|------|------|
| `message` | ✅ | Git 提交信息 |
| `path` | - | 仓库路径（默认当前目录） |
| `timeout` | - | 等待超时秒数（默认 300 = 5分钟） |
| `auto_rollback` | - | 失败时自动回滚（默认 false） |

**返回：**

```json
{
  "success": true,
  "commit": "abc123",
  "jobs": [
    {"id": "job-1", "plugin": "go-test-fast", "status": "done", "duration": "2.3s"}
  ],
  "summary": "✅ 全部通过 (2 个插件)"
}
```

**使用场景：**

```
用户: AI，帮我修改代码并测试

AI: [修改代码...]
    [调用 dev_cycle_run(message="fix: 修复XX问题")]

结果: ✅ 全部通过 (2 个插件)
      - go-lint: ✅ 0.5s
      - go-test-fast: ✅ 2.3s
```

### 可用资源

- `actiond://status` - 服务器状态
- `actiond://plugins` - 插件列表
- `actiond://actions` - 执行记录

### 配置 Claude Code

在 `~/.claude/claude_desktop_config.json` 添加：

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

### AI 使用示例

```
用户: 检查最近的 CI 任务状态

AI: [调用 actiond_actions_list]
最近有 3 个任务:
- test-python (python-pytest): ✅ 成功 (1.3s)
- ActionD (go-lint): ⛔ 已禁用
- demo-app (java-quicktest): ✅ 成功 (45s)
```

## 配置

运行时配置文件: `~/.localgithub/actions/config.json`

### 禁用插件

```json
{
  "plugins": {
    "java-quicktest": {
      "enabled": false
    }
  }
}
```

也可以通过 CLI 直接恢复核心 Go 校验链：

```bash
actiond plugins restore-go
```

### 覆盖触发器

```json
{
  "plugins": {
    "go-lint": {
      "triggers": ["git.tag"]
    }
  }
}
```

### 添加自定义插件（无需 manifest.json）

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

## 架构

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
   │ (Dashboard) │  │  (AI 集成)  │  │  (RESTful)  │
   └─────────────┘  └─────────────┘  └─────────────┘
```

## API 端点

| 端点 | 方法 | 描述 |
|------|------|------|
| `/api/plugins` | GET | 列出所有插件 |
| `/api/plugins` | POST | 创建自定义插件 |
| `/api/plugins/reload` | POST | 热加载插件 |
| `/api/plugins/{name}/toggle` | POST | 启用/禁用插件 |
| `/api/actions` | GET | 列出执行记录 |
| `/api/actions/{id}` | GET | 获取任务详情 |
| `/api/actions/{id}/stream` | GET | SSE 实时日志流 |
| `/api/actions/{id}/artifacts/{file}` | GET | 下载产物 |
| `/api/actions/{id}/cancel` | POST | 取消运行中的任务 (V1.0.8+) |
| `/api/actions/{id}/retry` | POST | 重试失败的任务 (V1.0.8+) |
| `/api/actions/{id}/approve` | POST | 人工审批并放行被阻塞的任务 |

## 日志分层 (v1.2+)

ActionD 采用分层日志架构，针对不同受众输出不同格式：

| 层级 | 用途 | 示例 |
|------|------|------|
| `event` | 事件日志 | `📨 Received: git.push [my-repo]` |
| `dispatch` | 调度日志 | `→ Dispatching to: go-lint` |
| `plugin` | 插件执行 | 插件 stdout/stderr 输出 |
| `user` | 用户摘要 | `✅ All 3 plugins passed (5.2s)` |
| `ai` | AI 结构化摘要 | JSON 格式，供 AI 消费 |

### AI 摘要格式

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

## 文件位置

| 路径 | 描述 |
|------|------|
| `~/.localgithub/actions/` | 数据目录 |
| `~/.localgithub/actions/actiond.db` | SQLite 任务数据库 |
| `~/.localgithub/actions/actiond.pid` | 守护进程 PID 文件 |
| `~/.localgithub/actions/config.json` | 用户配置 |
| `~/.localgithub/plugins/` | 用户自定义插件目录 |
| `~/.localgithub/actions/actiond.log` | 守护进程日志 |

## 开发

```bash
# 编译
go build ./...

# 运行测试
go test ./...

# 安装到 GOPATH
go install ./cmd/actiond
```

## 许可证

MIT License - 详见 [LICENSE](LICENSE)

## 相关项目

- [LGH](https://github.com/JoeGlenn1213/lgh) - Local Git Hub
- [actiond-web](https://github.com/JoeGlenn1213/actiond-web) - Web 控制台 UI
