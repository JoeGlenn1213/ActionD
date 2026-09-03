#!/usr/bin/env python3
import sys
import json
import subprocess
import os
import shutil
import tempfile

def to_action_result(old_result: dict, plugin_id: str, capability: str, issue_count: int) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    # Map status
    if status == "error":
        status = "failed"
        
    # Decision depends on issue count for lint
    decision = "pass" if issue_count == 0 else "deny"
    if status == "failed":
        decision = "deny"
    
    summary_msg = old_result.get("summary", "Lint executed successfully")

    now = datetime.utcnow().isoformat() + "Z"

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "go",
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
            "message": summary_msg,
            "counts": {
                "issues": issue_count
            }
        },
        "hints": [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", [])],
        "signals": {
            "lint_error_count": issue_count
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result


def _parse_json_from_output(raw_output: str):
    """
    Extract the first JSON object/array from mixed output.

    golangci-lint v2 appends a human-readable summary line (e.g. "0 issues.")
    after the JSON report even when ``--output.json.path stdout`` is set, so a
    naive ``json.loads`` over the whole stdout would fail. We scan for the first
    JSON value and return it (or None when nothing parseable is present).
    """
    if not raw_output or not raw_output.strip():
        return None
    decoder = json.JSONDecoder()
    for idx, ch in enumerate(raw_output):
        if ch in "{[":
            try:
                return decoder.raw_decode(raw_output, idx)[0]
            except (ValueError, TypeError):
                continue
    return None


def _count_issues(parsed) -> int:
    """Count lint issues from a parsed golangci-lint JSON report."""
    if isinstance(parsed, dict):
        issues = parsed.get("Issues")
        if isinstance(issues, list):
            return len(issues)
        return 0
    if isinstance(parsed, list):
        return len(parsed)
    return 0


def _emit(action_result: dict, exit_code: int):
    """Emit the ActionResult JSON on stdout and exit with the given code."""
    print(json.dumps(action_result))
    sys.exit(exit_code)


def main():
    # 1. Read input JSON from stdin
    try:
        input_data = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        print(json.dumps({"status": "failed", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "failed", "error": "No repo_path provided"}))
        sys.exit(1)

    if not os.path.isdir(repo_path):
        print(json.dumps({"status": "failed", "error": f"repo_path does not exist: {repo_path}"}))
        sys.exit(1)

    # 2. Not a Go repository -> intentionally skip (success, not a failure)
    if not os.path.isfile(os.path.join(repo_path, "go.mod")):
        response = {
            "status": "success",
            "artifacts": [],
            "summary": "skipped: not a Go repository (no go.mod found)",
        }
        action_result = to_action_result(response, plugin_id="go-lint", capability="lint", issue_count=0)
        _emit(action_result, 0)

    # 3. Prepare a writable cache location so lint tools don't touch ~/Library/Caches
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

    env = os.environ.copy()
    # 共享缓存目录: 避免每个任务目录携带一份 ~200MB 的重复 Go/lint 缓存
    # (曾导致 ~/.localgithub/actions 膨胀到 10GB)。Go 与 golangci-lint 缓存
    # 均为内容寻址, 跨任务共享是安全的。
    env["GOLANGCI_LINT_CACHE"] = os.path.join(tempfile.gettempdir(), "actd-lintcache-go-lint")
    env["GOCACHE"] = os.path.join(tempfile.gettempdir(), "actd-gocache-go-lint")

    saved_artifacts = []

    # 4. golangci-lint primary path (fail-closed on issues)
    if shutil.which("golangci-lint"):
        cmd = ["golangci-lint", "run", "./...", "--output.json.path", "stdout"]
        proc = subprocess.run(cmd, cwd=repo_path, env=env, capture_output=True, text=True)
        lint_output = proc.stdout
        lint_error = proc.stderr
        returncode = proc.returncode

        parsed = _parse_json_from_output(lint_output)
        issue_count = _count_issues(parsed)

        if artifact_dir:
            # Save a valid JSON report (re-dump the parsed report so the
            # trailing human summary line does not corrupt the file).
            report_content = json.dumps(parsed, indent=2) if parsed is not None else lint_output
            report_path = os.path.join(artifact_dir, "lint-report.json")
            with open(report_path, "w") as f:
                f.write(report_content)
            saved_artifacts.append("lint-report.json")

            if lint_error.strip():
                log_path = os.path.join(artifact_dir, "lint-error.log")
                with open(log_path, "w") as f:
                    f.write(lint_error)
                saved_artifacts.append("lint-error.log")

        if returncode == 1 or issue_count > 0:
            # Issues found: fail-closed.
            status = "failed"
            summary = f"Found {issue_count} lint issues"
            exit_code = 1
        elif returncode != 0:
            # Lint tool itself errored (config/internal error, build failure).
            # Fail-closed: we cannot verify, so block.
            status = "failed"
            detail = (lint_error or lint_output).strip()
            summary = f"golangci-lint exited with code {returncode}: {detail[:300]}"
            exit_code = 1
        else:
            status = "success"
            summary = "No lint issues"
            exit_code = 0

        response = {"status": status, "artifacts": saved_artifacts, "summary": summary}
        action_result = to_action_result(response, plugin_id="go-lint", capability="lint", issue_count=issue_count)
        _emit(action_result, exit_code)

    # 5. Fallback: golangci-lint unavailable -> degrade to go vet ./...
    if shutil.which("go"):
        cmd = ["go", "vet", "./..."]
        proc = subprocess.run(cmd, cwd=repo_path, env=env, capture_output=True, text=True)
        vet_output = ((proc.stdout or "") + (proc.stderr or "")).strip()
        returncode = proc.returncode

        has_output = bool(vet_output)
        failed = returncode != 0 or has_output

        if artifact_dir and vet_output:
            log_path = os.path.join(artifact_dir, "go-vet.log")
            with open(log_path, "w") as f:
                f.write(vet_output)
            saved_artifacts.append("go-vet.log")

        if failed:
            # count non-empty output lines as a best-effort issue count
            issue_count = len([ln for ln in vet_output.splitlines() if ln.strip()])
            if issue_count == 0:
                issue_count = 1
            status = "failed"
            summary = "golangci-lint unavailable; go vet reported issues"
            exit_code = 1
        else:
            issue_count = 0
            status = "success"
            summary = "golangci-lint unavailable; go vet clean"
            exit_code = 0

        response = {"status": status, "artifacts": saved_artifacts, "summary": summary}
        action_result = to_action_result(response, plugin_id="go-lint", capability="lint", issue_count=issue_count)
        _emit(action_result, exit_code)

    # 6. Neither tool available -> fail-closed (cannot verify)
    response = {
        "status": "failed",
        "artifacts": saved_artifacts,
        "summary": "no Go lint tool available (golangci-lint and go vet are both missing)",
    }
    action_result = to_action_result(response, plugin_id="go-lint", capability="lint", issue_count=0)
    _emit(action_result, 1)


if __name__ == "__main__":
    main()
