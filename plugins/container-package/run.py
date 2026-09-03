#!/usr/bin/env python3
"""
container-package - 构建打包产物

支持:
- Go: binary (GOOS/GOARCH cross-compile)
- Python: wheel, sdist
- Java: jar (Maven/Gradle)
- Node: npm pack, tarball
- Docker: image build

输出:
- package-manifest.json: 产物清单

fail-closed 约定:
- build 子命令失败 -> status=failed + exit 1 (不再吞失败)
- 无可构建语言/无 Dockerfile -> status=success + summary 注明 skipped
- 产物清单与 manifest.json 声明对齐 (主产物 package-manifest.json)
"""

import sys
import json
import subprocess
import os
import hashlib
import shutil
from pathlib import Path
from datetime import datetime


def tool_available(name: str) -> bool:
    return shutil.which(name) is not None


def get_git_info(repo_path: str) -> dict:
    """获取 Git 信息"""
    try:
        sha_result = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        sha = sha_result.stdout.strip() if sha_result.returncode == 0 else "unknown"

        tag_result = subprocess.run(
            ["git", "describe", "--tags", "--always"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        version = tag_result.stdout.strip() if tag_result.returncode == 0 else "0.0.0"

        return {"sha": sha, "version": version}
    except Exception:
        return {"sha": "unknown", "version": "0.0.0"}


def calculate_checksum(file_path: str) -> str:
    """计算 SHA256"""
    sha256 = hashlib.sha256()
    with open(file_path, "rb") as f:
        for chunk in iter(lambda: f.read(4096), b""):
            sha256.update(chunk)
    return sha256.hexdigest()


def build_go_binary(repo_path: str, output_dir: str, git_info: dict) -> dict:
    """构建 Go 二进制"""
    result = {"status": "skipped", "artifacts": []}

    if not (Path(repo_path) / "go.mod").exists():
        result["reason"] = "No go.mod found"
        return result

    if not tool_available("go"):
        result["reason"] = "go not installed"
        return result

    try:
        with open(Path(repo_path) / "go.mod") as f:
            module_line = f.readline().strip()
            module_name = module_line.replace("module ", "").strip('"')

        binary_name = module_name.split("/")[-1] if "/" in module_name else module_name
        if not binary_name:
            binary_name = "app"

        ldflags = f"-X main.Version={git_info['version']} -X main.Commit={git_info['sha']}"

        output_path = os.path.join(output_dir, binary_name)
        build_result = subprocess.run(
            ["go", "build", "-ldflags", ldflags, "-o", output_path, "."],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=120
        )

        if build_result.returncode == 0:
            result["status"] = "success"
            result["artifacts"].append({
                "name": binary_name,
                "type": "binary",
                "path": output_path,
                "checksum": calculate_checksum(output_path),
                "size": os.path.getsize(output_path),
                "platform": f"{os.uname().sysname}/{os.uname().machine}"
            })
        else:
            result["status"] = "failed"
            result["error"] = (build_result.stderr or build_result.stdout or "go build failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def build_python_package(repo_path: str, output_dir: str, git_info: dict) -> dict:
    """构建 Python 包"""
    result = {"status": "skipped", "artifacts": []}

    has_pyproject = (Path(repo_path) / "pyproject.toml").exists()
    has_setup = (Path(repo_path) / "setup.py").exists()

    if not has_pyproject and not has_setup:
        result["reason"] = "No pyproject.toml/setup.py found"
        return result

    # python build 模块缺失 -> 降级为 skipped
    if subprocess.run([sys.executable, "-c", "import build"], capture_output=True).returncode != 0:
        result["reason"] = "python 'build' module not installed"
        return result

    try:
        dist_dir = Path(repo_path) / "dist"

        if dist_dir.exists():
            shutil.rmtree(dist_dir)

        build_result = subprocess.run(
            [sys.executable, "-m", "build"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=180
        )

        if build_result.returncode == 0:
            result["status"] = "success"

            for f in dist_dir.glob("*.whl"):
                dest = os.path.join(output_dir, f.name)
                shutil.copy(f, dest)
                result["artifacts"].append({
                    "name": f.name,
                    "type": "wheel",
                    "path": dest,
                    "checksum": calculate_checksum(dest),
                    "size": os.path.getsize(dest)
                })

            for f in dist_dir.glob("*.tar.gz"):
                dest = os.path.join(output_dir, f.name)
                shutil.copy(f, dest)
                result["artifacts"].append({
                    "name": f.name,
                    "type": "sdist",
                    "path": dest,
                    "checksum": calculate_checksum(dest),
                    "size": os.path.getsize(dest)
                })
        else:
            result["status"] = "failed"
            result["error"] = (build_result.stderr or build_result.stdout or "python build failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def build_java_package(repo_path: str, output_dir: str, git_info: dict) -> dict:
    """构建 Java JAR"""
    result = {"status": "skipped", "artifacts": []}

    pom = (Path(repo_path) / "pom.xml").exists()
    gradle = (Path(repo_path) / "build.gradle").exists() or (Path(repo_path) / "build.gradle.kts").exists()

    if not pom and not gradle:
        result["reason"] = "No Maven/Gradle build file"
        return result

    try:
        if pom:
            if not tool_available("mvn"):
                result["reason"] = "mvn not installed"
                return result
            build_result = subprocess.run(
                ["mvn", "package", "-DskipTests", "-q"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300
            )
            target_dir = Path(repo_path) / "target"
        else:
            gradlew = Path(repo_path) / "gradlew"
            if not gradlew.exists():
                result["reason"] = "gradlew not found"
                return result
            build_result = subprocess.run(
                [str(gradlew), "build", "-x", "test", "-q"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300
            )
            target_dir = Path(repo_path) / "build" / "libs"

        if build_result.returncode == 0:
            result["status"] = "success"
            for f in target_dir.glob("*.jar"):
                if "original" not in f.name and "sources" not in f.name:
                    dest = os.path.join(output_dir, f.name)
                    shutil.copy(f, dest)
                    result["artifacts"].append({
                        "name": f.name,
                        "type": "jar",
                        "path": dest,
                        "checksum": calculate_checksum(dest),
                        "size": os.path.getsize(dest)
                    })
        else:
            result["status"] = "failed"
            result["error"] = (build_result.stderr or build_result.stdout or "java build failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def build_npm_package(repo_path: str, output_dir: str, git_info: dict) -> dict:
    """构建 NPM 包"""
    result = {"status": "skipped", "artifacts": []}

    if not (Path(repo_path) / "package.json").exists():
        result["reason"] = "No package.json found"
        return result

    if not tool_available("npm"):
        result["reason"] = "npm not installed"
        return result

    try:
        pack_result = subprocess.run(
            ["npm", "pack"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=60
        )

        if pack_result.returncode == 0:
            result["status"] = "success"

            tarball = pack_result.stdout.strip().splitlines()[-1].strip() if pack_result.stdout.strip() else ""
            if tarball:
                src = os.path.join(repo_path, tarball)
                dest = os.path.join(output_dir, tarball)
                if os.path.exists(src):
                    shutil.move(src, dest)
                    result["artifacts"].append({
                        "name": tarball,
                        "type": "npm_tarball",
                        "path": dest,
                        "checksum": calculate_checksum(dest),
                        "size": os.path.getsize(dest)
                    })
        else:
            result["status"] = "failed"
            result["error"] = (pack_result.stderr or pack_result.stdout or "npm pack failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def build_docker_image(repo_path: str, git_info: dict) -> dict:
    """构建 Docker 镜像"""
    result = {"status": "skipped", "artifacts": []}

    if not (Path(repo_path) / "Dockerfile").exists():
        result["reason"] = "No Dockerfile found"
        return result

    if not tool_available("docker"):
        result["reason"] = "docker not installed"
        return result

    try:
        image_name = Path(repo_path).name.lower().replace("_", "-")
        tag = f"{image_name}:{git_info['sha']}"

        build_result = subprocess.run(
            ["docker", "build", "-t", tag, "."],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=600
        )

        if build_result.returncode == 0:
            result["status"] = "success"
            result["artifacts"].append({
                "name": tag,
                "type": "docker_image",
                "size": "unknown"
            })
        else:
            result["status"] = "failed"
            result["error"] = (build_result.stderr or build_result.stdout or "docker build failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def to_action_result(plugin_id: str, capability: str, status: str, decision: str,
                     message: str, counts: dict, hints: list, artifacts: list,
                     signals: dict, language: str = "*") -> dict:
    """构造 V1 ActionResult。"""
    import uuid

    now = datetime.utcnow().isoformat() + "Z"
    return {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": language,
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
            "message": message,
            "counts": counts
        },
        "hints": hints,
        "artifacts": [{"name": a, "path": a} if isinstance(a, str) else a for a in artifacts],
        "signals": signals,
        "raw_outputs": {},
        "next_actions": []
    }


def main() -> int:
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        return 1

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        return 1

    git_info = get_git_info(repo_path)

    output_dir = artifact_dir or os.path.join(repo_path, ".artifacts", "packages")
    os.makedirs(output_dir, exist_ok=True)

    # 检测语言
    languages = []
    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
    if (Path(repo_path) / "pyproject.toml").exists() or (Path(repo_path) / "setup.py").exists():
        languages.append("python")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists() or (Path(repo_path) / "build.gradle.kts").exists():
        languages.append("java")
    if (Path(repo_path) / "package.json").exists():
        languages.append("node")
    has_dockerfile = (Path(repo_path) / "Dockerfile").exists()

    results = {
        "status": "success",
        "languages": languages,
        "git": git_info,
        "build_time": datetime.utcnow().isoformat() + "Z",
        "builds": {}
    }

    all_artifacts = []

    if "go" in languages:
        r = build_go_binary(repo_path, output_dir, git_info)
        results["builds"]["go"] = r
        all_artifacts.extend(r.get("artifacts", []))

    if "python" in languages:
        r = build_python_package(repo_path, output_dir, git_info)
        results["builds"]["python"] = r
        all_artifacts.extend(r.get("artifacts", []))

    if "java" in languages:
        r = build_java_package(repo_path, output_dir, git_info)
        results["builds"]["java"] = r
        all_artifacts.extend(r.get("artifacts", []))

    if "node" in languages:
        r = build_npm_package(repo_path, output_dir, git_info)
        results["builds"]["node"] = r
        all_artifacts.extend(r.get("artifacts", []))

    if has_dockerfile:
        r = build_docker_image(repo_path, git_info)
        results["builds"]["docker"] = r
        all_artifacts.extend(r.get("artifacts", []))

    # 汇总门禁
    failed = []
    skipped = []
    succeeded = []
    for name, b in results["builds"].items():
        if b.get("status") == "failed":
            failed.append((name, b.get("error", "build failed")))
        elif b.get("status") == "success":
            succeeded.append(name)
        else:
            skipped.append((name, b.get("reason", "skipped")))

    hints = []
    for name, err in failed:
        hints.append(f"[{name}] build failed: {err}")
    for name, reason in skipped:
        hints.append(f"[{name}] skipped: {reason}")

    if failed:
        status = "failed"
        decision = "deny"
        message = "Build failed: " + ", ".join(name for name, _ in failed)
    elif succeeded:
        status = "success"
        decision = "pass"
        message = f"Built {len(succeeded)} package(s): " + ", ".join(succeeded)
    else:
        status = "success"
        decision = "pass"
        reasons = "; ".join(f"{name}: {reason}" for name, reason in skipped) or "nothing to build"
        message = f"Skipped: {reasons}"

    # 文件产物 (docker_image 无文件路径, 不进入文件清单)
    file_artifacts = [a for a in all_artifacts if isinstance(a, dict) and a.get("path")]

    results["artifacts"] = all_artifacts
    results["summary"] = {
        "total_artifacts": len(all_artifacts),
        "types": list(set(a["type"] for a in all_artifacts)),
        "output_dir": output_dir
    }

    # 保存 manifest (主产物)
    saved_artifacts = []
    if artifact_dir:
        manifest_path = os.path.join(artifact_dir, "package-manifest.json")
        with open(manifest_path, "w") as f:
            json.dump(results, f, indent=2)
        saved_artifacts.append({"name": "package-manifest.json", "path": manifest_path})

    # 引擎记录: manifest + 实际构建出的文件产物
    for a in file_artifacts:
        saved_artifacts.append({"name": a.get("name", ""), "path": a.get("path", "")})

    action_result = to_action_result(
        plugin_id="container-package", capability="build",
        status=status,
        decision=decision,
        message=message,
        counts={
            "artifacts": len(all_artifacts),
            "built": len(succeeded),
            "failed": len(failed),
            "skipped": len(skipped),
        },
        hints=hints,
        artifacts=saved_artifacts,
        signals={
            "total_artifacts": len(all_artifacts),
            "built": len(succeeded),
            "failed": len(failed),
        },
    )
    print(json.dumps(action_result))
    return 1 if status == "failed" else 0


if __name__ == "__main__":
    sys.exit(main())
