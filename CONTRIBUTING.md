# Contributing to ActionD

感谢你关注 ActionD！欢迎提交 PR 和 Issue。

## 开发流程

1. **Fork** 本仓库
2. **创建分支**：`git checkout -b feature/your-feature-name`
3. **开发**并保持代码整洁
4. **运行测试**：`go test ./...`
5. **运行 linter**：`golangci-lint run`
6. **提交**：遵循 [Conventional Commits](https://www.conventionalcommits.org/)
7. **Push** 并创建 Pull Request

## 分支命名

- `feature/` - 新功能
- `fix/` - 修复
- `refactor/` - 重构
- `docs/` - 文档

## 提交信息格式

```
<type>: <description>

[optional body]

[optional footer]
```

示例：
```
feat: add python-pytest plugin

- 支持 pytest coverage
- 支持 unittest

Closes #123
```

**Type 类型：**
- `feat` - 新功能
- `fix` - Bug 修复
- `docs` - 文档
- `style` - 格式（不影响代码运行）
- `refactor` - 重构
- `test` - 测试
- `chore` - 构建/工具

## 代码规范

- 使用 `golangci-lint` 校验
- 公共函数/方法需要注释
- 错误处理要明确
- 避免硬编码常量

## 测试要求

- 新功能请附带测试
- 修复 bug 时请添加回归测试
- 确保 `go test ./...` 全部通过

## 开发环境

```bash
# 克隆
git clone https://github.com/YOUR_NAME/ActionD.git
cd ActionD

# 安装依赖
go mod download

# 本地构建
make build

# 运行测试
go test ./...

# Lint
golangci-lint run
```

## 问题反馈

- Bug 请提交 [Issue](https://github.com/JoeGlenn1213/ActionD/issues)
- 功能建议欢迎讨论
- 紧急问题可发邮件

## 许可证

贡献的代码将采用 MIT 许可证。
