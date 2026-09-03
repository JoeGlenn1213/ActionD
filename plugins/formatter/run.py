#!/usr/bin/env python3
"""
formatter - 代码格式化检查（check-only）

CI 语义：只检查、绝不修改文件。发现需要格式化的文件 -> failed + deny + exit 1；
工具缺失/无法判定 -> skipped（success + exit 0，绝不误报失败）；
工具存在但执行报错 -> failed + deny + exit 1。

支持:
- Go: gofmt, goimports
- Python: black, isort, ruff format
- Java: mvn spotless:check / gradlew spotlessCheck
- TypeScript/JavaScript: npx prettier --check

输出契约: stdout 最后一行输出单个 V1 ActionResult JSON（含 action_id）；日志一律 stderr。
"""

import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

PLUGIN_ID = "formatter"
CAPABILITY = "lint"


def log(message):
    """打印日志到 stderr，保证 stdout 只输出 ActionResult JSON。"""
    print("[formatter] %s" % message, file=sys.stderr)


def to_action_result(status, summary_msg, issue_count, artifacts,
                     language, decision):
    """构造 V1 ActionResult（与 go-lint/java-checkstyle 的 to_action_result 对齐）。"""
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
            "files_needing_formatting": issue_count,
            "check_only": True,
        },
        "raw_outputs": {},
        "next_actions": [],
    }


def _emit(action_result, exit_code):
    """stdout 输出单个 ActionResult JSON 后按给定退出码退出。"""
    print(json.dumps(action_result))
    sys.exit(exit_code)


def _emit_skipped(reason, language):
    """故意跳过：success + pass + exit 0，summary 注明 skipped。"""
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


def _reformat_count(combined_output):
    """从格式化工具的 check 输出中估算需要重排的文件数。"""
    if not combined_output:
        return 0
    # 汇总行: "N file(s) would be reformatted"（ruff/black/isort 通用）
    m = re.search(r"(\d+)\s+files?\s+would\s+be\s+reformatted",
                  combined_output, re.IGNORECASE)
    if m:
        return int(m.group(1))
    # black 的逐文件行: "would reformat <path>"
    n = len([ln for ln in combined_output.splitlines()
             if ln.strip().startswith("would reformat")])
    if n:
        return n
    # ruff --check 的逐文件行: "unformatted: File would be reformatted"
    n = len([ln for ln in combined_output.splitlines()
             if "unformatted: File would be reformatted" in ln])
    if n:
        return n
    # isort 的逐文件行: "ERROR: ... Imports are incorrectly sorted ..."
    n = len([ln for ln in combined_output.splitlines()
             if "Imports are incorrectly sorted" in ln])
    if n:
        return n
    return 0


def check_go(repo_path):
    """Go 格式化检查（gofmt/goimports，check-only）。"""
    if not (Path(repo_path) / "go.mod").exists():
        return ("skipped", 0, "", "not a Go repository")

    tools = [t for t in ("gofmt", "goimports") if shutil.which(t)]
    if not tools:
        return ("skipped", 0, "", "no Go formatter available (gofmt/goimports missing)")

    files_needing = set()
    patches = []
    for tool in tools:
        try:
            listing = subprocess.run(
                [tool, "-l", "."], cwd=repo_path,
                capture_output=True, text=True, timeout=120)
        except FileNotFoundError:
            continue
        except subprocess.TimeoutExpired:
            return ("error", 0, "", "%s timed out" % tool)

        if listing.returncode != 0:
            detail = (listing.stderr or listing.stdout or "").strip()
            return ("error", 0, "",
                    "%s -l exited with code %d: %s" % (tool, listing.returncode, detail[:300]))

        found = {ln.strip() for ln in (listing.stdout or "").splitlines() if ln.strip()}
        files_needing |= found

        if found:
            try:
                diff = subprocess.run(
                    [tool, "-d", "."], cwd=repo_path,
                    capture_output=True, text=True, timeout=120)
                if diff.stdout:
                    patches.append("--- %s diff\n%s" % (tool, diff.stdout))
            except Exception:
                pass

    if files_needing:
        return ("violations", len(files_needing), "\n".join(patches),
                "Go: %d file(s) need formatting" % len(files_needing))
    return ("success", 0, "", "Go formatting clean")


