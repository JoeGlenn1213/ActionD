# Actiond Plugin Specification V1

本文档定义了 Actiond V1 插件系统的标准规范，所有新旧插件都必须遵循此规范进行实现和改造。

## 1. 插件设计原则

1. **唯一归属**：每个插件只属于一个特定的 Layer（Language / Common / Governance / Release），不重复注册。
2. **职责分离**：上层编排关心 Capability（能力），底层执行关心 Implementation（实现）。
3. **输出标准**：每个插件的输出必须封装为标准的 `ActionResult` 对象。

## 2. 插件元数据规范 (Metadata)

每个插件都必须提供一个标准的元数据定义文件（例如 `plugin.yaml` 或在代码中暴露相同结构的字典），作为插件注册的凭证。

```yaml
# 必备字段
id: python-ruff                 # 插件全局唯一ID，kebab-case命名
version: 1.0.0                  # 插件版本，遵循语义化版本
status: enabled                 # 状态: enabled, disabled, deprecated
owner_layer: language           # 所属层级: language, common, governance, release
capability: lint                # 统一能力语义，用于编排层引用
default_triggers:               # 默认监听的事件类型
  - git.push
inputs:                         # 插件声明需要的输入参数
  - repo_path
  - changed_files
  - config_path
outputs:                        # 插件声明会产出的输出类型
  - normalized_result
  - raw_log_path

# 推荐字段
language: python                # 适用的语言（仅当 owner_layer=language 时需要）
implementation: ruff            # 底层使用的具体工具
supported_profiles:             # 声明支持哪些默认的 pipeline profile
  - fast
  - full
depends_on: []                  # 前置依赖的 capability 或 plugin_id
produces_artifact: false        # 是否产生可部署产物
```

## 3. 插件生命周期

一个 Actiond 插件在执行时，经历以下标准生命周期：

1. **Initialize (初始化)**：接收上下文输入，解析元数据与配置，检查运行环境依赖（如对应的底层 CLI 工具是否已安装）。
2. **Execute (执行)**：调用底层工具执行实际的工程动作。
3. **Normalize (标准化)**：收集工具的原始输出（stdout/stderr/文件），并将其转换为 `ActionResult` 标准格式。
4. **Finalize (结束)**：清理临时文件，上报最终结果给 Pipeline 引擎。

## 4. 标准输入 (Input)

插件通过标准事件上下文接收输入，详见 `EVENT_MODEL.md`。

## 5. 标准输出 (Output)

插件必须返回一个符合 `action-result.schema.json` 规范的对象，包含：

- **Status (状态)**：`success`, `failed`, `skipped`, `blocked`, `warning`
- **Decision (决策)**：`pass`, `soft-pass`, `needs-review`, `deny`
- **Summary (摘要)**：简短的执行总结和关键数据统计。
- **Signals (信号)**：用于 Policy Gate 拦截的关键指标（如 `lint_error_count`, `coverage_delta`）。

## 6. 错误处理规范

插件内部发生错误时：
- 不应直接崩溃退出（Panic）。
- 应该捕获异常，并返回一个 `Status=failed` 且带有明确错误原因的 `ActionResult`。
- `Decision` 应该根据错误的严重程度设置为 `needs-review` 或 `deny`。