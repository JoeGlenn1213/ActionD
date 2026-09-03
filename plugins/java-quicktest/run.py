#!/usr/bin/env python3
"""
java-quicktest plugin for ActionD v0.2
Runs intelligent test selection based on AST diff analysis.
Uses Schema v1 JSON output format.
Triggered on git.push events for Java projects.
"""

import json
import shutil
import subprocess
import sys
import os
from pathlib import Path


def to_action_result(old_result: dict, plugin_id: str, capability: str) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"
        
    decision = "pass" if status == "success" else "deny"
    summary_msg = old_result.get("message") or old_result.get("error") or "Java quicktest completed"
    if status == "success" and "message" not in old_result:
        mode = old_result.get("test_mode", "unknown")
        count = old_result.get("tests_run", 0)
        summary_msg = f"Passed {count} tests in {mode} mode"

    now = datetime.utcnow().isoformat() + "Z"

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "java",
        "status": status,
        "decision": decision,
        "timing": {
            "started_at": now,
            "finished_at": now,
            "duration_ms": old_result.get("analysis_time_ms", 0)
        },
        "context": {
            "repo": "unknown",
            "module": old_result.get("module", "unknown"),
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": summary_msg,
            "counts": {
                "tests_run": old_result.get("tests_run", 0)
            }
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "tests_passed": status == "success",
            "high_risk": old_result.get("high_risk", False)
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result

def _quicktest_cache_dir() -> Path:
    """JAR 运行时缓存目录（可复用构建产物，避免每次 push 都 mvn package）。"""
    return Path.home() / ".localgithub" / "actions" / "cache" / "java-quicktest"


def _find_quicktest_jar(plugin_dir: Path):
    """按候选顺序定位 java-quicktest JAR；找不到返回 None。"""
    # 1) 插件目录自带
    for pat in ("target/java-quicktest-*.jar", "*.jar"):
        jars = sorted(plugin_dir.glob(pat))
        if jars:
            return jars[-1]
    # 2) 运行时缓存（上次构建的产物）
    cache_dir = _quicktest_cache_dir()
    if cache_dir.is_dir():
        jars = sorted(cache_dir.glob("java-quicktest-*.jar"))
        if jars:
            return jars[-1]
    return None


def _find_quicktest_src(plugin_dir: Path):
    """定位 java-quicktest 源码仓库（含 pom.xml），找不到返回 None。

    从插件目录向上逐层探测（最多 5 层），兼容插件从 repo / 符号链接 /
    worktree 等不同位置加载的场景。
    """
    base = plugin_dir
    for _ in range(5):
        for cand in (base / "java-quicktest", base / "LocalGitHub" / "java-quicktest"):
            if (cand / "pom.xml").exists():
                return cand
        base = base.parent
    # LocalGitHub monorepo 兜底（源码在 ~/neil/LocalGitHub/java-quicktest）
    fallback = Path.home() / "neil" / "LocalGitHub" / "java-quicktest"
    if (fallback / "pom.xml").exists():
        return fallback
    return None


def main():
    # Read input from stdin
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path", ".")
    artifact_dir = input_data.get("artifact_dir", "/tmp")
    event = input_data.get("event", {})

    # Ensure artifact directory exists
    os.makedirs(artifact_dir, exist_ok=True)

    # Check if it's a Java project
    pom_file = Path(repo_path) / "pom.xml"
    if not pom_file.exists():
        action_result = to_action_result({
            "status": "success",
            "error": "",
            "artifacts": [],
            "message": "Not a Maven project, skipping"
        }, plugin_id="java-quicktest", capability="test-fast")
        print(json.dumps(action_result))
        return

    # Locate the java-quicktest JAR robustly. The old path derivation
    # (plugin_dir.parent.parent.parent / "java-quicktest/target/...") broke
    # when the plugin was loaded from a worktree or symlinked dir.
    plugin_dir = Path(__file__).resolve().parent
    quicktest_jar = _find_quicktest_jar(plugin_dir)

    if quicktest_jar is None:
        src_dir = _find_quicktest_src(plugin_dir)
        if src_dir is None:
            print(json.dumps(to_action_result({
                "status": "failed",
                "error": "java-quicktest JAR 缺失且未找到源码仓库",
                "artifacts": [],
                "message": "java-quicktest JAR unavailable",
            }, plugin_id="java-quicktest", capability="test-fast")))
            sys.exit(1)
        if not shutil.which("mvn"):
            print(json.dumps(to_action_result({
                "status": "success",
                "error": "",
                "artifacts": [],
                "message": "skipped: java-quicktest JAR 缺失且本机无 mvn 可构建",
            }, plugin_id="java-quicktest", capability="test-fast")))
            sys.exit(0)
        build_result = subprocess.run(
            ["mvn", "package", "-DskipTests", "-q"],
            cwd=str(src_dir),
            capture_output=True,
            text=True,
            timeout=180
        )
        if build_result.returncode != 0:
            print(json.dumps(to_action_result({
                "status": "failed",
                "error": "java-quicktest 构建失败: %s" % (build_result.stderr or "")[-300:],
                "artifacts": [],
                "message": "java-quicktest build failed",
            }, plugin_id="java-quicktest", capability="test-fast")))
            sys.exit(1)
        built = sorted((src_dir / "target").glob("java-quicktest-*.jar"))
        if not built:
            print(json.dumps(to_action_result({
                "status": "failed",
                "error": "java-quicktest 构建成功但未找到产物 JAR",
                "artifacts": [],
                "message": "java-quicktest JAR missing after build",
            }, plugin_id="java-quicktest", capability="test-fast")))
            sys.exit(1)
        quicktest_jar = built[-1]
        # Cache the JAR so later runs skip the mvn build entirely.
        try:
            cache_dir = _quicktest_cache_dir()
            cache_dir.mkdir(parents=True, exist_ok=True)
            shutil.copy2(quicktest_jar, cache_dir / quicktest_jar.name)
        except OSError:
            pass  # Caching is best-effort; the run itself still works.

    # Run java-quicktest to get affected tests (Schema v1 JSON)
    quicktest_cmd = [
        "java", "-jar", str(quicktest_jar),
        "--src", "src/main/java",
        "--test", "src/test/java",
        "--git-diff", "HEAD~1",
        "--json"
    ]

    try:
        quicktest_result = subprocess.run(
            quicktest_cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=60
        )

        # Check execution status
        if quicktest_result.returncode != 0:
            print(json.dumps({
                "status": "error",
                "error": f"Java execution failed (code {quicktest_result.returncode}): {quicktest_result.stderr}"
            }))
            sys.exit(1)

        # Parse Schema v1 output
        if quicktest_result.stdout:
            report = json.loads(quicktest_result.stdout)
            
            # Validate Schema v1
            if report.get("schemaVersion") != "1.0":
                print(json.dumps({
                    "status": "error",
                    "error": f"Unsupported schema version: {report.get('schemaVersion')}"
                }))
                sys.exit(1)
        else:
            # Empty output - no changes
            report = create_empty_report()

    except json.JSONDecodeError:
        print(json.dumps({
            "status": "error",
            "error": f"Invalid quicktest output: {quicktest_result.stdout}"
        }))
        sys.exit(1)
    except subprocess.TimeoutExpired:
        print(json.dumps({"status": "error", "error": "Quicktest analysis timeout"}))
        sys.exit(1)

    # Extract information from Schema v1
    selected_tests = extract_test_classes(report)
    decision = report.get("decision", {})
    run_full = decision.get("runFullTests", False)
    analysis = report.get("analysis", {})
    high_risk = analysis.get("riskAssessment", {}).get("highRisk", False)
    module_context = report.get("context", {}).get("module")

    saved_artifacts = []

    # Save full Schema v1 analysis result
    analysis_file = os.path.join(artifact_dir, "quicktest-analysis.json")
    with open(analysis_file, "w") as f:
        json.dump(report, f, indent=2)
    saved_artifacts.append(os.path.abspath(analysis_file))

    if not selected_tests:
        response = {
            "status": "success",
            "message": "No tests affected by changes",
            "artifacts": saved_artifacts,
            "schema_version": report.get("schemaVersion", "1.0")
        }
        action_result = to_action_result(response, plugin_id="java-quicktest", capability="test-fast")
        print(json.dumps(action_result))
        return

    # Determine test command (support multi-module)
    if run_full or high_risk:
        # Run all tests
        if module_context:
            test_cmd = ["mvn", "-pl", module_context["name"], "test", "-q"]
        else:
            test_cmd = ["mvn", "test", "-q"]
        test_mode = "full"
    else:
        # Run only selected tests
        test_classes = ",".join(selected_tests)
        if module_context:
            test_cmd = ["mvn", "-pl", module_context["name"], "test", "-q", f"-Dtest={test_classes}"]
        else:
            test_cmd = ["mvn", "test", "-q", f"-Dtest={test_classes}"]
        test_mode = "quick"

    try:
        test_result = subprocess.run(
            test_cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=300  # 5 minute timeout
        )

        # Save test output
        test_output_file = os.path.join(artifact_dir, "test-output.log")
        with open(test_output_file, "w") as f:
            f.write(f"Schema Version: {report.get('schemaVersion', '1.0')}\n")
            f.write(f"Test Mode: {test_mode}\n")
            f.write(f"Selected Tests: {selected_tests}\n")
            f.write(f"High Risk: {high_risk}\n")
            if module_context:
                f.write(f"Module: {module_context['name']}\n")
            f.write(f"Analysis Time: {report.get('metrics', {}).get('analysisTimeMs', 0)}ms\n")
            f.write("=" * 50 + "\n")
            f.write(test_result.stdout)
            if test_result.stderr:
                f.write("\n=== STDERR ===\n")
                f.write(test_result.stderr)
        saved_artifacts.append(os.path.abspath(test_output_file))

        response = {
            "status": "success" if test_result.returncode == 0 else "error",
            "error": f"Tests failed" if test_result.returncode != 0 else "",
            "artifacts": saved_artifacts,
            "schema_version": report.get("schemaVersion", "1.0"),
            "test_mode": test_mode,
            "tests_run": len(selected_tests),
            "high_risk": high_risk,
            "analysis_time_ms": report.get("metrics", {}).get("analysisTimeMs", 0),
            "module": module_context["name"] if module_context else None
        }
        action_result = to_action_result(response, plugin_id="java-quicktest", capability="test-fast")
        print(json.dumps(action_result))

    except subprocess.TimeoutExpired:
        print(json.dumps({"status": "error", "error": "Test timeout (5 min)"}))
        sys.exit(1)
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e)}))
        sys.exit(1)


def extract_test_classes(report):
    """
    Extract test class names from Schema v1 report.
    
    Args:
        report: Schema v1 analysis report dict
        
    Returns:
        List of test class names (strings)
    """
    tests = report.get("tests", [])
    return [test.get("testClass") for test in tests if test.get("testClass")]


def create_empty_report():
    """Create an empty Schema v1 report for no-change scenarios."""
    return {
        "schemaVersion": "1.0",
        "meta": {"mode": "quick"},
        "context": {},
        "changeSummary": {"changedFiles": [], "changedClasses": []},
        "analysis": {},
        "decision": {"runFullTests": False},
        "tests": [],
        "metrics": {}
    }


if __name__ == "__main__":
    main()
