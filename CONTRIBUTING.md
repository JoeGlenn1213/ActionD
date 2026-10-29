# Contributing to ActionD

[English](#english) · [简体中文](#简体中文)

---

## English

Thanks for contributing. Bug reports, docs fixes, and plugins are all welcome.

### Development workflow

1. Fork the repo and create a branch (`feature/…`, `fix/…`, `docs/…`)
2. Make your change
3. Run tests and lint:
   ```bash
   make test    # go test ./... -v
   make lint    # golangci-lint run ./...
   ```
4. Commit with [Conventional Commits](https://www.conventionalcommits.org/)
5. Open a Pull Request (see the PR template checklist)

### Repo layout (where to change what)

| Area | Path |
|------|------|
| CLI entry | `cmd/actiond/` |
| Engine / HTTP / MCP | `internal/app`, `internal/server`, `internal/mcp`, … |
| **New plugins** | `plugins/<name>/manifest.json` + runner — **no** `main.go` edits; see [docs/PLUGIN_GUIDE.md](docs/PLUGIN_GUIDE.md) |
| Contracts | `schemas/`, [docs/PLUGIN_SPEC.md](docs/PLUGIN_SPEC.md) |
| Web console | sibling repo [actiond-web](https://github.com/JoeGlenn1213/actiond-web) |

### Local setup

```bash
git clone https://github.com/YOUR_NAME/ActionD.git
cd ActionD
go mod download
make build          # → dist/actiond

# Runtime needs LGH:
#   lgh serve -d
# Then:
./dist/actiond setup
./dist/actiond doctor
./dist/actiond start
```

Console UI (optional): build/deploy [actiond-web](https://github.com/JoeGlenn1213/actiond-web) or pass `--web-dir`. Dev console often runs on `:3001` against ActionD on `:3000`.

Environment variables and hardening: [README § Environment variables](README.md#environment-variables), [SECURITY.md](SECURITY.md).

### Commit message format

```
<type>: <description>

[optional body]
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`.

### Code & tests

- Public Go APIs need comments; prefer clear errors over silent skips
- New behavior should include tests; bugfixes should add regression coverage
- Plugin PRs: keep `manifest.json` valid (`name`, `command`, `triggers`) and document any new env requirements

### Issues

- Bugs / features: use GitHub Issue templates
- Security: [SECURITY.md](SECURITY.md) — private advisory, not a public issue

### License

Contributions are licensed under MIT.

---

## 简体中文

感谢关注 ActionD。欢迎提交 Issue、文档修正与插件。

### 开发流程

1. Fork 并创建分支（`feature/` / `fix/` / `docs/`）
2. 开发改动
3. 运行：
   ```bash
   make test
   make lint
   ```
4. 使用 [Conventional Commits](https://www.conventionalcommits.org/) 提交
5. 按 PR 模板自检后开 Pull Request

### 改哪里

- **新插件**：`plugins/<name>/manifest.json` + 脚本，**不要**改 `cmd/actiond/main.go`。详见 [docs/PLUGIN_GUIDE.md](docs/PLUGIN_GUIDE.md)
- **引擎 / API / MCP**：`internal/`
- **Web 控制台**：独立仓库 [actiond-web](https://github.com/JoeGlenn1213/actiond-web)

### 本地环境

需要本机运行 `lgh serve -d`；`actiond setup` / `doctor` / `start` 见英文小节。环境变量与安全约定见 [README_CN.md](README_CN.md) 与 [SECURITY.md](SECURITY.md)。

### 问题反馈

- 功能与缺陷：使用 Issue 模板
- 安全问题：按 [SECURITY.md](SECURITY.md) 私下披露

贡献代码采用 MIT 许可证。
