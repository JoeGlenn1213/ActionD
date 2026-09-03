#!/usr/bin/env python3
"""
integration-test - 集成测试和 E2E 测试

支持:
- Docker Compose 服务编排
- 服务健康检查
- 集成测试执行
- E2E 测试 (Playwright/Cypress)

输出:
- integration-results.json
- e2e-report.html

fail-closed 约定:
- 子命令失败 (pytest/go test/npm/playwright/cypress/compose up) -> status=failed + exit 1
- 无集成测试定义 -> status=success + summary 注明 skipped (不误报失败)
- docker compose down 去掉 -v, 不做破坏性卷清理
"""

import sys
import json
import subprocess
import os
import time
import shutil
from pathlib import Path
from datetime import datetime

COMPOSE_FILES = ["docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"]


def tool_available(name: str) -> bool:
    return shutil.which(name) is not None


def find_compose_file(repo_path: str):
    for name in COMPOSE_FILES:
        if (Path(repo_path) / name).exists():
            return name
    return None


def check_docker_available() -> bool:
    """检查 Docker 是否可用"""
    try:
        result = subprocess.run(
            ["docker", "info"],
            capture_output=True,
            timeout=10
        )
        return result.returncode == 0
    except Exception:
        return False


def start_services(repo_path: str) -> dict:
    """启动服务 (docker-compose)"""
    result = {"status": "skipped", "services": []}

    compose_file = find_compose_file(repo_path)
    if not compose_file:
        result["reason"] = "No docker-compose file found"
        return result

    if not check_docker_available():
        result["reason"] = "Docker not available"
        return result

    try:
        up_result = subprocess.run(
            ["docker", "compose", "-f", compose_file, "up", "-d", "--wait"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=180
        )

        if up_result.returncode == 0:
            result["status"] = "success"

            ps_result = subprocess.run(
                ["docker", "compose", "-f", compose_file, "ps", "--format", "json"],
                cwd=repo_path,
                capture_output=True,
                text=True
            )

            if ps_result.returncode == 0 and ps_result.stdout.strip():
                try:
                    services = json.loads(ps_result.stdout)
                    if isinstance(services, list):
                        result["services"] = [s.get("Service", s.get("name", "unknown")) for s in services]
                    else:
                        result["services"] = [services.get("Service", "unknown")]
                except Exception:
                    pass
        else:
            result["status"] = "failed"
            result["error"] = (up_result.stderr or up_result.stdout or "docker compose up failed").strip()[-2000:]

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def stop_services(repo_path: str, compose_file: str = "docker-compose.yml"):
    """停止服务 (非破坏性: 不加 -v, 保留卷数据)"""
    try:
        subprocess.run(
            ["docker", "compose", "-f", compose_file, "down"],
            cwd=repo_path,
            capture_output=True,
            timeout=60
        )
    except Exception:
        pass


def _has_python_integration(repo_path: str) -> bool:
    repo = Path(repo_path)
    if (repo / "tests" / "integration").is_dir():
        return True
    for p in repo.rglob("*integration*.py"):
        if any(part in {".git", "node_modules", "vendor", "__pycache__"} for part in p.parts):
            continue
        return True
    return False


def _has_go_integration(repo_path: str) -> bool:
    repo = Path(repo_path)
    for p in repo.rglob("*.go"):
        if any(part in {".git", "vendor", "node_modules"} for part in p.parts):
            continue
        try:
            content = p.read_text(errors="ignore")
        except Exception:
            continue
        if "//go:build integration" in content or "// +build integration" in content:
            return True
    return False


def run_integration_tests(repo_path: str, artifact_dir: str) -> dict:
    """运行集成测试"""
    result = {"status": "skipped", "tests": [], "passed": 0, "failed": 0}

    has_pytest = _has_python_integration(repo_path)
    has_go_test = _has_go_integration(repo_path)

    if not has_pytest and not has_go_test:
        result["reason"] = "No integration tests defined"
        return result

    try:
        if has_pytest:
            if not tool_available("pytest"):
                result["reason"] = "pytest not installed"
                return result

            test_result = subprocess.run(
                ["pytest", "tests/integration/", "-v", "--tb=short"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300,
                env={**os.environ, "INTEGRATION_TEST": "true"}
            )

            result["output"] = test_result.stdout[-5000:] if test_result.stdout else ""

            import re
            m = re.search(r'(\d+) passed', test_result.stdout or "")
            if m:
                result["passed"] = int(m.group(1))
            m = re.search(r'(\d+) failed', test_result.stdout or "")
            if m:
                result["failed"] = int(m.group(1))

            if test_result.returncode != 0:
                result["status"] = "failed"
                result["error"] = (test_result.stdout or test_result.stderr or "pytest failed").strip()[-2000:]
            else:
                result["status"] = "success"

        elif has_go_test:
            if not tool_available("go"):
                result["reason"] = "go not installed"
                return result

            test_result = subprocess.run(
                ["go", "test", "-tags=integration", "-v", "./..."],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300
            )

            result["output"] = test_result.stdout[-5000:] if test_result.stdout else ""

            if test_result.stdout:
                result["passed"] = test_result.stdout.count("--- PASS:")
                result["failed"] = test_result.stdout.count("--- FAIL:")

            if test_result.returncode != 0:
                result["status"] = "failed"
                result["error"] = (test_result.stdout or test_result.stderr or "go test failed").strip()[-2000:]
            else:
                result["status"] = "success"

    except Exception as e:
        result["status"] = "failed"
        result["error"] = str(e)

    return result


def run_e2e_tests(repo_path: str, artifact_dir: str) -> dict:
    """运行 E2E 测试"""
    result = {"status": "skipped", "tests": []}

    pkg_file = Path(repo_path) / "package.json"
    if not pkg_file.exists():
        result["reason"] = "No package.json found"
        return result

    try:
        with open(pkg_file) as f:
            pkg = json.load(f)

        scripts = pkg.get("scripts", {})
        deps = {**pkg.get("dependencies", {}), **pkg.get("devDependencies", {})}
        has_playwright = "playwright" in deps or "@playwright/test" in deps
        has_cypress = "cypress" in deps

        if has_playwright:
            cmd = ["npx", "playwright", "test", "--reporter=json"]
        elif has_cypress:
            cmd = ["npx", "cypress", "run", "--reporter", "json"]
        elif "test:e2e" in scripts:
            cmd = ["npm", "run", "test:e2e"]
        else:
            result["reason"] = "No E2E test framework detected"
            return result

        if not tool_available("npm") and not tool_available("npx"):
            result["reason"] = "npm/npx not installed"
            return result

        e2e_result = subprocess.run(
            cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=600
        )

        result["output"] = e2e_result.stdout[-5000:] if e2e_result.stdout else ""

        if e2e_result.returncode != 0:
            result["status"] = "failed"
            result["error"] = (e2e_result.stdout or e2e_result.stderr or "e2e test failed").strip()[-2000:]
        else:
            result["status"] = "success"

        # 读取报告
        report_files = list(Path(repo_path).glob("**/*report*.json"))
        for rf in report_files:
            try:
                with open(rf) as f:
                    json.load(f)
                    result["tests"].append({
                        "report_file": str(rf.name),
                        "summary": "available"
                    })
            except Exception:
                pass

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

    results = {
        "status": "success",
        "timestamp": datetime.now().isoformat()
    }

    compose_file = find_compose_file(repo_path)
    services = None
    if compose_file:
        services = start_services(repo_path)
        results["services"] = services

    try:
        if services and services.get("status") == "success":
            time.sleep(5)

        results["integration"] = run_integration_tests(repo_path, artifact_dir)
        results["e2e"] = run_e2e_tests(repo_path, artifact_dir)
    finally:
        # 仅在确实启动了服务时 down (非破坏性, 不加 -v)
        if compose_file and services and services.get("status") == "success":
            stop_services(repo_path, compose_file)

    # 汇总
    integration = results.get("integration", {})
    e2e = results.get("e2e", {})
    services_status = services.get("status", "skipped") if services else "skipped"

    results["summary"] = {
        "integration_passed": integration.get("passed", 0),
        "integration_failed": integration.get("failed", 0),
        "e2e_status": e2e.get("status", "skipped"),
        "services_status": services_status
    }

    hints = []
    failed_parts = []

    if services_status == "failed":
        failed_parts.append("services")
        hints.append(f"[services] {services.get('error', 'docker compose up failed')}")
    if services_status == "skipped" and compose_file:
        hints.append(f"[services] skipped: {services.get('reason', 'not started')}")

    if integration.get("status") == "failed":
        failed_parts.append("integration")
        hints.append(f"[integration] {integration.get('error', 'integration tests failed')}")
    if integration.get("status") == "skipped":
        hints.append(f"[integration] skipped: {integration.get('reason', 'no integration tests')}")

    if e2e.get("status") == "failed":
        failed_parts.append("e2e")
        hints.append(f"[e2e] {e2e.get('error', 'e2e tests failed')}")
    if e2e.get("status") == "skipped":
        hints.append(f"[e2e] skipped: {e2e.get('reason', 'no e2e tests')}")

    if failed_parts:
        status = "failed"
        decision = "deny"
        message = "Integration/E2E failed: " + ", ".join(failed_parts)
    elif integration.get("status") == "success" or e2e.get("status") == "success":
        status = "success"
        decision = "pass"
        message = (f"Integration tests: {integration.get('passed', 0)} passed, "
                   f"{integration.get('failed', 0)} failed; e2e: {e2e.get('status')}")
    else:
        status = "success"
        decision = "pass"
        message = "Skipped: no integration or E2E tests defined"

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        json_path = os.path.join(artifact_dir, "integration-results.json")
        with open(json_path, "w") as f:
            json.dump(results, f, indent=2)
        saved_artifacts.append(json_path)

    action_result = to_action_result(
        plugin_id="integration-test", capability="test",
        status=status,
        decision=decision,
        message=message,
        counts={
            "integration_passed": integration.get("passed", 0),
            "integration_failed": integration.get("failed", 0),
            "e2e_status": e2e.get("status", "skipped"),
        },
        hints=hints,
        artifacts=saved_artifacts,
        signals={
            "integration_passed": integration.get("passed", 0),
            "integration_failed": integration.get("failed", 0),
            "e2e_status": e2e.get("status", "skipped"),
            "services_status": services_status,
        },
    )
    print(json.dumps(action_result))
    return 1 if status == "failed" else 0


if __name__ == "__main__":
    sys.exit(main())
