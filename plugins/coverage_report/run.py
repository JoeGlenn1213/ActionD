#!/usr/bin/env python3
"""
coverage_report - 收集测试覆盖率报告

支持:
- Go: go test -cover
- Python: pytest --cov (需 pytest-cov)
- Java: JaCoCo
- TypeScript: Jest/Vitest coverage

输出:
- line_coverage: 行覆盖率
- branch_coverage: 分支覆盖率
- coverage_diff: 与基线的差异
- flaky_tests: 不稳定测试标记
- slow_tests: 慢测试列表

fail-closed 约定:
- 插件自己触发测试且测试失败 -> status=failed + exit 1 (不再吞失败)
- 无覆盖率配置/工具缺失 -> 视为 skipped: status=success + summary 注明 skipped (不误报失败)
"""

import sys
import json
import subprocess
import os
import shutil
from pathlib import Path


def tool_available(name: str) -> bool:
    return shutil.which(name) is not None


def _file_contains(path: Path, needle: str) -> bool:
    """检查文本文件是否包含指定子串（用于检测构建配置，如 jacoco 插件）。"""
    try:
        return needle in path.read_text(encoding="utf-8", errors="ignore")
    except OSError:
        return False


def _log(message: str) -> None:
    """日志走 stderr，避免污染 stdout 上的 V1 ActionResult JSON。"""
    print(f"[coverage_report] {message}", file=sys.stderr)