def check_python(repo_path):
    """Python 格式化检查（black/isort/ruff format，check-only）。"""
    has_py = bool(list(Path(repo_path).glob("*.py"))) or \
        (Path(repo_path) / "pyproject.toml").exists()
    if not has_py:
        return ("skipped", 0, "", "no Python files detected")

    checks = []
    if shutil.which("black"):
        checks.append(("black", ["black", "--check", "--diff", "."]))
    if shutil.which("isort"):
        checks.append(("isort", ["isort", "--check-only", "--diff", "."]))
    if shutil.which("ruff"):
        checks.append(("ruff", ["ruff", "format", "--check", "--diff", "."]))

    if not checks:
        return ("skipped", 0, "",
                "no Python formatter available (black/isort/ruff missing)")

    total_issues = 0
    patches = []
    errors = []

    for name, cmd in checks:
        try:
            proc = subprocess.run(cmd, cwd=repo_path,
                                  capture_output=True, text=True, timeout=180)
        except FileNotFoundError:
            continue
        except subprocess.TimeoutExpired:
            errors.append("%s timed out" % name)
            continue

        combined = (proc.stdout or "") + "\n" + (proc.stderr or "")

        if proc.returncode == 0:
            continue
        if proc.returncode == 1:
            n = _reformat_count(combined)
            total_issues += max(n, 1)
            if proc.stdout:
                patches.append("--- %s diff\n%s" % (name, proc.stdout))
        else:
            errors.append("%s exited with code %d: %s" %
                          (name, proc.returncode, combined.strip()[:300]))

    if errors:
        return ("error", total_issues, "\n".join(patches), "; ".join(errors))
    if total_issues:
        return ("violations", total_issues, "\n".join(patches),
                "Python: %d file(s) need formatting" % total_issues)
    return ("success", 0, "", "Python formatting clean")


def _pom_has_plugin(repo_path, artifact_id):
    """pom.xml 是否声明了指定 Maven 插件（文本级检测，足够稳健）。"""
    pom = Path(repo_path) / "pom.xml"
    try:
        text = pom.read_text(encoding="utf-8", errors="ignore")
    except OSError:
        return False
    return "<artifactId>%s</artifactId>" % artifact_id in text


def _gradle_has_marker(repo_path, marker):
    """build.gradle(.kts) 是否包含指定标记（如 spotless）。"""
    for name in ("build.gradle", "build.gradle.kts"):
        p = Path(repo_path) / name
        try:
            if marker in p.read_text(encoding="utf-8", errors="ignore"):
                return True
        except OSError:
            continue
    return False


def check_java(repo_path):
    """Java 格式化检查（spotless check-only）。"""
    pom = (Path(repo_path) / "pom.xml").exists()
    gradle = (Path(repo_path) / "build.gradle").exists() or \
        (Path(repo_path) / "build.gradle.kts").exists()
    if not (pom or gradle):
        return ("skipped", 0, "", "not a Java repository")

    if pom:
        # 无 spotless 配置的 Maven 项目直接跳过，避免把 BUILD FAILURE
        # (No plugin found for prefix 'spotless') 误判为"文件需格式化"。
        if not _pom_has_plugin(repo_path, "spotless-maven-plugin"):
            return ("skipped", 0, "", "pom.xml 未配置 spotless 插件")
        if not shutil.which("mvn"):
            return ("skipped", 0, "", "no Java formatter available (mvn missing)")
        cmd = ["mvn", "spotless:check"]
    else:
        if not _gradle_has_marker(repo_path, "spotless"):
            return ("skipped", 0, "", "build.gradle 未配置 spotless")
        gradlew = os.path.join(repo_path, "gradlew")
        if not os.path.isfile(gradlew):
            return ("skipped", 0, "", "no Java formatter available (gradlew missing)")
        cmd = ["./gradlew", "spotlessCheck"]

    try:
        proc = subprocess.run(cmd, cwd=repo_path,
                              capture_output=True, text=True, timeout=300)
    except FileNotFoundError:
        return ("skipped", 0, "", "no Java formatter available")
    except subprocess.TimeoutExpired:
        return ("error", 0, "", "Java formatter timed out")

    combined = (proc.stdout or "") + "\n" + (proc.stderr or "")
    if proc.returncode == 0:
        return ("success", 0, "", "Java formatting clean")
    # 防御：mvn 因插件前缀缺失而失败不是格式违规。
    if "No plugin found for prefix" in combined or \
            "Could not find or load main class" in combined:
        return ("skipped", 0, "", "spotless 不可用（未配置或无法解析）")
    # spotless 用非零退出码表示存在未格式化文件（fail-closed）。
    return ("violations", 1, combined,
            "Java: formatting check failed (spotless reported issues)")


