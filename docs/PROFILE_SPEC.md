# Actiond Profile Specification V1

本文档定义了 Actiond V1 Pipeline Profile（流水线配置）的标准规范。Profile 是将散落的插件按特定场景进行组合编排的声明式描述。

## 1. 核心概念

- **Profile (配置文件)**：定义了特定场景下（如快速验证、全面回归、发布）的执行全景。
- **Pipeline (流水线)**：Profile 被触发后实例化运行的过程。
- **Stage (阶段)**：流水线划分为多个逻辑阶段，阶段间通常串行，阶段内允许并发。
- **Step (步骤)**：阶段内的最小执行单元，引用具体的 `capability` 或 `plugin_id`。

## 2. 默认 Profile 清单

系统内置四种标准 Profile，推荐所有的工程项目使用或继承：

1. **fast**：主打快反馈，适用于 `git.push` 或本地开发。重点在轻量级的 lint 和单元测试。
2. **full**：全面回归，适用于 PR 合并或主干分支监控。包含全量测试、覆盖率、依赖扫描。
3. **release**：版本发布，适用于 `git.tag`。强调产物构建、审批与部署闭环。
4. **nightly**：夜间巡检，适用于定时任务。包含耗时长的基准测试、全量安全扫描等。

## 3. Profile Schema 规范

一个典型的 `profile.yaml` 结构如下：

```yaml
name: fast-profile
description: 快速反馈流水线，适用于 push 级轻量校验
triggers:
  - event: git.push
    branches:
      - '*'
    exclude_branches:
      - main
      - release/*

# 全局配置，对所有 step 生效
global_env:
  DEBUG: "false"

# 流水线阶段定义，按顺序执行
stages:
  - name: setup
    steps:
      - capability: env-check       # 优先使用能力抽象进行调用
      - capability: affected-scope

  - name: static-analysis
    parallel: true                  # 允许阶段内并发执行
    steps:
      - capability: lint
        timeout_seconds: 300
      - plugin_id: security-scan    # 也可以直接调用具体的插件ID

  - name: testing
    steps:
      - capability: test-fast
        continue_on_error: false    # 默认 false，设为 true 时该步骤失败不阻塞流水线

  - name: governance
    steps:
      - capability: coverage-report
      - capability: policy-gate
        with:
          ruleset: fast-default     # 传递参数给插件

  - name: observability
    always_run: true                # 无论前面成功失败，此阶段始终运行
    steps:
      - capability: observability-export
```

## 4. 依赖解析与装配逻辑

编排引擎 (Pipeline Engine) 在解析 Profile 时，遵循以下规则：

1. **能力映射**：当 Step 中配置了 `capability: lint` 时，引擎会根据当前项目的语言类型（如 `language: python`），自动匹配注册表中 `capability=lint` 且 `language=python` 的插件（如 `python-ruff`）。
2. **条件执行**：若某 Step 声明的插件依赖项在当前上下文中不满足（例如没有相关文件变更），该 Step 状态将被标记为 `skipped`。
3. **熔断机制**：任一未标记为 `continue_on_error: true` 的 Step 失败，且其返回的 Decision 为 `deny`，流水线将立即熔断。