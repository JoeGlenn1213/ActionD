# Changelog

## Unreleased（ASSURANCE isolated checkout）

**状态**：已落地，待发布版本号

- **隔离 checkout（CI 干净执行）**：插件不再跑在开发者本机工作区——`repopath.Checkout` 从 LGH bare 仓物化推送 sha 的干净副本（`~/.localgithub/checkouts/<repo>/<sha>`，幂等复用）；worker fail-closed（checkout 失败 → job failed，绝不回退脏工作区）；dispatcher 语言探测与 intent 提取同样优先 checkout，杜绝「未来代码」错位与 .env 泄漏；新 flags `--repos-dir` / `--checkout-root`
- **插件退出码诚实化**：Python 插件失败时 `sys.exit(1)`（此前恒 0，stdout JSON 单通道一旦失效会假 PASS）
- **worker.Stop 同步化**：Stop 等待执行 goroutine 退出（WaitGroup + sync.Once）——修复 `TestRequeueDedupInQueue` 的 TempDir 清理竞态（隔离 checkout 首轮 CI 暴露的既有 flake）

## Unreleased（ASSURANCE intent/task_id）

**状态**：已落地，待发布版本号

- **native 契约 v1**：`ActionJob.Intent` —— 派发时从 push commit message 提取 `task:/goal:` 标注（`job.IntentFromMessage` 单一事实源），SQLite 迁移 + API 暴露；`actiond_run_report` 的 native coverage 口径 = 派发时记录的意图（消息重构仍只算 reconstructed，R2 反作弊不变）
- **账本层地基（§7 前置债闭环）**：派发幂等（partial unique index + `ErrDuplicateJob`）、pending 恢复（`worker.Requeue`，43 orphan 实测复活）、启动事件缺口重放（水位线 + events.jsonl 尾部重放）
- **部署 SOP**：codesign 陷阱最终解法——全新路径（`~/.local/bin/actiond2`）+ `xattr -c` + 最终路径签名 + `plutil` 改 ProgramArguments + `launchctl bootout/bootstrap`；launchd 极简 PATH 需显式 git/go 回退

## Unreleased（ASSURANCE Phase C）

**状态**：已落地，待发布版本号

- **`actiond_handoff_pack`（第 22 个工具）**：按 ASSURANCE §2 契约生成 Handoff Package（envelope+payload+自包含 Markdown），Gate 实验中接手 Agent 的唯一输入；validateHandoff 降级不硬失败
- **假 PASS 修复（重要）**：exec runner 失败判定补认 ActionResult 的 `status: "failed"`——此前 python-pytest 收集错误/测试失败被标为 `done`（假阴性，由 Handoff Gate fixture 在生产抓出）；抽 `isFailureResult()` + 回归测试
- **verifier-canary 加固**：`find_go()` 显式解析 go 二进制（shutil.which + 已知路径回退）——launchd 最小 PATH 下首次运行曾误报 go toolchain not found

## Unreleased（ASSURANCE Phase B）

**状态**：已落地，待发布版本号

- **verifier 出处**：`ActionJob` 记录插件 manifest 版本（`plugin_version`）与执行 profile（`profile`）；SQLite 自动迁移（`ensureColumn`）+ REST API 暴露
- **Verdict 分档**：`actiond_run_report` 按 profile 粗粒度映射 tier（fast→FAST，full/release→FULL）；`promotion_allowed` 晋升闸门——只有 FULL pass 才可视为充分验证；Trust 区新增 `MaxTier/HasFullPass/VerifierProvenance`
- **verifier-canary 插件**：已知必挂/必过 Go fixture 元验证（mutation testing on the verifier），fail-closed——任何一组与预期不符即 job 失败（`repoFilter=ActionD.git`，仅验证器自身仓库触发）
- 前置：`Plugin` 接口新增 `Version()`（manifest 版本透传，`echo` 内置插件返回 `builtin`）

# Release v1.2.1

**Released**: 2026-08-18

**Changes since**: v1.2.0

---

## 🔧 Improvements

- **MCP 参数类型精确化**：`actiond_actions_list.limit`、`actiond_log.limit`、`actiond_diagnose.limit`、`dev_cycle_run.timeout`、`actiond_job_wait.timeout` 的 JSON Schema 从 `"number"` 收窄为 `"integer"`（新增 `withInteger` helper，规避 mcp-go v0.43.2 无 `WithInteger` 的库限制）
- **Makefile**：新增 `make lint` target（对齐 lgh 仓约定）