def run_go_coverage(repo_path: str, artifact_dir: str) -> dict:
    """运行 Go 覆盖率测试"""
    if not tool_available("go"):
        return {"status": "skipped", "reason": "go not installed"}

    try:
        result = subprocess.run(
            ["go", "test", "./...", "-coverprofile=coverage.out", "-covermode=atomic"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=120
        )

        if result.returncode != 0:
            return {
                "status": "failed",
                "error": (result.stderr or result.stdout or "go test failed").strip()[-2000:]
            }

        # 计算覆盖率
        coverage_result = subprocess.run(
            ["go", "tool", "cover", "-func=coverage.out"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )

        total_coverage = 0.0
        if coverage_result.returncode == 0:
            for line in coverage_result.stdout.strip().split("\n"):
                if "total:" in line.lower():
                    parts = line.split()
                    for part in parts:
                        if "%" in part:
                            try:
                                total_coverage = float(part.replace("%", ""))
                            except ValueError:
                                pass
                            break

        return {
            "status": "success",
            "line_coverage": total_coverage,
            "raw_output": coverage_result.stdout
        }
    except Exception as e:
        return {"status": "failed", "error": str(e)}


def run_python_coverage(repo_path: str, artifact_dir: str) -> dict:
    """运行 Python 覆盖率测试"""
    if not tool_available("pytest"):
        return {"status": "skipped", "reason": "pytest not installed"}

    # 覆盖率配置缺失 (无 pytest-cov) -> skipped, 不误报失败
    has_pytest_cov = subprocess.run(
        [sys.executable, "-c", "import pytest_cov"],
        capture_output=True,
        text=True
    ).returncode == 0
    if not has_pytest_cov:
        return {"status": "skipped", "reason": "pytest-cov not installed (no coverage config)"}

    try:
        result = subprocess.run(
            ["pytest", "--cov=.", "--cov-report=json", "--cov-report=term", "-v", "--tb=short"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=180
        )

        # pytest 退出码: 0=通过, 1=测试失败, 5=未收集到测试
        if result.returncode == 5:
            return {"status": "skipped", "reason": "no tests collected"}
        if result.returncode != 0:
            return {
                "status": "failed",
                "error": (result.stdout or result.stderr or "pytest failed").strip()[-2000:]
            }

        # 读取 coverage.json
        coverage_file = Path(repo_path) / "coverage.json"
        coverage_data = {}
        if coverage_file.exists():
            with open(coverage_file) as f:
                coverage_data = json.load(f)

        total_coverage = coverage_data.get("totals", {}).get("percent_covered", 0)

        return {
            "status": "success",
            "line_coverage": total_coverage,
            "raw_output": result.stdout[-2000:] if len(result.stdout) > 2000 else result.stdout
        }
    except Exception as e:
        return {"status": "failed", "error": str(e)}


def run_java_coverage(repo_path: str, artifact_dir: str) -> dict:
    """运行 Java 覆盖率测试 (JaCoCo)"""
    repo = Path(repo_path)
    pom_file = repo / "pom.xml"
    gradle_groovy = repo / "build.gradle"
    gradle_kts = repo / "build.gradle.kts"

    if not pom_file.exists() and not gradle_groovy.exists() and not gradle_kts.exists():
        return {"status": "skipped", "reason": "No Maven/Gradle build file"}

    try:
        if pom_file.exists():
            if not tool_available("mvn"):
                return {"status": "skipped", "reason": "mvn not installed"}
            # 无 jacoco 插件配置 -> skipped，不把 mvn 的 "No plugin found for prefix 'jacoco'" 误报为失败
            if not _file_contains(pom_file, "jacoco-maven-plugin"):
                _log("java: pom.xml 未配置 jacoco-maven-plugin, skipped")
                return {"status": "skipped", "reason": "项目未配置 jacoco 覆盖率插件"}
            result = subprocess.run(
                ["mvn", "test", "jacoco:report", "-q"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300
            )
        else:
            gradle_file = gradle_kts if gradle_kts.exists() else gradle_groovy
            gradlew = repo / "gradlew"
            if not gradlew.exists():
                return {"status": "skipped", "reason": "gradlew not found (no coverage config)"}
            if not _file_contains(gradle_file, "jacoco"):
                _log("java: build.gradle 未配置 jacoco, skipped")
                return {"status": "skipped", "reason": "项目未配置 jacoco 覆盖率插件"}
            result = subprocess.run(
                [str(gradlew), "test", "jacocoTestReport", "-q"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=300
            )

        if result.returncode != 0:
            return {
                "status": "failed",
                "error": (result.stderr or result.stdout or "build/test failed").strip()[-2000:]
            }

        # 解析 JaCoCo 报告
        jacoco_xml = Path(repo_path) / "target" / "site" / "jacoco" / "jacoco.xml"
        if not jacoco_xml.exists():
            jacoco_xml = Path(repo_path) / "build" / "reports" / "jacoco" / "test" / "jacocoTestReport.xml"

        coverage = 0.0
        if jacoco_xml.exists():
            import xml.etree.ElementTree as ET
            tree = ET.parse(jacoco_xml)
            root = tree.getroot()
            counter = root.find(".//counter[@type='INSTRUCTION']")
            if counter is not None:
                covered = int(counter.get("covered", 0))
                missed = int(counter.get("missed", 0))
                total = covered + missed
                if total > 0:
                    coverage = (covered / total) * 100

        return {
            "status": "success",
            "line_coverage": coverage,
            "raw_output": result.stdout[-1000:] if result.stdout else ""
        }
    except Exception as e:
        return {"status": "failed", "error": str(e)}


def run_typescript_coverage(repo_path: str, artifact_dir: str) -> dict:
    """运行 TypeScript 覆盖率测试"""
    pkg_file = Path(repo_path) / "package.json"
    if not pkg_file.exists():
        return {"status": "skipped", "reason": "No package.json found"}

    if not tool_available("npm"):
        return {"status": "skipped", "reason": "npm not installed"}

    try:
        with open(pkg_file) as f:
            pkg = json.load(f)
        scripts = pkg.get("scripts", {})
        if "test" not in scripts and "test:coverage" not in scripts:
            return {"status": "skipped", "reason": "No test script in package.json (no coverage config)"}

        result = subprocess.run(
            ["npm", "test", "--", "--coverage", "--coverageReporters=json-summary"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=180
        )

        if result.returncode != 0:
            return {
                "status": "failed",
                "error": (result.stdout or result.stderr or "npm test failed").strip()[-2000:]
            }

        coverage_file = Path(repo_path) / "coverage" / "coverage-summary.json"
        coverage = 0.0
        if coverage_file.exists():
            with open(coverage_file) as f:
                cov_data = json.load(f)
                coverage = cov_data.get("total", {}).get("lines", {}).get("pct", 0)

        return {
            "status": "success",
            "line_coverage": coverage,
            "raw_output": result.stdout[-1000:] if result.stdout else ""
        }
    except Exception as e:
        return {"status": "failed", "error": str(e)}


def to_action_result(plugin_id: str, capability: str, status: str, decision: str,
                     message: str, counts: dict, hints: list, artifacts: list,
                     signals: dict, language: str = "*") -> dict:
    """构造 V1 ActionResult。"""
    from datetime import datetime
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

    # 检测语言
    detected_langs = []
    if (Path(repo_path) / "go.mod").exists():
        detected_langs.append("go")
    if list(Path(repo_path).glob("*.py")) or (Path(repo_path) / "pyproject.toml").exists():
        detected_langs.append("python")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists() or (Path(repo_path) / "build.gradle.kts").exists():
        detected_langs.append("java")
    if (Path(repo_path) / "package.json").exists():
        detected_langs.append("typescript")

    if not detected_langs:
        result = {
            "status": "success",
            "summary": "Skipped: no supported language detected",
            "detected_languages": [],
            "coverage_by_language": {},
            "average_coverage": 0.0,
            "details": {},
            "artifacts": []
        }
        action_result = to_action_result(
            plugin_id="coverage_report", capability="coverage",
            status="success", decision="pass",
            message="Skipped: no supported language detected",
            counts={"languages": 0},
            hints=[],
            artifacts=[],
            signals={"coverage_percent": 0.0, "coverage_by_language": {}},
        )
        print(json.dumps(action_result))
        return 0

    # 运行覆盖率测试
    coverage_results = {}
    if "go" in detected_langs:
        coverage_results["go"] = run_go_coverage(repo_path, artifact_dir)
    if "python" in detected_langs:
        coverage_results["python"] = run_python_coverage(repo_path, artifact_dir)
    if "java" in detected_langs:
        coverage_results["java"] = run_java_coverage(repo_path, artifact_dir)
    if "typescript" in detected_langs:
        coverage_results["typescript"] = run_typescript_coverage(repo_path, artifact_dir)

    # 汇总结果
    failed = []
    skipped = []
    total_coverage = 0.0
    count = 0
    coverage_by_language = {}
    for lang, data in coverage_results.items():
        if data.get("status") == "failed":
            failed.append((lang, data.get("error", data.get("reason", "failed"))))
        elif data.get("status") == "skipped":
            skipped.append((lang, data.get("reason", "skipped")))
        elif data.get("status") == "success" and "line_coverage" in data:
            total_coverage += data["line_coverage"]
            count += 1
            coverage_by_language[lang] = data["line_coverage"]

    avg_coverage = total_coverage / count if count > 0 else 0.0

    hints = []
    if failed:
        status = "failed"
        decision = "deny"
        for lang, err in failed:
            hints.append(f"[{lang}] coverage/test run failed: {err}")
        message = f"Coverage run failed for {len(failed)} language(s): " + ", ".join(l for l, _ in failed)
    elif count > 0:
        status = "success"
        decision = "pass"
        message = f"Coverage report generated for {count} language(s), avg: {avg_coverage:.1f}%"
        for lang, reason in skipped:
            hints.append(f"[{lang}] skipped: {reason}")
    else:
        # 全部 skipped: 无覆盖率配置, 视为 skipped 而非失败
        status = "success"
        decision = "pass"
        reasons = "; ".join(f"{lang}: {reason}" for lang, reason in skipped) or "no coverage configured"
        message = f"Skipped: {reasons}"

    result = {
        "status": status,
        "summary": message,
        "detected_languages": detected_langs,
        "coverage_by_language": coverage_by_language,
        "average_coverage": round(avg_coverage, 2),
        "details": coverage_results
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        report_path = os.path.join(artifact_dir, "coverage-report.json")
        with open(report_path, "w") as f:
            json.dump(result, f, indent=2)
        saved_artifacts.append(report_path)

    action_result = to_action_result(
        plugin_id="coverage_report", capability="coverage",
        status=status,
        decision=decision,
        message=message,
        counts={
            "languages": len(detected_langs),
            "coverage_measured": count,
            "failed": len(failed),
            "skipped": len(skipped),
        },
        hints=hints,
        artifacts=saved_artifacts,
        signals={
            "coverage_percent": round(avg_coverage, 2),
            "coverage_by_language": coverage_by_language,
        },
    )
    print(json.dumps(action_result))
    return 1 if status == "failed" else 0


if __name__ == "__main__":
    sys.exit(main())
