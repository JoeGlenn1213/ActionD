#!/usr/bin/env python3
"""
benchmark - 性能基准测试

支持:
- Go: go test -bench
- Python: pytest-benchmark
- Java: JMH
- Node: npm run benchmark

输出:
- benchmark-report.json: 结构化结果
- benchmark-report.md: 可读报告

契约: stdout 最后一行输出单个 V1 ActionResult JSON（含 action_id）；日志一律 stderr。
无 benchmark 定义 -> skipped（success + exit 0）；执行失败 -> failed + deny + exit 1；
成功 -> 汇总到 summary 并产出报告 artifact。
"""

import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from datetime import datetime

PLUGIN_ID = "benchmark"
CAPABILITY = "test"


def log(message):
    print("[benchmark] %s" % message, file=sys.stderr)


def to_action_result(status, summary_msg, issue_count, artifacts,
                     language, decision, benchmark_count=0):
    import uuid
    now = datetime.utcnow().isoformat() + "Z"
    return {
        "action_id": "act_%s" % uuid.uuid4().hex[:8],
        "plugin_id": PLUGIN_ID,
        "capability": CAPABILITY,
        "language": language,
        "status": status,
        "decision": decision,
        "timing": {
            "started_at": now,
            "finished_at": now,
            "duration_ms": 0,
        },
        "context": {
            "repo": "unknown",
            "module": "unknown",
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown",
        },
        "summary": {
            "message": summary_msg,
            "counts": {"issues": issue_count, "benchmarks": benchmark_count},
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": artifacts,
        "signals": {
            "benchmark_count": benchmark_count,
        },
        "raw_outputs": {},
        "next_actions": [],
    }


def _emit(action_result, exit_code):
    print(json.dumps(action_result))
    sys.exit(exit_code)


def _emit_skipped(reason, language):
    action_result = to_action_result(
        "success", "skipped: %s" % reason, 0, [], language, "pass")
    log(reason)
    _emit(action_result, 0)


def read_input():
    try:
        data = json.load(sys.stdin)
        if isinstance(data, dict):
            return data
    except (json.JSONDecodeError, ValueError):
        pass
    return {}


def _pytest_benchmark_available():
    try:
        r = subprocess.run(
            [sys.executable, "-c", "import pytest_benchmark"],
            capture_output=True, timeout=10)
        return r.returncode == 0
    except Exception:
        return False


def _jmh_configured(repo_path):
    pom = Path(repo_path) / "pom.xml"
    if pom.exists():
        try:
            return "jmh" in pom.read_text().lower()
        except OSError:
            return False
    gradle = Path(repo_path) / "build.gradle"
    if gradle.exists():
        try:
            return "jmh" in gradle.read_text().lower()
        except OSError:
            return False
    return False


def _node_benchmark_script(repo_path):
    pkg_file = Path(repo_path) / "package.json"
    if not pkg_file.exists():
        return False
    try:
        with open(pkg_file) as f:
            pkg = json.load(f)
        return "benchmark" in pkg.get("scripts", {})
    except (json.JSONDecodeError, OSError):
        return False


def detect_benchmark_definitions(repo_path):
    """检测各语言是否存在 benchmark 定义（脚本 / pytest-benchmark / go benchmark 文件）。"""
    defs = {}

    if (Path(repo_path) / "go.mod").exists():
        has = False
        for f in Path(repo_path).rglob("*_test.go"):
            try:
                if "func Benchmark" in f.read_text():
                    has = True
                    break
            except OSError:
                continue
        defs["go"] = has

    if (Path(repo_path) / "pyproject.toml").exists() or (Path(repo_path) / "requirements.txt").exists():
        defs["python"] = _pytest_benchmark_available()

    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists():
        defs["java"] = _jmh_configured(repo_path)

    if (Path(repo_path) / "package.json").exists():
        defs["node"] = _node_benchmark_script(repo_path)

    return defs


def detect_languages(repo_path):
    languages = []
    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
    if (Path(repo_path) / "pyproject.toml").exists() or (Path(repo_path) / "requirements.txt").exists():
        languages.append("python")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists():
        languages.append("java")
    if (Path(repo_path) / "package.json").exists():
        languages.append("node")
    return languages


def run_go_benchmark(repo_path, artifact_dir):
    # Go 构建缓存重定向到共享临时目录：避免写 ~/Library/Caches（沙箱/CI 环境
    # 会被拒绝），也避免每个任务目录携带一份 ~200MB 的重复缓存。
    env = os.environ.copy()
    cache_dir = os.path.join(tempfile.gettempdir(), "actd-gocache-benchmark")
    try:
        os.makedirs(cache_dir, exist_ok=True)
        env["GOCACHE"] = cache_dir
    except OSError:
        pass
    try:
        proc = subprocess.run(
            ["go", "test", "-bench=.", "-benchmem", "-json", "./..."],
            cwd=repo_path, capture_output=True, text=True, timeout=300, env=env)
    except subprocess.TimeoutExpired:
        return {"status": "error", "benchmarks": [], "message": "go benchmark timed out"}

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        return {"status": "error", "benchmarks": [],
                "message": "go test -bench exited with code %d: %s" % (proc.returncode, detail[:300])}

    benchmarks = []
    for line in (proc.stdout or "").splitlines():
        if line.startswith("{"):
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if entry.get("Action") == "output" and "ns/op" in entry.get("Output", ""):
                output = entry["Output"]
                m = re.match(
                    r"(\S+)\s+(\d+)\s+(\d+\.?\d*)\s*ns/op\s+(\d+\.?\d*)\s*B/op\s+(\d+)\s*allocs/op",
                    output)
                if m:
                    benchmarks.append({
                        "name": m.group(1),
                        "iterations": int(m.group(2)),
                        "ns_per_op": float(m.group(3)),
                        "bytes_per_op": float(m.group(4)),
                        "allocs_per_op": int(m.group(5)),
                    })
    return {"status": "success", "benchmarks": benchmarks, "message": "go benchmarks executed"}


def run_python_benchmark(repo_path, artifact_dir):
    raw_path = os.path.join(artifact_dir, "benchmark-raw.json") if artifact_dir \
        else os.path.join(repo_path, ".benchmark-raw.json")
    cmd = ["pytest", "--benchmark-only", "--benchmark-json=%s" % raw_path, "-q"]
    try:
        proc = subprocess.run(cmd, cwd=repo_path, capture_output=True, text=True, timeout=300)
    except subprocess.TimeoutExpired:
        return {"status": "error", "benchmarks": [], "message": "pytest benchmark timed out"}

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        return {"status": "error", "benchmarks": [],
                "message": "pytest --benchmark-only exited with code %d: %s" % (proc.returncode, detail[:300])}

    if not os.path.isfile(raw_path):
        return {"status": "skipped", "benchmarks": [], "message": "no benchmark tests found"}

    try:
        with open(raw_path) as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        return {"status": "error", "benchmarks": [],
                "message": "failed to parse benchmark json: %s" % e}
    finally:
        try:
            os.unlink(raw_path)
        except OSError:
            pass

    benchmarks = []
    for bench in data.get("benchmarks", []):
        stats = bench.get("stats", {})
        benchmarks.append({
            "name": bench.get("name", "unknown"),
            "iterations": stats.get("iterations", 0),
            "mean_ns": stats.get("mean", 0) * 1e9,
            "min_ns": stats.get("min", 0) * 1e9,
            "max_ns": stats.get("max", 0) * 1e9,
            "stddev_ns": stats.get("stddev", 0) * 1e9,
        })
    return {"status": "success", "benchmarks": benchmarks, "message": "pytest benchmarks executed"}


def run_java_benchmark(repo_path, artifact_dir):
    try:
        proc = subprocess.run(
            ["mvn", "clean", "package", "-Pbenchmark", "-q"],
            cwd=repo_path, capture_output=True, text=True, timeout=600)
    except subprocess.TimeoutExpired:
        return {"status": "error", "benchmarks": [], "message": "JMH build timed out"}

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        return {"status": "error", "benchmarks": [],
                "message": "mvn JMH build exited with code %d: %s" % (proc.returncode, detail[:300])}

    return {"status": "success", "benchmarks": [],
            "message": "JMH benchmarks compiled; run manually"}


def run_node_benchmark(repo_path, artifact_dir):
    try:
        proc = subprocess.run(
            ["npm", "run", "benchmark", "--silent"],
            cwd=repo_path, capture_output=True, text=True, timeout=300)
    except subprocess.TimeoutExpired:
        return {"status": "error", "benchmarks": [], "message": "npm benchmark timed out"}

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        return {"status": "error", "benchmarks": [],
                "message": "npm run benchmark exited with code %d: %s" % (proc.returncode, detail[:300])}

    return {"status": "success", "benchmarks": [],
            "message": "node benchmarks executed", "output": proc.stdout}


def generate_report(results):
    """生成 Markdown 报告。results: {lang: {status, benchmarks, message}}。"""
    lines = [
        "# Benchmark Report",
        "",
        "**Generated**: %s" % datetime.now().isoformat(),
        "",
    ]
    for lang, data in results.items():
        benchmarks = data.get("benchmarks", [])
        if data.get("status") == "success" and benchmarks:
            lines.append("## %s" % lang.upper())
            lines.append("")
            lines.append("| Benchmark | Iterations | ns/op | B/op | Allocs/op |")
            lines.append("|-----------|------------|-------|------|-----------|")
            for b in benchmarks:
                name = b.get("name", "unknown")[:40]
                iters = b.get("iterations", 0)
                ns = b.get("ns_per_op", b.get("mean_ns", 0))
                bytes_op = b.get("bytes_per_op", "-")
                allocs = b.get("allocs_per_op", "-")
                lines.append("| %s | %s | %.2f | %s | %s |" % (name, iters, ns, bytes_op, allocs))
            lines.append("")
        elif data.get("status") == "success":
            lines.append("## %s" % lang.upper())
            lines.append("")
            lines.append("_%s_" % data.get("message", "executed"))
            lines.append("")
    return "\n".join(lines)


def main():
    input_data = read_input()
    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        action_result = to_action_result(
            "failed", "No repo_path provided", 0, [], "unknown", "deny")
        log("No repo_path provided")
        _emit(action_result, 1)

    if not os.path.isdir(repo_path):
        action_result = to_action_result(
            "failed", "repo_path does not exist: %s" % repo_path,
            0, [], "unknown", "deny")
        log("repo_path does not exist: %s" % repo_path)
        _emit(action_result, 1)

    languages = detect_languages(repo_path)
    lang_field = languages[0] if len(languages) == 1 else \
        ("multi" if languages else "unknown")

    if not languages:
        _emit_skipped("no supported language detected in repository", "unknown")

    # 检测 benchmark 定义（脚本 / pytest-benchmark / go benchmark 文件）
    definitions = detect_benchmark_definitions(repo_path)

    if not any(definitions.values()):
        _emit_skipped("no benchmark definitions found (no benchmark script, "
                      "pytest-benchmark, or go benchmark file)", lang_field)

    runners = {
        "go": run_go_benchmark,
        "python": run_python_benchmark,
        "java": run_java_benchmark,
        "node": run_node_benchmark,
    }

    results = {}
    for lang, has in definitions.items():
        if has:
            log("running %s benchmark" % lang)
            results[lang] = runners[lang](repo_path, artifact_dir)

    errors = {lang: r for lang, r in results.items() if r["status"] == "error"}
    successes = {lang: r for lang, r in results.items() if r["status"] == "success"}

    # 执行失败 -> failed + deny + exit 1
    if errors:
        lang, r = next(iter(errors.items()))
        action_result = to_action_result(
            "failed", "%s: %s" % (lang, r["message"]), 1, [],
            lang_field, "deny")
        log("benchmark execution failed: %s" % r["message"])
        _emit(action_result, 1)

    # 全部 skipped（例如 pytest-benchmark 存在但无实际 benchmark 测试）-> skipped
    if not successes:
        reasons = "; ".join(r["message"] for r in results.values())
        _emit_skipped("no benchmark tests executed: %s" % reasons, lang_field)

    # 成功 -> 汇总到 summary 并产出报告 artifact
    total_benchmarks = sum(len(r.get("benchmarks", [])) for r in successes.values())
    summary_msg = ("Executed %d benchmark(s) across %d language(s)"
                   % (total_benchmarks, len(successes)))

    artifacts = []
    if artifact_dir:
        try:
            os.makedirs(artifact_dir, exist_ok=True)

            report_payload = {
                "languages": list(successes.keys()),
                "results": {lang: r for lang, r in successes.items()},
                "summary": {
                    "total_benchmarks": total_benchmarks,
                    "languages_run": list(successes.keys()),
                },
            }
            json_path = os.path.join(artifact_dir, "benchmark-report.json")
            with open(json_path, "w") as f:
                json.dump(report_payload, f, indent=2)
            artifacts.append({"name": "benchmark-report.json", "path": json_path})

            md_path = os.path.join(artifact_dir, "benchmark-report.md")
            with open(md_path, "w") as f:
                f.write(generate_report(successes))
            artifacts.append({"name": "benchmark-report.md", "path": md_path})
        except OSError as e:
            log("failed to write artifacts: %s" % e)

    action_result = to_action_result(
        "success", summary_msg, 0, artifacts, lang_field, "pass",
        benchmark_count=total_benchmarks)
    log(summary_msg)
    _emit(action_result, 0)


if __name__ == "__main__":
    main()