---

# Release v1.2.0

**Released**: 2026-04-08

**Changes since**: v1.1.2

---

## 🚀 Major Features

### Profile 过滤机制 (P0)

解决 "每次 push 触发 15 个 job" 的核心问题：

- **profile 过滤**: 激活 `supported_profiles` 字段，默认 profile=fast
- **MCP 接口**: 新增 `actiond_profile_get` / `actiond_profile_set` 工具
- **dev_cycle_run**: 支持 `profile` 参数，临时切换 CI 强度

| Profile | 触发 Jobs | 适用场景 |
|---------|----------|---------|
| fast | 3-5 | 日常开发，快速反馈 |
| full | 6-10 | 合并前，完整检查 |
| release | 10-15 | 发布流程，CI/CD |

### Job 去重与队列优化 (P0)

- **Job 去重**: 同一 event+plugin 不重复入队
- **队列扩容**: buffer 10 → 50，防止积压

### Push 元数据增强 (P1)

- **changed_files**: push 事件携带变更文件列表
- ActionD Event 新增 `ChangedFiles` 字段

### CI 状态回写 (P2)

- **LGH Status API**: `GET/POST /api/repos/{repo}/commits/{sha}/status`
- **ActionD 回调**: Job 完成后自动通知 LGH

---

## 📦 New MCP Tools

| 工具 | 说明 |
|------|------|
| `actiond_profile_get` | 查询当前执行 profile |
| `actiond_profile_set` | 切换 profile (fast/full/release) |

---

## 🔧 Modified Files

| 文件 | 变更 |
|------|------|
| `internal/dispatcher/dispatcher.go` | Profile 过滤 + SetProfile/Profile 方法 |
| `internal/plugin/plugin.go` | Plugin interface 新增 Profiles() |
| `internal/plugin/manifest.go` | SupportedProfiles 字段 |
| `internal/plugin/exec_runner.go` | profiles 字段和方法 |
| `internal/worker/worker.go` | Job 去重 + statusCallback |
| `internal/mcp/server.go` | profile_get/set 工具 |
| `internal/mcp/workflow.go` | dev_cycle_run profile 参数 |
| `internal/server/config.go` | Profile 配置持久化 |
| `internal/server/server.go` | /api/profile 端点 |
| `internal/event/types.go` | ChangedFiles 字段 |
| `internal/app/app.go` | profile 同步 + status callback |
| `plugins/*/manifest.json` | profiles 分类修正 |

---

## ✅ Tests

- 所有单元测试通过
- 编译验证通过 (Go 1.23+)

---

# Release v1.1.2

**Released**: 2026-03-28

**Changes since**: v1.0.2

---

## Quality Improvements

- **release**: 标准化插件清单格式，统一使用 manifest.json
- **plugin**: 完善 echo/deploy/release-note 插件定义
- **docs**: 更新项目文档和质量报告

## Bug Fixes

- 修复 release 插件使用非标准 plugin.yaml 格式

## Tests

- 完善单元测试和 E2E 测试覆盖

## P0/P1/P2 Quality Gates

- ✅ P0: ActionD Go 1.23.0 降级完成
- ✅ P1: API 路由测试和 E2E 测试已添加
- ✅ P2: 插件清单格式标准化完成

**Released**: 2026-03-25

**Changes since**: validation-go-build-20260312-0452

---

## 🚀 Features

- enhance CLI observability and result schema (5f992ecf)
- **log**: 添加日志轮转和清理功能 (f9eccf0b)
- **plugins**: 添加P2平台化插件 (87030477)
- **plugins**: 添加P1工程治理插件 (fa2374e7)
- **plugins**: 添加5个工程治理核心插件 (13ca3abb)
- 添加 python-build 和 java-build 插件，补齐 CI 闭环 (d145e438)

## 🐛 Bug Fixes

- **security_scan**: 添加 AI 生成的临时索引目录到排除列表 (cc9e9e08)
- **security_scan**: 添加 AI 生成的临时索引目录到排除列表 (d7711fc8)
- 修复 dev_cycle_run 路径参数 (9d2a4170)
- **go-lint**: 放宽 lint 规则，发现问题只警告不失败 (a3a6fd54)
- 修复 dev_cycle_run 的 lgh up 参数格式 (81cd9c08)
- 修复 dev_cycle_run 的 lgh up 参数格式 (ad749441)

