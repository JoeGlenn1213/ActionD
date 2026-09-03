#!/usr/bin/env python3
"""
artifact_manifest - 生成构建产物清单

输出:
- artifact_name: 产物名称
- version: 版本号
- git_sha: Git 提交哈希
- build_time: 构建时间
- checksum: 文件校验和
- dependency_snapshot: 依赖快照
- language/runtime_version: 语言/运行时版本
"""

import sys
import json
import subprocess
import os
import hashlib
from pathlib import Path
from datetime import datetime

def get_git_info(repo_path: str) -> dict:
    """获取 Git 信息"""
    try:
        # 获取 SHA
        sha_result = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        sha = sha_result.stdout.strip() if sha_result.returncode == 0 else "unknown"

        # 获取短 SHA
        short_sha = sha[:8] if sha != "unknown" else "unknown"

        # 获取分支名
        branch_result = subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        branch = branch_result.stdout.strip() if branch_result.returncode == 0 else "unknown"

        # 获取提交信息
        msg_result = subprocess.run(
            ["git", "log", "-1", "--pretty=%s"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        message = msg_result.stdout.strip() if msg_result.returncode == 0 else "unknown"

        return {
            "sha": sha,
            "short_sha": short_sha,
            "branch": branch,
            "commit_message": message
        }
    except Exception:
        return {"sha": "unknown", "short_sha": "unknown", "branch": "unknown"}

def calculate_checksum(file_path: str) -> str:
    """计算文件 SHA256 校验和"""
    sha256_hash = hashlib.sha256()
    with open(file_path, "rb") as f:
        for chunk in iter(lambda: f.read(4096), b""):
            sha256_hash.update(chunk)
    return sha256_hash.hexdigest()

def get_go_version() -> str:
    """获取 Go 版本"""
    try:
        result = subprocess.run(["go", "version"], capture_output=True, text=True)
        if result.returncode == 0:
            return result.stdout.strip().split()[2]
    except Exception:
        pass
    return "unknown"

def get_python_version() -> str:
    """获取 Python 版本"""
    import sys
    return f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"

def get_java_version() -> str:
    """获取 Java 版本"""
    try:
        result = subprocess.run(["java", "-version"], capture_output=True, text=True)
        if result.returncode == 0:
            return result.stderr.split()[2].strip('"') if result.stderr else "unknown"
    except Exception:
        pass
    return "unknown"

def get_node_version() -> str:
    """获取 Node 版本"""
    try:
        result = subprocess.run(["node", "--version"], capture_output=True, text=True)
        if result.returncode == 0:
            return result.stdout.strip()
    except Exception:
        pass
    return "unknown"

def get_go_deps(repo_path: str) -> dict:
    """获取 Go 依赖"""
    deps = {"direct": [], "indirect": []}
    try:
        result = subprocess.run(
            ["go", "list", "-m", "-json", "all"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        if result.returncode == 0:
            for line in result.stdout.strip().split("\n}\n"):
                if line.strip():
                    try:
                        mod = json.loads(line + "}" if not line.endswith("}") else line)
                        dep_info = f"{mod.get('Path', '')}@{mod.get('Version', '')}"
                        if mod.get("Indirect"):
                            deps["indirect"].append(dep_info)
                        else:
                            deps["direct"].append(dep_info)
                    except:
                        pass
    except Exception:
        pass
    return deps

def get_python_deps(repo_path: str) -> dict:
    """获取 Python 依赖"""
    deps = {"direct": [], "indirect": []}
    req_file = Path(repo_path) / "requirements.txt"
    if req_file.exists():
        with open(req_file) as f:
            deps["direct"] = [line.strip() for line in f if line.strip() and not line.startswith("#")]
    return deps

def get_npm_deps(repo_path: str) -> dict:
    """获取 NPM 依赖"""
    deps = {"direct": [], "dev": []}
    pkg_file = Path(repo_path) / "package.json"
    if pkg_file.exists():
        with open(pkg_file) as f:
            pkg = json.load(f)
            deps["direct"] = list(pkg.get("dependencies", {}).keys())
            deps["dev"] = list(pkg.get("devDependencies", {}).keys())
    return deps

def detect_artifacts(repo_path: str) -> list:
    """检测构建产物"""
    artifacts = []
    repo = Path(repo_path)

    # Go 二进制
    for f in repo.glob("*.exe"):
        artifacts.append({
            "name": f.name,
            "type": "binary",
            "path": str(f.relative_to(repo)),
            "checksum": calculate_checksum(str(f)),
            "size": f.stat().st_size
        })

    # Python wheel/sdist
    for f in (repo / "dist").glob("*.whl"):
        artifacts.append({
            "name": f.name,
            "type": "wheel",
            "path": str(f.relative_to(repo)),
            "checksum": calculate_checksum(str(f)),
            "size": f.stat().st_size
        })

    # Java JAR
    for f in (repo / "target").glob("*.jar"):
        artifacts.append({
            "name": f.name,
            "type": "jar",
            "path": str(f.relative_to(repo)),
            "checksum": calculate_checksum(str(f)),
            "size": f.stat().st_size
        })

    # Node packages
    for f in (repo / "dist").glob("*.tgz"):
        artifacts.append({
            "name": f.name,
            "type": "npm_package",
            "path": str(f.relative_to(repo)),
            "checksum": calculate_checksum(str(f)),
            "size": f.stat().st_size
        })

    return artifacts

def to_action_result(old_result: dict, plugin_id: str, capability: str) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"
        
    decision = "pass" if status == "success" else "deny"
    summary_msg = old_result.get("summary", "Artifact manifest generated")

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
            "commit_sha": old_result.get("git", {}).get("sha", "unknown"),
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": summary_msg,
            "counts": {
                "artifacts_found": len(old_result.get("artifacts", []))
            }
        },
        "hints": [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "artifact_count": len(old_result.get("artifacts", [])),
            "manifest_generated": status == "success"
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

    # 收集元数据
    git_info = get_git_info(repo_path)
    build_time = datetime.utcnow().isoformat() + "Z"

    # 检测语言
    languages = []
    runtime_versions = {}

    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
        runtime_versions["go"] = get_go_version()
    if (Path(repo_path) / "pyproject.toml").exists() or list(Path(repo_path).glob("*.py")):
        languages.append("python")
        runtime_versions["python"] = get_python_version()
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists():
        languages.append("java")
        runtime_versions["java"] = get_java_version()
    if (Path(repo_path) / "package.json").exists():
        languages.append("typescript")
        runtime_versions["node"] = get_node_version()

    # 收集依赖快照
    dependencies = {}
    if "go" in languages:
        dependencies["go"] = get_go_deps(repo_path)
    if "python" in languages:
        dependencies["python"] = get_python_deps(repo_path)
    if "typescript" in languages:
        dependencies["npm"] = get_npm_deps(repo_path)

    # 检测产物
    artifacts = detect_artifacts(repo_path)

    # 生成版本号
    version = f"1.0.0-{git_info['short_sha']}"

    # 构建清单
    manifest = {
        "status": "success",
        "manifest_version": "1.0.0",
        "build_time": build_time,
        "version": version,
        "git": git_info,
        "languages": languages,
        "runtime_versions": runtime_versions,
        "dependencies": dependencies,
        "artifacts": artifacts,
        "summary": f"Generated manifest for {len(languages)} language(s), {len(artifacts)} artifact(s)"
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        manifest_path = os.path.join(artifact_dir, "artifact-manifest.json")
        with open(manifest_path, "w") as f:
            json.dump(manifest, f, indent=2)
        saved_artifacts.append("artifact-manifest.json")

    manifest["artifacts"] = saved_artifacts
    
    # Wrap as ActionResult
    action_result = to_action_result(manifest, plugin_id="artifact_manifest", capability="build")
    print(json.dumps(action_result))

if __name__ == "__main__":
    main()
