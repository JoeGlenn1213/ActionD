#!/usr/bin/env python3
"""
dependency-graph - 生成跨语言模块依赖图

功能:
- Go: go mod graph
- Python: pipdeptree / requirements.txt / pyproject.toml
- Java: mvn dependency:tree / gradlew dependencies
- Node: package.json / npm ls

输出:
- dependency-graph.json: 结构化依赖数据
- dependency-graph.mmd: Mermaid 图

契约: stdout 最后一行输出单个 V1 ActionResult JSON（含 action_id）；日志一律 stderr。
图生成失败 -> failed + deny + exit 1；成功 -> artifacts 列真实产出路径；
不适用/工具缺失 -> skipped（success + exit 0）。
"""

import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

PLUGIN_ID = "dependency-graph"
CAPABILITY = "utility"


def log(message):
    print("[dependency-graph] %s" % message, file=sys.stderr)


def to_action_result(status, summary_msg, issue_count, artifacts,
                     language, decision, graph_nodes=0, direct_deps=0):
    from datetime import datetime
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
            "counts": {"issues": issue_count},
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": artifacts,
        "signals": {
            "graph_nodes": graph_nodes,
            "direct_dependencies": direct_deps,
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


def parse_go_deps(repo_path):
    """解析 Go 依赖。返回 {status, direct, graph, message}。"""
    if not (Path(repo_path) / "go.mod").exists():
        return {"status": "skipped", "direct": [], "graph": {}, "message": "not a Go repository"}

    if not shutil.which("go"):
        return {"status": "skipped", "direct": [], "graph": {},
                "message": "go toolchain not available"}

    try:
        proc = subprocess.run(
            ["go", "mod", "graph"], cwd=repo_path,
            capture_output=True, text=True, timeout=60)
    except subprocess.TimeoutExpired:
        return {"status": "error", "direct": [], "graph": {},
                "message": "go mod graph timed out"}

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        return {"status": "error", "direct": [], "graph": {},
                "message": "go mod graph exited with code %d: %s" % (proc.returncode, detail[:300])}

    graph = {}
    for line in (proc.stdout or "").strip().splitlines():
        if " " in line:
            parts = line.split(" ")
            if len(parts) >= 2:
                source, target = parts[0], parts[1]
                graph.setdefault(source, []).append(target)

    direct = []
    go_mod = Path(repo_path) / "go.mod"
    if go_mod.exists():
        content = go_mod.read_text()
        in_require = False
        for line in content.splitlines():
            if "require (" in line:
                in_require = True
                continue
            if in_require and ")" in line:
                in_require = False
                continue
            if in_require and line.strip() and not line.strip().startswith("//"):
                parts = line.strip().split()
                if len(parts) >= 2:
                    direct.append({"name": parts[0], "version": parts[1]})

    return {"status": "success", "direct": direct, "graph": graph,
            "message": "Go dependencies parsed"}


def parse_python_deps(repo_path):
    """解析 Python 依赖。pipdeptree 优先，requirements.txt / pyproject.toml 兜底。"""
    if shutil.which("pipdeptree"):
        try:
            proc = subprocess.run(
                ["pipdeptree", "--json-tree"], cwd=repo_path,
                capture_output=True, text=True, timeout=60)
            if proc.returncode == 0 and proc.stdout.strip():
                tree = json.loads(proc.stdout)
                direct = []
                graph = {}

                def add_children(parent, children):
                    for child in children:
                        child_name = child.get("package", {}).get("package_name", "unknown")
                        graph.setdefault(parent, []).append(child_name)
                        add_children(child_name, child.get("dependencies", []))

                for pkg in tree:
                    name = pkg.get("package", {}).get("package_name", "unknown")
                    direct.append({
                        "name": name,
                        "version": pkg.get("package", {}).get("installed_version", "unknown"),
                    })
                    add_children(name, pkg.get("dependencies", []))
                return {"status": "success", "direct": direct, "graph": graph,
                        "message": "Python dependencies parsed via pipdeptree"}
            # pipdeptree 返回非零/空输出/异常 -> 继续兜底
        except Exception:
            pass

    # 兜底：requirements.txt
    req_file = Path(repo_path) / "requirements.txt"
    if req_file.exists():
        direct = []
        for line in req_file.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#"):
                m = re.match(r"^([a-zA-Z0-9_.-]+)", line)
                if m:
                    direct.append({"name": m.group(1), "version": "unknown"})
        return {"status": "success", "direct": direct, "graph": {},
                "message": "Python dependencies parsed from requirements.txt"}

    # 兜底：pyproject.toml [project].dependencies
    pyproject = Path(repo_path) / "pyproject.toml"
    if pyproject.exists():
        direct = []
        content = pyproject.read_text()
        in_deps = False
        for line in content.splitlines():
            stripped = line.strip()
            if stripped.startswith("dependencies") and "=" in stripped:
                in_deps = True
                continue
            if in_deps:
                if stripped.startswith("[") and stripped.endswith("]"):
                    in_deps = False
                    continue
                dep = stripped.strip("[],\"' ")
                if dep and not dep.startswith("#"):
                    # 去掉版本说明符，仅保留包名
                    name = re.split(r"[<>=!~\s]", dep)[0].strip()
                    if name:
                        direct.append({"name": name, "version": "unknown"})
        return {"status": "success", "direct": direct, "graph": {},
                "message": "Python dependencies parsed from pyproject.toml"}

    return {"status": "skipped", "direct": [], "graph": {},
            "message": "no Python dependency source (pipdeptree/requirements.txt/pyproject.toml missing)"}


def parse_java_deps(repo_path):
    """解析 Java 依赖（简化）。"""
    pom = (Path(repo_path) / "pom.xml").exists()
    gradle = (Path(repo_path) / "build.gradle").exists() or \
        (Path(repo_path) / "build.gradle.kts").exists()
    if not (pom or gradle):
        return {"status": "skipped", "direct": [], "graph": {}, "message": "not a Java repository"}

    direct = []
    try:
        if pom:
            if not shutil.which("mvn"):
                return {"status": "skipped", "direct": [], "graph": {},
                        "message": "mvn not available"}
            proc = subprocess.run(
                ["mvn", "dependency:tree", "-DoutputType=json"], cwd=repo_path,
                capture_output=True, text=True, timeout=300)
            if proc.returncode != 0:
                detail = (proc.stderr or proc.stdout or "").strip()
                return {"status": "error", "direct": [], "graph": {},
                        "message": "mvn dependency:tree failed: %s" % detail[:300]}
            return {"status": "success", "direct": direct, "graph": {},
                    "message": "Maven dependency tree parsed (simplified)"}
        else:
            gradlew = os.path.join(repo_path, "gradlew")
            if not os.path.isfile(gradlew):
                return {"status": "skipped", "direct": [], "graph": {},
                        "message": "gradlew not available"}
            proc = subprocess.run(
                ["./gradlew", "dependencies", "--configuration", "implementation"],
                cwd=repo_path, capture_output=True, text=True, timeout=300)
            if proc.returncode != 0:
                detail = (proc.stderr or proc.stdout or "").strip()
                return {"status": "error", "direct": [], "graph": {},
                        "message": "gradlew dependencies failed: %s" % detail[:300]}
            for line in (proc.stdout or "").splitlines():
                if "\\---" in line or "+---" in line:
                    parts = line.replace("\\---", "").replace("+---", "").strip().split(":")
                    if len(parts) >= 2:
                        direct.append({
                            "group": parts[0],
                            "name": parts[1],
                            "version": parts[2] if len(parts) > 2 else "unknown",
                        })
            return {"status": "success", "direct": direct, "graph": {},
                    "message": "Gradle dependencies parsed (simplified)"}
    except subprocess.TimeoutExpired:
        return {"status": "error", "direct": [], "graph": {},
                "message": "Java dependency resolution timed out"}
    except Exception as e:
        return {"status": "error", "direct": [], "graph": {},
                "message": "Java dependency resolution error: %s" % e}


def parse_node_deps(repo_path):
    """解析 Node 依赖（package.json 优先，npm ls 增强）。"""
    pkg_file = Path(repo_path) / "package.json"
    if not pkg_file.exists():
        return {"status": "skipped", "direct": [], "graph": {}, "message": "not a Node repository"}

    direct = []
    dev = []
    graph = {}
    try:
        with open(pkg_file) as f:
            pkg = json.load(f)
        for name, version in pkg.get("dependencies", {}).items():
            direct.append({"name": name, "version": version})
        for name, version in pkg.get("devDependencies", {}).items():
            dev.append({"name": name, "version": version})
    except (json.JSONDecodeError, OSError) as e:
        return {"status": "error", "direct": [], "graph": {},
                "message": "failed to parse package.json: %s" % e}

    # npm ls 仅作增强，失败不影响主结果（package.json 已能给出直接依赖）。
    if shutil.which("npm"):
        try:
            proc = subprocess.run(
                ["npm", "ls", "--json", "--all"], cwd=repo_path,
                capture_output=True, text=True, timeout=60)
            if proc.returncode == 0 and proc.stdout.strip():
                tree = json.loads(proc.stdout)
                graph = tree.get("dependencies", {})
        except Exception:
            pass

    result = {"status": "success", "direct": direct, "graph": graph,
              "message": "Node dependencies parsed from package.json"}
    if dev:
        result["dev"] = dev
    return result


def generate_mermaid(graph):
    """生成 Mermaid 图（限制大小）。"""
    lines = ["graph TD"]
    for source, targets in graph.items():
        if not isinstance(targets, list):
            continue
        for target in targets:
            s = source.split("@")[0].replace("/", "_").replace("-", "_")
            t = str(target).split("@")[0].replace("/", "_").replace("-", "_")
            lines.append("    %s --> %s" % (s, t))
    return "\n".join(lines[:100])


def detect_languages(repo_path):
    languages = []
    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
    if (Path(repo_path) / "pyproject.toml").exists() or (Path(repo_path) / "requirements.txt").exists():
        languages.append("python")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists() \
            or (Path(repo_path) / "build.gradle.kts").exists():
        languages.append("java")
    if (Path(repo_path) / "package.json").exists():
        languages.append("node")
    return languages


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

    parsers = {
        "go": parse_go_deps,
        "python": parse_python_deps,
        "java": parse_java_deps,
        "node": parse_node_deps,
    }

    per_lang = {}
    for lang in languages:
        per_lang[lang] = parsers[lang](repo_path)

    failed = {lang: r for lang, r in per_lang.items() if r["status"] == "error"}
    succeeded = {lang: r for lang, r in per_lang.items() if r["status"] == "success"}

    # 图生成失败 -> failed + deny + exit 1
    if failed:
        lang, r = next(iter(failed.items()))
        action_result = to_action_result(
            "failed", "%s: %s" % (lang, r["message"]), 1, [],
            lang_field, "deny")
        log("graph generation failed: %s" % r["message"])
        _emit(action_result, 1)

    # 全部跳过（工具缺失/不适用）-> skipped
    if not succeeded:
        reasons = "; ".join(r["message"] for r in per_lang.values())
        _emit_skipped("no dependency source available: %s" % reasons, lang_field)

    # 汇总成功语言
    full_graph = {}
    for lang, r in succeeded.items():
        g = r.get("graph", {})
        if isinstance(g, dict):
            full_graph.update(g)

    total_direct = sum(len(r.get("direct", [])) for r in succeeded.values())
    graph_nodes = len(full_graph)

    summary_msg = ("Generated dependency graph across %d language(s), "
                   "%d direct dependencies, %d graph nodes"
                   % (len(succeeded), total_direct, graph_nodes))

    mermaid = generate_mermaid(full_graph)

    # 写 artifacts（真实产出路径）
    artifacts = []
    if artifact_dir:
        try:
            os.makedirs(artifact_dir, exist_ok=True)

            payload = {
                "languages": list(succeeded.keys()),
                "dependencies": {lang: r for lang, r in succeeded.items()},
                "graph_nodes": graph_nodes,
                "total_direct_dependencies": total_direct,
                "mermaid": mermaid,
            }
            graph_path = os.path.join(artifact_dir, "dependency-graph.json")
            with open(graph_path, "w") as f:
                json.dump(payload, f, indent=2)
            artifacts.append({"name": "dependency-graph.json", "path": graph_path})

            mermaid_path = os.path.join(artifact_dir, "dependency-graph.mmd")
            with open(mermaid_path, "w") as f:
                f.write(mermaid)
            artifacts.append({"name": "dependency-graph.mmd", "path": mermaid_path})
        except OSError as e:
            log("failed to write artifacts: %s" % e)

    action_result = to_action_result(
        "success", summary_msg, 0, artifacts, lang_field, "pass",
        graph_nodes=graph_nodes, direct_deps=total_direct)
    log(summary_msg)
    _emit(action_result, 0)


if __name__ == "__main__":
    main()