## ✅ Tests

- dev_cycle_run MCP 工具验证 (ca11debe)
- dev_cycle_run 最终验证 (0aee170b)
- 验证 dev_cycle_run 修复完成 (d1fd999b)
- 验证 P0/P1/P2 新插件集成 (dc481732)

## 📝 Other Changes

- infra: fix Go lint debt (09deafbc)

## 👥 Contributors

Your Name


# ActionD Changelog

All notable changes to this project will be documented in this file.

## [1.2.0] - 2025-03-16

### Added

#### CLI Commands
- `actiond setup` - One-click environment initialization
- `actiond restart` - Restart the ActionD server
- Enhanced `actiond status` - Now shows directory paths, LGH connection, web assets info
- Enhanced `actiond doctor` - 8-category graded checks (FATAL/WARN/INFO)

#### Data Model (v0.3)
- Extended ActionJob model with 15+ new fields:
  - `event_type`, `trigger_reason` - Trigger source tracking
  - `branch`, `tag`, `commit_sha`, `commit_message`, `commit_author` - Git context
  - `artifacts`, `raw_log_path` - Artifact tracking
  - `error_summary`, `exit_code` - Error details
  - `result` - Structured ActionResult
  - `retry_count`, `retry_of`, `original_run` - Retry tracking

#### Structured Result Protocol
- New `internal/plugin/result.go` - Standardized plugin output format
- Plugins can now return structured JSON with:
  - `status`, `summary`, `failed_step`
  - `artifacts`, `metrics`, `hints`
  - AI-friendly error details

#### Failure Interpreter
- New `internal/interpreter/failure.go` - Auto-analyzes 20+ error patterns
- Recognizes common failures:
  - npm: install failed, lockfile mismatch, module not found
  - go: mod tidy, build failed, test failed, golangci-lint
  - python: module not found, pytest failed
  - java: maven/gradle build failed, test failed
  - general: permission denied, timeout, out of memory

#### Log Layers
- New `internal/log/layers.go` - 5-layer logging architecture
  - `event` - What happened (git push, tag)
  - `dispatch` - Plugin matching, scheduling
  - `plugin` - Execution output
  - `user` - Human-readable summary
  - `ai` - Structured JSON for AI consumption

#### Web Dashboard (ActionD-Web v0.2)
- Enhanced Action Detail page:
  - Info cards: Duration, Plugin, Repository, Trigger
  - Git context display: Commit SHA, Branch, Tag
  - Error summary with AI suggestions
  - Artifacts list
  - Retry button for failed/completed actions

#### Setup Module
- New `internal/app/setup.go` - Standardized directory initialization
- `DefaultDirs()` function for consistent path management

### Changed
- SQLite store now supports all new job fields
- Job creation populates git context from event payload
- Better error messages with `ErrorSummary` field

### Fixed
- Stop command now waits for process to exit before cleaning PID file
- Force kill fallback if graceful shutdown fails

---

## [1.1.0] - 2025-02

### Added
- MCP server for AI integration
- `dev_cycle_run` end-to-end workflow
- Auto-rollback on failure
- Hot plugin reload
- Real-time log streaming via SSE
- Job retry functionality (`/api/actions/{id}/retry`)
- Job cancel functionality (`/api/actions/{id}/cancel`)

### Changed
- Dynamic plugin discovery via manifest.json
- Better LGH socket connection handling

---

## [1.0.0] - 2025-01

### Added
- Initial release
- Core plugin system (go-lint, go-test-fast, go-build)
- Java plugins (quicktest, checkstyle)
- Python plugin (pytest)
- Web plugins (lint, test, build)
- Web dashboard
- CLI commands: start, stop, status, log, doctor, version
- Delegated dependency handling to plugins
- Plugin configuration via config.json
- Artifact storage and download
- Plugin enable/disable via API and CLI
- golangci-lint integration
- Event-driven architecture with LGH socket
- SQLite job persistence via CGO driver
- Worker pool for concurrent execution
- PID file management for daemon mode
- Basic doctor command for environment check
- Repo root resolution with LGH mappings fallback
- DeepWiki integration for AI documentation generation

---

[Unreleased]: https://github.com/JoeGlenn1213/ActionD/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/JoeGlenn1213/ActionD/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/JoeGlenn1213/ActionD/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/JoeGlenn1213/ActionD/releases/tag/v1.0.0

## 2026-08-22: intent/task_id 与账本地基
