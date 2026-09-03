#!/usr/bin/env python3
"""
release-note - 自动生成发布说明

从以下来源生成:
- Git commits (conventional commits)
- PR labels (if available)
- Previous changelog

输出:
- RELEASE_NOTES.md: 本次发布说明
- CHANGELOG.md: 完整变更日志 (追加)
"""

import sys
import json
import subprocess
import os
import re
from pathlib import Path
from datetime import datetime
from collections import defaultdict

# Conventional commit 类型映射
COMMIT_TYPES = {
    "feat": ("Features", "🚀"),
    "fix": ("Bug Fixes", "🐛"),
    "docs": ("Documentation", "📚"),
    "style": ("Styles", "💎"),
    "refactor": ("Code Refactoring", "♻️"),
    "perf": ("Performance", "⚡"),
    "test": ("Tests", "✅"),
    "build": ("Build System", "📦"),
    "ci": ("CI/CD", "👷"),
    "chore": ("Chores", "🔧"),
    "revert": ("Reverts", "⏪"),
}

def get_git_log(repo_path: str, since_tag: str = None) -> list:
    """获取 Git 提交历史"""
    try:
        # 获取上一个 tag
        if not since_tag:
            result = subprocess.run(
                ["git", "describe", "--tags", "--abbrev=0", "HEAD~1"],
                cwd=repo_path,
                capture_output=True,
                text=True
            )
            since_tag = result.stdout.strip() if result.returncode == 0 else None

        # 获取提交
        cmd = ["git", "log", "--pretty=format:%H|%s|%an|%ad", "--date=short"]
        if since_tag:
            cmd.append(f"{since_tag}..HEAD")

        result = subprocess.run(
            cmd,
            cwd=repo_path,
            capture_output=True,
            text=True
        )

        commits = []
        if result.returncode == 0:
            for line in result.stdout.strip().split('\n'):
                if '|' in line:
                    parts = line.split('|')
                    if len(parts) >= 4:
                        commits.append({
                            "hash": parts[0][:8],
                            "subject": parts[1],
                            "author": parts[2],
                            "date": parts[3]
                        })

        return commits, since_tag

    except Exception as e:
        return [], None

def parse_conventional_commit(subject: str) -> dict:
    """解析 conventional commit"""
    # 格式: type(scope): description
    pattern = r'^(\w+)(?:\(([^\)]+)\))?:\s*(.+)$'
    match = re.match(pattern, subject)

    if match:
        return {
            "type": match.group(1),
            "scope": match.group(2),
            "description": match.group(3),
            "is_conventional": True
        }

    return {
        "type": "other",
        "scope": None,
        "description": subject,
        "is_conventional": False
    }

