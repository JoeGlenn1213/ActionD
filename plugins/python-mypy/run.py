#!/usr/bin/env python3
"""
python-mypy - static type checking gate for Python projects.

Executes mypy against the repository, honouring project-level configuration
(mypy.ini / .mypy.ini / pyproject.toml [tool.mypy] / setup.cfg [mypy]) which
mypy auto-discovers from the working directory. Fails closed when type errors
are found. When mypy is unavailable the gate intentionally skips (success) so
a missing local tool never fabricates a pipeline failure.
"""

import json
import os
import subprocess
import sys
import tempfile
import uuid
from datetime import datetime, timezone

PLUGIN_ID = "python-mypy"
CAPABILITY = "typecheck"
LANGUAGE = "python"
REPORT_NAME = "mypy-report.txt"

# mypy default error line format: "path:line: error: message  [error-code]".
# Notes are "path:line: note: ..." and never count as errors.
ERROR_MARKER = ": error:"


def log(message):
    """Log to stderr only; stdout is reserved for the final ActionResult JSON."""
    print("[python-mypy] %s" % message, file=sys.stderr)


def _argv_input():
    """Parse the engine's argv fallback (--repo <path> --out <dir>)."""
    data = {}
    argv = sys.argv[1:]
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--repo" and i + 1 < len(argv):
            data["repo_path"] = argv[i + 1]
            i += 2
            continue
        if arg == "--out" and i + 1 < len(argv):
            data["artifact_dir"] = argv[i + 1]
            i += 2
            continue
        i += 1
    return data


def read_input():
    """Read {event, repo_path, artifact_dir} preferring stdin JSON, argv as fallback."""
    data = _argv_input()
    if not sys.stdin.isatty():
        try:
            raw = sys.stdin.read()
            if raw and raw.strip():
                parsed = json.loads(raw)
                if isinstance(parsed, dict):
                    data.update(parsed)  # stdin JSON wins over argv
        except (json.JSONDecodeError, ValueError) as exc:
            log("invalid JSON on stdin (%s); falling back to argv" % exc)
    return data


def action_result(status, decision, message, issue_count, hints, artifacts):
    """Build the V1 ActionResult (must contain action_id)."""
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
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
            "message": message,
            "counts": {"issues": issue_count},
        },
        "hints": hints,
        "artifacts": artifacts,
        "signals": {"mypy_error_count": issue_count},
        "raw_outputs": {},
        "next_actions": [],
    }


def emit(result, exit_code):
    """Print the single ActionResult JSON to stdout and exit."""
    print(json.dumps(result))
    sys.exit(exit_code)


def mypy_available():
    """Return True when `python -m mypy` runs on the executing interpreter."""
    probe = subprocess.run(
        [sys.executable, "-m", "mypy", "--version"],
        capture_output=True,
        text=True,
    )
    return probe.returncode == 0


def write_report(artifact_dir, content):
    """Write the report into artifact_dir and return its artifact entry."""
    os.makedirs(artifact_dir, exist_ok=True)
    report_path = os.path.join(artifact_dir, REPORT_NAME)
    with open(report_path, "w") as f:
        f.write(content)
    return {"name": REPORT_NAME, "path": report_path}


def main():
    input_data = read_input()
    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if isinstance(input_data.get("event"), dict):
        log("event: %s" % input_data["event"].get("type", "unknown"))

    # Fail-closed on a missing/invalid repo path.
    if not isinstance(repo_path, str) or not repo_path.strip():
        result = action_result(
            "failed", "deny", "No repo_path provided", 0, ["repo_path is required"], []
        )
        emit(result, 1)

    if not os.path.isdir(repo_path):
        result = action_result(
            "failed", "deny",
            "repo_path does not exist: %s" % repo_path, 0, [], []
        )
        emit(result, 1)

    # Never write into the sample repo; reports land in artifact_dir only.
    if not isinstance(artifact_dir, str) or not artifact_dir.strip():
        artifact_dir = os.path.join(tempfile.gettempdir(), "actd-mypy-artifacts")
        log("artifact_dir not provided; using %s" % artifact_dir)

    # Tool availability gate: missing mypy -> intentionally skip (not a failure).
    if not mypy_available():
        message = "skipped: mypy is not available (pip install mypy)"
        log(message)
        report = write_report(
            artifact_dir,
            "SKIPPED: mypy not available.\nFix: pip install mypy\n",
        )
        result = action_result(
            "success", "pass", message, 0, ["pip install mypy"], [report]
        )
        emit(result, 0)

    cmd = [sys.executable, "-m", "mypy", repo_path]
    log("running: %s" % " ".join(cmd))
    try:
        proc = subprocess.run(
            cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=270,  # under the manifest's 5m ceiling
        )
    except subprocess.TimeoutExpired:
        message = "mypy timed out after 270s"
        log(message)
        report = write_report(artifact_dir, message + "\n")
        result = action_result("failed", "deny", message, 0, [message], [report])
        emit(result, 1)

    stdout = proc.stdout or ""
    stderr = proc.stderr or ""
    if stdout.strip():
        log("mypy stdout:\n%s" % stdout.rstrip())
    if stderr.strip():
        log("mypy stderr:\n%s" % stderr.rstrip())

    error_lines = [ln.strip() for ln in stdout.splitlines() if ERROR_MARKER in ln]
    issue_count = len(error_lines)

    combined = [
        "$ %s" % " ".join(cmd),
        "",
        "[stdout]",
        stdout,
        "[stderr]",
        stderr,
    ]
    report = write_report(artifact_dir, "\n".join(combined))

    if proc.returncode == 0:
        message = "mypy: no type errors"
        result = action_result("success", "pass", message, 0, [], [report])
        emit(result, 0)

    if proc.returncode == 1:
        # Type errors found: fail-closed.
        message = "mypy found %d type error(s)" % issue_count
        hints = error_lines[:10]
        result = action_result("failed", "deny", message, issue_count, hints, [report])
        emit(result, 1)

    # Any other exit code (e.g. 2) is a mypy internal/config error: fail-closed.
    detail = (stderr or stdout).strip()
    message = "mypy exited with code %d: %s" % (proc.returncode, detail[:300])
    log(message)
    result = action_result("failed", "deny", message, issue_count, [message], [report])
    emit(result, 1)


if __name__ == "__main__":
    main()
