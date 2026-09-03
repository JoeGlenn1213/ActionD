# ActionD Plugin 开发指南

> 让你的工具融入本地 AI 工作流

---

## 概述

ActionD 支持两种插件类型：

| 类型 | 语言 | 适用场景 |
|------|------|---------|
| **Go 插件** | Go | 高性能、内嵌逻辑 |
| **Exec 插件** | 任意 (Python/Node/Shell) | AI 集成、外部服务 |

---

## 快速开始

### 方式 A: Exec 插件 (推荐)

用任何语言编写脚本，ActionD 通过 stdin/stdout 通信。

#### 1. 创建脚本

```python
#!/usr/bin/env python3
# plugins/my-plugin/my_plugin.py

import json
import sys
import os

def main():
    # 1. 读取输入
    input_data = json.load(sys.stdin)
    event = input_data.get('event', {})
    repo_path = input_data.get('repo_path', '')
    artifact_dir = input_data.get('artifact_dir', '')
    
    # 2. 你的逻辑
    print(f"[my-plugin] Processing {event.get('type')}", file=sys.stderr)
    
    # 3. 写入 artifacts
    os.makedirs(artifact_dir, exist_ok=True)
    with open(f"{artifact_dir}/output.md", 'w') as f:
        f.write(f"# My Plugin Output\n\nRepo: {repo_path}\n")
    
    # 4. 返回结果
    print(json.dumps({
        "status": "success",
        "artifacts": ["output.md"],
        "model": "gpt-4",  # 可选
        "tokens": 1000     # 可选
    }))

if __name__ == '__main__':
    main()
```

#### 2. 注册插件

在 `cmd/actiond/main.go` 中添加：

```go
plugin.NewExecPlugin(plugin.ExecPluginConfig{
    Name:       "my-plugin",
    Command:    "python3",
    Args:       []string{"/path/to/my_plugin.py"},
    Triggers:   []string{"git.push"},  // 事件类型
    Timeout:    5 * time.Minute,
    WorkingDir: "/path/to/plugin/dir",
})
```

---

### 方式 B: Go 插件

实现 `plugin.Plugin` 接口：

```go
// plugins/my-plugin/my_plugin.go
package myplugin

import (
    "github.com/JoeGlenn1213/actiond/internal/event"
    "github.com/JoeGlenn1213/actiond/internal/plugin"
)

type MyPlugin struct{}

func (p *MyPlugin) Name() string {
    return "my-plugin"
}

func (p *MyPlugin) Triggers() []string {
    return []string{event.TypeGitPush}
}

func (p *MyPlugin) Match(e event.Event) bool {
    // 过滤条件，如: 只处理 main 分支
    return true
}

func (p *MyPlugin) Run(ctx plugin.Context) error {
    // 1. 读取事件
    evt := ctx.Event
    
    // 2. 你的逻辑
    
    // 3. 写入 artifacts
    if ctx.Artifacts != nil {
        ctx.Artifacts.WriteJSON("result.json", map[string]string{
            "repo": evt.Repo,
        })
    }
    
    return nil
}
```

---

## 输入/输出协议

### Exec 插件 stdin 输入

```json
{
  "event": {
    "id": "uuid",
    "type": "git.push",
    "repo": "ActionD",
    "timestamp": "2025-12-27T21:48:00Z",
    "_replayed": false
  },
  "repo_path": "/path/to/repo",
  "artifact_dir": "~/.localgithub/actions/xxx/",
  "diff": "...",
  "files": ["file1.go", "file2.py"]
}
```

### Exec 插件 stdout 输出

```json
{
  "status": "success",       // 或 "error"
  "error": "...",            // 仅 error 时
  "artifacts": ["doc.md"],   // 创建的文件列表
  "model": "glm-4.6",        // AI 模型 (可选)
  "tokens": 12345,           // Token 消耗 (可选)
  "duration_ms": 5000        // 执行时间 (可选)
}
```

---

## 事件类型

| 事件 | 触发时机 |
|------|---------|
| `repo.added` | 仓库添加到 LGH |
| `repo.removed` | 仓库从 LGH 移除 |
| `git.push` | 代码推送 |

---

## ArtifactWriter API

Go 插件可使用 `ctx.Artifacts`:

```go
// 写入原始字节
ctx.Artifacts.Write("file.txt", []byte("content"))

// 写入 JSON
ctx.Artifacts.WriteJSON("data.json", myStruct)

// 获取输出目录
dir := ctx.Artifacts.Dir()
```

---

## 最佳实践

1. **幂等性**: 同一事件多次执行应产生相同结果
2. **超时处理**: Exec 插件设置合理 Timeout
3. **错误日志**: 用 `stderr` 输出日志, `stdout` 仅返回 JSON
4. **增量处理**: 利用 `event.Old` / `event.New` 只处理变更
5. **Artifact 命名**: 使用有意义的文件名 (如 `architecture.md`)

---

## 示例插件

| 插件 | 路径 | 说明 |
|------|------|------|
| Echo | `plugins/echo/` | 最简单示例 (Go) |
| DeepWiki | `deepwiki-mcp/scripts/actiond_adapter.py` | AI 代码分析 (Python) |

---

## 下一步

- [ ] 更多插件: Code Review, Security Scan, Architecture Diagram
- [ ] ActionD Web UI: 可视化 Action 历史和 Artifacts
- [ ] sqlite 持久化: ActionJob 状态管理