def get_current_tag(repo_path: str) -> str:
    """获取当前 tag"""
    try:
        result = subprocess.run(
            ["git", "describe", "--tags", "--exact-match", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        if result.returncode == 0:
            return result.stdout.strip()
    except:
        pass

    # 生成版本号
    result = subprocess.run(
        ["git", "rev-parse", "--short", "HEAD"],
        cwd=repo_path,
        capture_output=True,
        text=True
    )
    return f"v0.0.0-{result.stdout.strip()}" if result.returncode == 0 else "v0.0.0"

def generate_release_notes(commits: list, version: str, prev_tag: str = None) -> str:
    """生成发布说明"""
    # 按类型分组
    grouped = defaultdict(list)
    other_commits = []

    for commit in commits:
        parsed = parse_conventional_commit(commit["subject"])
        commit["parsed"] = parsed

        if parsed["is_conventional"] and parsed["type"] in COMMIT_TYPES:
            grouped[parsed["type"]].append(commit)
        else:
            other_commits.append(commit)

    # 生成 Markdown
    lines = [
        f"# Release {version}",
        "",
        f"**Released**: {datetime.now().strftime('%Y-%m-%d')}",
        ""
    ]

    if prev_tag:
        lines.append(f"**Changes since**: {prev_tag}")
        lines.append("")

    lines.append("---")
    lines.append("")

    # 按类型输出
    for commit_type in ["feat", "fix", "perf", "refactor", "docs", "test", "build", "ci", "chore"]:
        if commit_type in grouped:
            type_name, emoji = COMMIT_TYPES[commit_type]
            lines.append(f"## {emoji} {type_name}")
            lines.append("")

            for commit in grouped[commit_type]:
                parsed = commit["parsed"]
                scope = f"**{parsed['scope']}**: " if parsed["scope"] else ""
                lines.append(f"- {scope}{parsed['description']} ({commit['hash']})")

            lines.append("")

    # 其他更改
    if other_commits:
        lines.append("## 📝 Other Changes")
        lines.append("")
        for commit in other_commits[:20]:  # 限制数量
            lines.append(f"- {commit['subject']} ({commit['hash']})")
        lines.append("")

    # 贡献者
    authors = list(set(c["author"] for c in commits))
    if authors:
        lines.append("## 👥 Contributors")
        lines.append("")
        lines.append(", ".join(authors))
        lines.append("")

    return '\n'.join(lines)

def update_changelog(repo_path: str, release_notes: str, version: str) -> str:
    """更新 CHANGELOG.md"""
    changelog_path = Path(repo_path) / "CHANGELOG.md"

    existing = ""
    if changelog_path.exists():
        with open(changelog_path) as f:
            existing = f.read()

    # 新内容插入到开头（跳过标题）
    header = "# Changelog\n\n"
    if existing.startswith(header):
        new_content = header + release_notes + "\n\n---\n\n" + existing[len(header):]
    else:
        new_content = header + release_notes + "\n\n" + existing

    return new_content

def to_action_result(old_result: dict, plugin_id: str, capability: str) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"
        
    decision = "pass" if status == "success" else "deny"
    summary_msg = f"Generated release notes for {old_result.get('version', 'unknown')} ({old_result.get('commits_count', 0)} commits)"

    now = datetime.utcnow().isoformat() + "Z"

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "*",
        "status": status,
        "decision": decision,
        "timing": {
            "started_at": now,
            "finished_at": now,
            "duration_ms": 0
        },
        "context": {
            "repo": "unknown",
            "module": "unknown",
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": summary_msg,
            "counts": old_result.get("commit_stats", {})
        },
        "hints": [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "release_version": old_result.get("version"),
            "commits_count": old_result.get("commits_count", 0)
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result

def main():
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)

    # 获取信息
    commits, prev_tag = get_git_log(repo_path)
    current_tag = get_current_tag(repo_path)

    # 生成发布说明
    release_notes = generate_release_notes(commits, current_tag, prev_tag)

    # 更新 changelog
    changelog = update_changelog(repo_path, release_notes, current_tag)

    # 统计
    stats = defaultdict(int)
    for commit in commits:
        parsed = parse_conventional_commit(commit["subject"])
        stats[parsed["type"]] += 1

    result = {
        "status": "success",
        "version": current_tag,
        "previous_tag": prev_tag,
        "commits_count": len(commits),
        "commit_stats": dict(stats),
        "release_notes": release_notes,
        "artifacts": []
    }

    # 保存文件
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

        # RELEASE_NOTES.md
        notes_path = os.path.join(artifact_dir, "RELEASE_NOTES.md")
        with open(notes_path, "w") as f:
            f.write(release_notes)
        result["artifacts"].append("RELEASE_NOTES.md")

        # CHANGELOG.md (也写入 repo 根目录)
        changelog_path = os.path.join(artifact_dir, "CHANGELOG.md")
        with open(changelog_path, "w") as f:
            f.write(changelog)
        result["artifacts"].append("CHANGELOG.md")

        # 同时写入 repo 根目录
        repo_changelog = Path(repo_path) / "CHANGELOG.md"
        with open(repo_changelog, "w") as f:
            f.write(changelog)

    # Wrap as ActionResult
    action_result = to_action_result(result, plugin_id="release-note", capability="release")
    print(json.dumps(action_result))

if __name__ == "__main__":
    main()
