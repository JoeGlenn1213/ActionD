# Actiond Event Model V1

本文档定义了 Actiond V1 系统的标准事件模型。所有的流水线触发和插件输入都必须依赖于这个统一的事件上下文对象。

## 1. 事件触发类型 (Event Types)

Actiond 响应以下几类核心标准事件：

- `git.push`: 代码推送到远程仓库
- `git.tag`: 创建并推送新的 Tag
- `pull_request.opened`: PR 创建
- `pull_request.updated`: PR 更新（有新 commit）
- `manual.run`: 用户手动触发执行
- `schedule.nightly`: 定时任务触发（如夜间巡检）
- `approval.resume`: 人工审批通过后恢复被挂起的流水线

## 2. 标准输入上下文 (Event Context)

无论事件来源（Webhook / CLI / UI），进入编排引擎和插件时，必须被组装为统一的上下文结构。

### 2.1 结构示例

```yaml
event_id: evt_a1b2c3d4e5
event_type: git.push
timestamp: "2026-03-24T10:00:00Z"
actor: alice

# 仓库与版本信息
repository:
  name: demo-api
  owner: example-org
  url: https://github.com/example-org/demo-api.git

git_ref:
  branch: feature/new-plugin
  tag: null
  commit_sha: 8f2a3b1c

# 工程特征上下文 (由前置理解层/LGH注入)
workspace_context:
  target_languages:
    - python
  target_modules:
    - runtime-core
  changed_files:
    - runtime-core/main.py
    - runtime-core/tests/test_main.py

# 执行意图
execution_intent:
  profile: fast
  trigger_source: webhook
```

## 3. 数据流转规范

1. **不可变性**：一旦 Event Context 被实例化，传递给各个插件的过程中，该上下文是**只读**的。插件不允许修改原始输入上下文。
2. **上下文增强**：阶段性的产出（如 `affected-scope` 找出的受影响模块），不应修改输入 Event Context，而是通过 `ActionResult` 输出，由引擎负责合并到下一步的运行参数中。
3. **轻量传递**：上下文中只包含元数据和路径引用，不应包含大体积的原始文件内容或二进制数据。