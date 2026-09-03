#!/usr/bin/env python3
"""
affected_scope - 分析 git 变更影响范围

输出:
- changed_files: 变更的文件列表
- affected_modules: 受影响的模块
- affected_languages: 受影响的编程语言
- downstream_impact: 下游影响分析
"""

import sys
import json
import subprocess
import os
from pathlib import Path
from collections import defaultdict

# 语言到文件扩展名的映射
LANG_EXTENSIONS = {
    "go": [".go"],
    "python": [".py", ".pyi", ".pyx"],
    "java": [".java", ".kt", ".kts"],
    "typescript": [".ts", ".tsx", ".js", ".jsx"],
    "rust": [".rs"],
    "cpp": [".cpp", ".cc", ".cxx", ".h", ".hpp"],
}

# 语言到模块标识文件的映射
MODULE_MARKERS = {
    "go": ["go.mod"],
    "python": ["pyproject.toml", "setup.py", "requirements.txt", "Pipfile"],
    "java": ["pom.xml", "build.gradle", "build.gradle.kts"],
    "typescript": ["package.json"],
    "rust": ["Cargo.toml"],
}

# 超大仓保护上限：变更文件超过该数量时跳过逐文件模块分析。
# find_module_root 对每个文件向上探测模块标识，64k 文件的备份仓会让
# 该分析跑到 120s+ 仍超时（narrly-platform-backup 实测）。
MAX_CHANGED_FILES = 10000

def detect_language(file_path: str) -> str:
    """根据文件扩展名检测语言"""
    ext = Path(file_path).suffix.lower()
    for lang, extensions in LANG_EXTENSIONS.items():
        if ext in extensions:
            return lang
    return "unknown"

def find_module_root(file_path: str, repo_path: str) -> str:
    """找到文件所属的模块根目录"""
    file_path = Path(file_path)
    repo_path = Path(repo_path)

    # 向上查找模块标识文件
    current = file_path.parent if file_path.is_file() else file_path
    while current != repo_path and current != current.parent:
        for lang, markers in MODULE_MARKERS.items():
            for marker in markers:
                if (current / marker).exists():
                    return str(current.relative_to(repo_path) or ".")
        current = current.parent

    return "."

def get_changed_files(repo_path: str) -> list:
    """获取变更文件列表"""
    try:
        # 获取最近一次提交的变更
        result = subprocess.run(
            ["git", "diff", "--name-only", "HEAD~1", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        if result.returncode != 0 or not result.stdout.strip():
            # 可能是首次提交，使用 git diff --cached
            result = subprocess.run(
                ["git", "diff", "--name-only", "--cached"],
                cwd=repo_path,
                capture_output=True,
                text=True
            )
        if result.returncode != 0:
            return []
        return [f for f in result.stdout.strip().split("\n") if f]
    except Exception:
        return []

def analyze_downstream_impact(modules: list, repo_path: str) -> list:
    """分析下游影响（简化版：基于 import/depend 分析）"""
    # 这里可以扩展为更复杂的依赖分析
    # 目前返回受影响模块列表
    return modules

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

    # 获取变更文件
    changed_files = get_changed_files(repo_path)

    # 超大仓保护：超过上限时只做廉价的语言统计，跳过逐文件模块探测
    # （后者对超大 diff 是指数级 stat 开销，会吃满插件超时）。
    if len(changed_files) > MAX_CHANGED_FILES:
        affected_languages = defaultdict(int)
        for f in changed_files:
            affected_languages[detect_language(f)] += 1
        result = {
            "status": "success",
            "summary": (
                f"skipped per-file analysis: {len(changed_files)} changed files "
                f"exceeds affected-scope cap ({MAX_CHANGED_FILES})"
            ),
            "changed_files": [],
            "affected_modules": {},
            "affected_languages": dict(affected_languages),
            "downstream_impact": [],
            "stats": {
                "total_files": len(changed_files),
                "total_modules": 0,
                "languages_detected": list(affected_languages.keys()),
                "skipped": "oversized",
            },
        }
        saved_artifacts = []
        if artifact_dir:
            os.makedirs(artifact_dir, exist_ok=True)
            report_path = os.path.join(artifact_dir, "affected-scope.json")
            with open(report_path, "w") as f:
                json.dump(result, f, indent=2)
            saved_artifacts.append("affected-scope.json")
        result["artifacts"] = saved_artifacts
        print(json.dumps(result))
        return

    # 分析受影响的语言
    affected_languages = defaultdict(int)
    for f in changed_files:
        lang = detect_language(f)
        affected_languages[lang] += 1

    # 分析受影响模块
    affected_modules = defaultdict(list)
    for f in changed_files:
        lang = detect_language(f)
        if lang != "unknown":
            module = find_module_root(f, repo_path)
            if f not in affected_modules[module]:
                affected_modules[module].append(f)

    # 下游影响分析
    downstream_impact = analyze_downstream_impact(list(affected_modules.keys()), repo_path)

    result = {
        "status": "success",
        "summary": f"Found {len(changed_files)} changed files affecting {len(affected_modules)} modules",
        "changed_files": changed_files,
        "affected_modules": {k: v for k, v in affected_modules.items()},
        "affected_languages": dict(affected_languages),
        "downstream_impact": downstream_impact,
        "stats": {
            "total_files": len(changed_files),
            "total_modules": len(affected_modules),
            "languages_detected": list(affected_languages.keys())
        }
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        report_path = os.path.join(artifact_dir, "affected-scope.json")
        with open(report_path, "w") as f:
            json.dump(result, f, indent=2)
        saved_artifacts.append("affected-scope.json")

    result["artifacts"] = saved_artifacts
    print(json.dumps(result))

if __name__ == "__main__":
    main()
