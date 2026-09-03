#!/usr/bin/env python3
"""
env-check - 检查语言运行时和工具链版本

检查项:
- Go/Python/Java/Node 版本
- lockfile 是否存在
- build tool 是否匹配
- 必要环境变量
- docker/runner 能力
"""

import sys
import json
import subprocess
import os
import shutil
from pathlib import Path

def get_version(cmd: list) -> str:
    """获取命令版本"""
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode == 0:
            return result.stdout.strip().split('\n')[0][:100]
    except Exception:
        pass
    return None

def check_go() -> dict:
    """检查 Go 环境"""
    info = {"installed": False}
    version = get_version(["go", "version"])
    if version:
        info["installed"] = True
        info["version"] = version
        # 检查 go.mod
        info["has_go_mod"] = Path("go.mod").exists()
        # 检查 go.sum
        info["has_go_sum"] = Path("go.sum").exists()
        # GOPATH
        info["gopath"] = os.environ.get("GOPATH", "")
    return info

def check_python() -> dict:
    """检查 Python 环境"""
    info = {"installed": False}

    # python3
    version = get_version(["python3", "--version"])
    if version:
        info["installed"] = True
        info["version"] = version

        # 检查包管理器
        info["has_pip"] = shutil.which("pip3") is not None
        info["has_poetry"] = shutil.which("poetry") is not None
        info["has_pipenv"] = shutil.which("pipenv") is not None

        # 检查 lock 文件
        info["has_requirements"] = Path("requirements.txt").exists()
        info["has_pyproject"] = Path("pyproject.toml").exists()
        info["has_poetry_lock"] = Path("poetry.lock").exists()
        info["has_pipfile"] = Path("Pipfile").exists()
        info["has_pipfile_lock"] = Path("Pipfile.lock").exists()

    return info

def check_java() -> dict:
    """检查 Java 环境"""
    info = {"installed": False}

    # java
    version = get_version(["java", "-version"])
    if version:
        info["installed"] = True
        info["version"] = version.split('\n')[0] if '\n' in version else version

        # 检查构建工具
        info["has_maven"] = shutil.which("mvn") is not None
        info["has_gradle"] = shutil.which("gradle") is not None or Path("gradlew").exists()

        # 检查配置文件
        info["has_pom"] = Path("pom.xml").exists()
        info["has_build_gradle"] = Path("build.gradle").exists() or Path("build.gradle.kts").exists()

    return info

def check_node() -> dict:
    """检查 Node.js 环境"""
    info = {"installed": False}

    version = get_version(["node", "--version"])
    if version:
        info["installed"] = True
        info["version"] = version

        # npm/yarn/pnpm
        info["has_npm"] = shutil.which("npm") is not None
        info["npm_version"] = get_version(["npm", "--version"])

        info["has_yarn"] = shutil.which("yarn") is not None
        info["yarn_version"] = get_version(["yarn", "--version"])

        info["has_pnpm"] = shutil.which("pnpm") is not None
        info["pnpm_version"] = get_version(["pnpm", "--version"])

        # lock 文件
        info["has_package_json"] = Path("package.json").exists()
        info["has_package_lock"] = Path("package-lock.json").exists()
        info["has_yarn_lock"] = Path("yarn.lock").exists()
        info["has_pnpm_lock"] = Path("pnpm-lock.yaml").exists()

    return info

def check_docker() -> dict:
    """检查 Docker 环境"""
    info = {"installed": False}

    version = get_version(["docker", "--version"])
    if version:
        info["installed"] = True
        info["version"] = version

        # 检查 Dockerfile
        info["has_dockerfile"] = Path("Dockerfile").exists()

        # 检查 docker-compose
        info["has_compose"] = Path("docker-compose.yml").exists() or Path("docker-compose.yaml").exists()
        info["compose_version"] = get_version(["docker", "compose", "version"])

    return info

def check_env_vars(required_vars: list) -> dict:
    """检查环境变量"""
    result = {}
    missing = []
    for var in required_vars:
        value = os.environ.get(var)
        result[var] = "set" if value else "missing"
        if not value:
            missing.append(var)
    return {"vars": result, "missing": missing}

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

    os.chdir(repo_path)

    # 检测项目语言
    languages = []
    if Path("go.mod").exists():
        languages.append("go")
    if Path("pyproject.toml").exists() or Path("setup.py").exists() or list(Path(".").glob("*.py")):
        languages.append("python")
    if Path("pom.xml").exists() or Path("build.gradle").exists():
        languages.append("java")
    if Path("package.json").exists():
        languages.append("node")

    # 执行检查
    results = {
        "status": "success",
        "languages_detected": languages,
        "checks": {}
    }

    issues = []

    # 检查各语言环境
    results["checks"]["go"] = check_go()
    if "go" in languages and not results["checks"]["go"].get("has_go_sum"):
        issues.append({"level": "warning", "message": "Go project missing go.sum - run 'go mod tidy'"})

    results["checks"]["python"] = check_python()
    if "python" in languages:
        py = results["checks"]["python"]
        if not py.get("has_requirements") and not py.get("has_pyproject"):
            issues.append({"level": "warning", "message": "Python project missing dependency file"})

    results["checks"]["java"] = check_java()
    results["checks"]["node"] = check_node()
    if "node" in languages:
        node = results["checks"]["node"]
        if not node.get("has_package_lock") and not node.get("has_yarn_lock") and not node.get("has_pnpm_lock"):
            issues.append({"level": "warning", "message": "Node project missing lock file"})

    results["checks"]["docker"] = check_docker()

    # 检查通用环境变量
    common_vars = ["PATH", "HOME"]
    results["checks"]["env_vars"] = check_env_vars(common_vars)

    # 汇总
    results["issues"] = issues
    results["summary"] = {
        "languages": languages,
        "all_installed": all(
            results["checks"].get(lang, {}).get("installed", True)
            for lang in ["go", "python", "java", "node"] if lang in languages
        ),
        "issues_count": len(issues),
        "warnings_count": len([i for i in issues if i["level"] == "warning"])
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        report_path = os.path.join(artifact_dir, "env-report.json")
        with open(report_path, "w") as f:
            json.dump(results, f, indent=2)
        saved_artifacts.append("env-report.json")

    results["artifacts"] = saved_artifacts
    print(json.dumps(results))

if __name__ == "__main__":
    main()