def check_typescript(repo_path):
    """TypeScript/JavaScript 格式化检查（prettier --check，check-only）。"""
    if not (Path(repo_path) / "package.json").exists():
        return ("skipped", 0, "", "not a Node/web repository")

    if not shutil.which("npx"):
        return ("skipped", 0, "", "no web formatter available (npx missing)")

    try:
        proc = subprocess.run(
            ["npx", "prettier", "--check", "."], cwd=repo_path,
            capture_output=True, text=True, timeout=180)
    except FileNotFoundError:
        return ("skipped", 0, "", "prettier not available")
    except subprocess.TimeoutExpired:
        return ("error", 0, "", "prettier timed out")

    combined = (proc.stdout or "") + "\n" + (proc.stderr or "")
    if proc.returncode == 0:
        return ("success", 0, "", "web formatting clean")
    if proc.returncode == 1:
        n = len([ln for ln in combined.splitlines()
                 if ln.strip().startswith("[warn]")])
        return ("violations", max(n, 1), combined,
                "web: %d file(s) need formatting" % max(n, 1))
    return ("error", 0, combined,
            "prettier exited with code %d: %s" % (proc.returncode, combined.strip()[:300]))


def detect_languages(repo_path):
    """检测仓库语言（与原有检测逻辑保持一致）。"""
    languages = []
    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
    if list(Path(repo_path).glob("*.py")) or (Path(repo_path) / "pyproject.toml").exists():
        languages.append("python")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists() \
            or (Path(repo_path) / "build.gradle.kts").exists():
        languages.append("java")
    if (Path(repo_path) / "package.json").exists():
        languages.append("typescript")
    return languages


def write_artifacts(artifact_dir, per_lang, all_patches):
    """把报告和 patch 写入 artifact_dir，返回 [{name, path}] 真实产出路径。"""
    artifacts = []
    if not artifact_dir:
        return artifacts
    try:
        os.makedirs(artifact_dir, exist_ok=True)

        report = {lang: {"status": r[0], "issues": r[1], "message": r[3]}
                  for lang, r in per_lang.items()}
        report_path = os.path.join(artifact_dir, "format-report.json")
        with open(report_path, "w") as f:
            json.dump(report, f, indent=2)
        artifacts.append({"name": "format-report.json", "path": report_path})

        if all_patches:
            patch_path = os.path.join(artifact_dir, "format-patch.diff")
            with open(patch_path, "w") as f:
                f.write("\n".join(all_patches))
            artifacts.append({"name": "format-patch.diff", "path": patch_path})
    except OSError as e:
        log("failed to write artifacts: %s" % e)
    return artifacts


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

    # 各语言 check-only 检查
    per_lang = {}
    runners = {
        "go": check_go,
        "python": check_python,
        "java": check_java,
        "typescript": check_typescript,
    }
    for lang in languages:
        per_lang[lang] = runners[lang](repo_path)

    # 汇总判定
    error_results = {lang: r for lang, r in per_lang.items() if r[0] == "error"}
    violation_results = {lang: r for lang, r in per_lang.items() if r[0] == "violations"}
    ran_results = {lang: r for lang, r in per_lang.items()
                   if r[0] in ("success", "violations")}

    total_issues = sum(r[1] for r in per_lang.values())
    all_patches = [r[2] for r in per_lang.values() if r[2]]

    if error_results:
        # 工具存在但执行报错 -> failed + deny + exit 1
        lang, r = next(iter(error_results.items()))
        artifacts = write_artifacts(artifact_dir, per_lang, all_patches)
        action_result = to_action_result(
            "failed", "%s: %s" % (lang, r[3]), total_issues,
            artifacts, lang_field, "deny")
        log("formatter error -> deny: %s" % r[3])
        _emit(action_result, 1)

    if violation_results:
        # 格式化违规 -> failed + deny + exit 1
        artifacts = write_artifacts(artifact_dir, per_lang, all_patches)
        action_result = to_action_result(
            "failed",
            "Found %d file(s) needing formatting" % total_issues,
            total_issues, artifacts, lang_field, "deny")
        log("formatter violations -> deny: %d file(s)" % total_issues)
        _emit(action_result, 1)

    if not ran_results:
        # 检测到语言但没有任何可用格式化工具 -> skipped
        reasons = "; ".join(r[3] for r in per_lang.values())
        _emit_skipped("no formatter tool available: %s" % reasons, lang_field)

    # 全部通过
    artifacts = write_artifacts(artifact_dir, per_lang, all_patches)
    action_result = to_action_result(
        "success", "Formatting check passed (0 file(s) needing formatting)",
        0, artifacts, lang_field, "pass")
    log("formatter clean -> pass")
    _emit(action_result, 0)


if __name__ == "__main__":
    main()
