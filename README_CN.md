# ActionD

**本地 AI 动作执行引擎 - 为 LGH（Local Git Hub）打造**

ActionD 是一个轻量级的本地 CI/CD 引擎，专为 AI Agent 设计。它监听来自 [LGH](https://github.com/JoeGlenn1213/lgh) 的 Git 事件，并自动触发插件操作，如代码检查、测试、构建和 AI 文档生成。

## 特性

- 🔌 **插件系统** - 可扩展架构，支持 Python/Node/Shell 插件
- 📡 **事件驱动** - 响应 `git.push`、`git.tag` 等 LGH 事件
- 🖥️ **Web 控制台** - 实时监控面板 `http://localhost:3000`
- 🔄 **实时日志** - 通过 SSE 实时推送日志输出
- 🛠️ **CLI 管理** - 完整的守护进程控制：`start`、`stop`、`status`、`doctor`
- ⚙️ **配置系统** - JSON 配置文件，支持启用/禁用插件和修改触发器

## 核心特性升级

### 结构化结果 (ActionResult)
系统内所有官方插件（包括 `go-test-fast`, `python-pytest`, `web-build` 等）均已升级至统一的 `ActionResult` 规范，并在 `signals` 中暴露细粒度特征。

### 人工审批机制
对于被 `approval-gate` 挂起的高风险操作，您可以通过 CLI 命令轻松放行：
```bash
actiond approve <job_id>
```

## 安装

### 前置要求

- Go 1.25+
- Python 3.8+（用于插件）
- 本地运行的 [LGH](https://github.com/JoeGlenn1213/lgh)

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

# 2. 后台启动 ActionD（默认自动读取 LGH mappings）
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

| 命令 | 说明 |
|------|------|
| `actiond setup` | 一键初始化环境 (v1.2+) |
| `actiond start` | 前台启动 |
| `actiond start -d` | 后台守护进程启动 |
| `actiond stop` | 停止守护进程 |
| `actiond restart` | 重启服务 (v1.2+) |
| `actiond status` | 查看运行状态 + 目录信息 (v1.2+) |
| `actiond log` | 查看服务器运行日志，支持 `--job` 和 `--plugin` 过滤 |
| `actiond wait <job_id>` | 阻塞等待特定任务完成，支持 `--timeout` 参数 |
| `actiond plugins restore-go` | 重新启用 Go 校验插件 |
| `actiond doctor` | 诊断系统依赖 (分级检查，提供目录缺失的修复建议) |
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

## 构建说明

`make build` 使用 `CGO_ENABLED=0`（SQLite 为 modernc.org/sqlite 纯 Go 实现），二进制可自由交叉编译。

`make release` 默认只构建当前主机平台；需要其他目标时覆盖 `RELEASE_PLATFORMS="linux/amd64 linux/arm64 darwin/arm64"`。

### 启动选项

```bash
./actiond start --help

选项:
  -d, --daemon              后台运行
      --repo-root string    仓库根目录（可选；未提供时优先使用 LGH mappings）
      --deepwiki-path       DeepWiki MCP 路径（可选）
      --web-dir string      Web 控制台静态文件路径（可选；默认自动探测）
```

## 配置文件

ActionD 支持通过 `~/.localgithub/actions/config.json` 进行运行时配置。

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

### 修改触发器

```json
{
  "plugins": {
    "go-lint": {
      "triggers": ["git.tag"]
    }
  }
}
```

修改配置后，重启 ActionD 生效：

```bash
./actiond stop && ./actiond start -d
```

如果只是要恢复核心 Go 校验链，也可以直接执行：

```bash
actiond plugins restore-go
```

## 内置插件

| 插件 | 触发事件 | 说明 |
|------|----------|------|
| `echo` | 所有事件 | 调试插件，输出事件信息 |
| `go-lint` | `git.push` | Go 代码检查 |
| `go-test-fast` | `git.push` | Go 快速单元测试 |
| `go-build` | `git.tag` | Go 二进制构建 |
| `java-quicktest` | `git.push` | 智能 Java 测试选择 |
| `deepwiki` | `git.tag` | AI 文档生成 |

## 创建自定义插件

插件是 Python 脚本，通过 stdin 接收事件数据，通过 stdout 输出结果。

### 插件目录结构

```
plugins/
└── my-plugin/
    └── run.py
```

### 示例插件

```python
#!/usr/bin/env python3
import json
import sys

# 从 stdin 读取输入
input_data = json.load(sys.stdin)
event = input_data["event"]
repo_path = input_data["repo_path"]

# 执行操作...
print(f"处理 {event['type']} 事件，仓库: {event['repo']}", file=sys.stderr)

# 输出结果
result = {
    "status": "success",
    "artifacts": []
}
print(json.dumps(result))
```

### 注册插件

在 `internal/app/app.go` 中添加：

```go
plugin.NewExecPlugin(plugin.ExecPluginConfig{
    Name:       "my-plugin",
    Command:    "python3",
    Args:       []string{filepath.Join(pluginsDir, "my-plugin/run.py")},
    Triggers:   []string{event.TypeGitPush},
    Timeout:    5 * time.Minute,
    WorkingDir: filepath.Join(pluginsDir, "my-plugin"),
}),
```

## 架构

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│    LGH      │────▶│   ActionD   │────▶│    插件     │
│   (事件)    │     │   (引擎)    │     │   (操作)    │
└─────────────┘     └─────────────┘     └─────────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │  Web 控制台  │
                    │   (仪表盘)   │
                    └─────────────┘
```

## 文件位置

| 路径 | 说明 |
|------|------|
| `~/.localgithub/actions/` | 数据目录 |
| `~/.localgithub/actions/actiond.db` | SQLite 任务数据库 |
| `~/.localgithub/actions/actiond.pid` | 守护进程 PID 文件 |
| `~/.localgithub/actions/config.json` | 用户配置文件 |
| `./actiond.log` | 守护进程日志 |

## 许可证

MIT License - 详见 [LICENSE](LICENSE)

## 相关项目

- [LGH](https://github.com/JoeGlenn1213/lgh) - 本地 Git Hub
- [actiond-web](https://github.com/JoeGlenn1213/actiond-web) - Web 控制台 UI
