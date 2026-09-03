#!/usr/bin/env python3
"""
web-typecheck - TypeScript type checking gate (tsc --noEmit) for ActionD.

Execution contract:
  - The engine injects JSON {event, repo_path, artifact_dir} on stdin and also
    appends `--repo <repo_path> --out <artifact_dir>` to argv. stdin JSON takes
    priority; argv is used only as a fallback.
  - The last stdout line is a single V1 ActionResult JSON (must include
    `action_id`). All logs go to stderr. A non-zero exit code means failure.

Fail-closed / skip semantics:
  - Type errors > 0  -> status=failed + decision=deny + exit 1.
  - No tsconfig*.json or no tsc available -> intentionally skipped
    (status=success + summary notes "skipped" + exit 0), never a false failure.
  - tsc exists but errors on execution -> failed + deny + exit 1.
"""

import json
import os
import re
import shutil
import subprocess
import sys

PLUGIN_ID = "web-typecheck"
CAPABILITY = "typecheck"
LANGUAGE = "typescript"
REPORT_NAME = "tsc-report.txt"

# tsc diagnostics look like: path/to/file.ts(1,5): error TS2322: Type ...
_TS_ERROR_RE = re.compile(r"\berror\s+TS\d+\b")


def log(message):
    print("[%s] %s" % (PLUGIN_ID, message), file=sys.stderr)


def read_input():
    """Read {event, repo_path, artifact_dir} from stdin JSON."""
    try:
        return json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        return {}


def parse_argv():
    """Fallback: parse `--repo <path> --out <dir>` from argv."""
    argv = sys.argv[1:]
    data = {}
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--repo" and i + 1 < len(argv):
            data["repo_path"] = argv[i + 1]
            i += 2
        elif arg.startswith("--repo="):
            data["repo_path"] = arg[len("--repo="):]
            i += 1
        elif arg == "--out" and i + 1 < len(argv):
            data["artifact_dir"] = argv[i + 1]
            i += 2
        elif arg.startswith("--out="):
            data["artifact_dir"] = arg[len("--out="):]
            i += 1
        else:
            i += 1
    return data


def resolve_input():
    input_data = read_input()
    argv_data = parse_argv()
    repo_path = input_data.get("repo_path") or argv_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir") or argv_data.get("artifact_dir")
    event = input_data.get("event", {})
    return repo_path, artifact_dir, event


def find_tsconfigs(repo_path):
    """Return tsconfig*.json files at the repo root (sorted)."""
    found = []
    try:
        entries = os.listdir(repo_path)
    except OSError:
        return []
    for entry in sorted(entries):
        if entry.startswith("tsconfig") and entry.endswith(".json"):
            found.append(entry)
    return found


def find_tsc(repo_path):
    """Prefer repo-local tsc, then a globally available one."""
    local = os.path.join(repo_path, "node_modules", ".bin", "tsc")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("tsc")


def to_action_result(status, decision, summary_msg, issue_count, hints, artifacts):
    from datetime import datetime
    import uuid

    now = datetime.utcnow().isoformat() + "Z"
    return {
        "action_id": "act_%s" % uuid.uuid4().hex[:8],
        "plugin_id": PLUGIN_ID,
        "capability": CAPABILITY,
        "language": LANGUAGE,
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
        "hints": hints,
        "artifacts": artifacts,
        "signals": {
            "type_error_count": issue_count,
            "typecheck_passed": status == "success",
        },
        "raw_outputs": {},
        "next_actions": [],
    }


def emit(result, exit_code):
    print(json.dumps(result))
    sys.exit(exit_code)


def main():
    repo_path, artifact_dir, event = resolve_input()

    if not repo_path or not os.path.isdir(repo_path):
        summary = "repo_path 不存在或未提供: %s" % (repo_path or "<empty>")
        log(summary)
        emit(to_action_result("failed", "deny", summary, 0, [], []), 1)

    event_type = event.get("type") if isinstance(event, dict) else "unknown"
    log("processing repo=%s event=%s" % (repo_path, event_type))

    # 1. No tsconfig*.json -> not a TypeScript project, skip intentionally.
    tsconfigs = find_tsconfigs(repo_path)
    if not tsconfigs:
        summary = "skipped: 未找到 tsconfig*.json（非 TypeScript 项目）"
        log(summary)
        emit(to_action_result("success", "pass", summary, 0, [], []), 0)

    # 2. Locate tsc (local first, then global). Missing -> skip + hint.
    tsc_path = find_tsc(repo_path)
    if not tsc_path:
        hint = "tsc 未找到：请先安装依赖（npm install 以使用 node_modules/.bin/tsc，或全局安装 typescript）"
        summary = "skipped: 未找到 tsc（本地 node_modules/.bin/tsc 与全局 tsc 均缺失）"
        log(summary)
        emit(to_action_result("success", "pass", summary, 0, [hint], []), 0)

    log("using tsc: %s (tsconfig: %s)" % (tsc_path, ", ".join(tsconfigs)))

    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

    cmd = [tsc_path, "--noEmit"]
    log("running: %s in %s" % (" ".join(cmd), repo_path))
    try:
        proc = subprocess.run(
            cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=300,
        )
    except subprocess.TimeoutExpired:
        summary = "tsc --noEmit 执行超时"
        log(summary)
        emit(to_action_result("failed", "deny", summary, 1, [summary], []), 1)

    stdout = proc.stdout or ""
    stderr = proc.stderr or ""
    combined = stdout
    if stdout and stderr:
        combined += "\n"
    combined += stderr

    error_lines = [ln.strip() for ln in combined.splitlines() if _TS_ERROR_RE.search(ln)]
    issue_count = len(error_lines)
    if proc.returncode != 0 and issue_count == 0:
        # tsc failed for a reason we could not parse as a TS diagnostic;
        # fail closed rather than under-reporting.
        issue_count = 1

    # 3. Write report into artifact_dir (never into the sample repo).
    artifacts = []
    if artifact_dir:
        report_path = os.path.join(artifact_dir, REPORT_NAME)
        report_lines = [
            "$ %s" % " ".join(cmd),
            "[exit] %d" % proc.returncode,
            "",
            "[stdout]",
            stdout,
            "",
            "[stderr]",
            stderr,
        ]
        with open(report_path, "w", encoding="utf-8") as f:
            f.write("\n".join(report_lines))
        artifacts.append({"name": REPORT_NAME, "path": report_path})
        log("wrote report: %s" % report_path)

    if stderr.strip():
        print("[TSC STDERR]\n%s" % stderr, file=sys.stderr)

    # 4. Type errors > 0 -> fail-closed.
    if proc.returncode != 0 or issue_count > 0:
        hints = error_lines[:10]
        if not hints:
            hints = ["tsc 退出码 %d，类型检查未通过" % proc.returncode]
        summary = "发现 %d 个类型错误" % issue_count
        log(summary)
        emit(to_action_result("failed", "deny", summary, issue_count, hints, artifacts), 1)

    summary = "类型检查通过（0 个类型错误）"
    log(summary)
    emit(to_action_result("success", "pass", summary, 0, [], artifacts), 0)


if __name__ == "__main__":
    main()
