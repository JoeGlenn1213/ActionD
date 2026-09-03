#!/usr/bin/env python3
"""
python-build - build Python packages (sdist + wheel).

Reads ``{event, repo_path, artifact_dir}`` from stdin, builds the repository
with ``python3 -m build`` and falls back to ``setup.py sdist bdist_wheel`` when
the ``build`` module is unavailable. Emits a single JSON ActionResult on stdout;
all diagnostics go to stderr so they never pollute the machine-readable output.
"""

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Dict, List, Optional

PLUGIN_ID = "python-build"
CAPABILITY = "build"

# Suffixes that mark a file as a built distribution artifact.
_DIST_SUFFIXES = (".whl", ".tar.gz", ".tgz", ".zip", ".tar.bz2")

# Written into the output directory for diagnostics, but not a build product.
_LOG_NAME = "build-output.txt"


def read_input() -> Dict[str, object]:
    try:
        return json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)


def run(cmd: List[str], cwd: Path, env: Optional[Dict[str, str]] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=str(cwd),
        capture_output=True,
        text=True,
        env=env or os.environ.copy(),
    )


def has_module(python_bin: str, module_name: str, cwd: Path) -> bool:
    probe = run([python_bin, "-c", "import %s" % module_name], cwd)
    return probe.returncode == 0


def capture_step(label: str, result: subprocess.CompletedProcess) -> List[str]:
    lines = ["$ %s" % label]
    if result.stdout:
        lines.extend(["[stdout]", result.stdout.strip()])
    if result.stderr:
        lines.extend(["[stderr]", result.stderr.strip()])
    lines.append("[exit] %d" % result.returncode)
    lines.append("")
    return lines


def resolve_python3() -> str:
    for candidate in ("python3", "python"):
        found = shutil.which(candidate)
        if found:
            return found
    return sys.executable


def collect_dist_files(out_dir: Path) -> List[Path]:
    """List built distribution artifacts in the output directory."""
    files: List[Path] = []
    if not out_dir.is_dir():
        return files
    for entry in sorted(out_dir.iterdir()):
        if not entry.is_file() or entry.name == _LOG_NAME:
            continue
        if entry.name.endswith(_DIST_SUFFIXES):
            files.append(entry)
    return files


def _fmt_artifacts(raw: List[object]) -> List[dict]:
    artifacts: List[dict] = []
    for item in raw:
        if isinstance(item, dict):
            artifacts.append(item)
        elif isinstance(item, str):
            artifacts.append({"name": item, "path": item})
    return artifacts


def to_action_result(
    old_result: dict,
    plugin_id: str,
    capability: str,
    summary_message: Optional[str] = None,
    counts: Optional[dict] = None,
    signals: Optional[dict] = None,
    hints: Optional[List[str]] = None,
) -> dict:
    """
    Convert a legacy-style result dict into the V1 ActionResult specification.
    """
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"

    decision = "pass" if status == "success" else "deny"

    if summary_message is None:
        summary_message = old_result.get("error", "Python package built successfully")
        if status == "success":
            summary_message = "Python package built successfully"

    if counts is None:
        counts = {"issues": 0 if status == "success" else 1}
    if signals is None:
        signals = {"build_passed": status == "success", "artifact_count": 0}
    if hints is None:
        hints = [summary_message] if decision == "deny" else []

    now = datetime.utcnow().isoformat() + "Z"

    return {
        "action_id": "act_%s" % uuid.uuid4().hex[:8],
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "python",
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
            "message": summary_message,
            "counts": counts,
        },
        "hints": hints,
        "artifacts": _fmt_artifacts(old_result.get("artifacts", [])),
        "signals": signals,
        "raw_outputs": {},
        "next_actions": [],
    }


def main() -> None:
    input_data = read_input()
    repo_path_raw = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not isinstance(repo_path_raw, str) or not repo_path_raw.strip():
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)

    repo_path = Path(repo_path_raw)
    out_dir = (
        Path(artifact_dir)
        if isinstance(artifact_dir, str) and artifact_dir.strip()
        else repo_path / "dist"
    )
    out_dir = out_dir.resolve()
    os.makedirs(out_dir, exist_ok=True)

    has_pyproject = (repo_path / "pyproject.toml").is_file()
    has_setup = (repo_path / "setup.py").is_file()

    python_bin = resolve_python3()

    # Nothing to build -> skip without failing (fail-closed must not false-fail).
    if not has_pyproject and not has_setup:
        action_result = to_action_result(
            {"status": "success", "artifacts": []},
            plugin_id=PLUGIN_ID,
            capability=CAPABILITY,
            summary_message="Skipped: no build configuration found (missing pyproject.toml and setup.py)",
            counts={"issues": 0},
            signals={"build_passed": True, "artifact_count": 0},
        )
        print(json.dumps(action_result))
        return

    # Prefer PEP 517 `python3 -m build`; fall back to setup.py only when the
    # `build` module is unavailable.
    if has_module(python_bin, "build", repo_path):
        cmd = [python_bin, "-m", "build", "--outdir", str(out_dir)]
        method = "build"
    elif has_setup:
        cmd = [python_bin, "setup.py", "sdist", "bdist_wheel", "-d", str(out_dir)]
        method = "setuptools"
    else:
        hint = (
            "The `build` module is not installed and no setup.py exists. "
            "Install it with: pip install build"
        )
        action_result = to_action_result(
            {"status": "failed", "artifacts": []},
            plugin_id=PLUGIN_ID,
            capability=CAPABILITY,
            summary_message=hint,
            counts={"issues": 1},
            signals={"build_passed": False, "artifact_count": 0},
            hints=[hint],
        )
        print(json.dumps(action_result))
        sys.exit(1)

    print("[python-build] Building with %s: %s" % (method, " ".join(cmd)), file=sys.stderr)

    result = run(cmd, repo_path)

    combined = ["$ " + " ".join(cmd), ""]
    combined.extend(capture_step(method, result))

    log_path = out_dir / _LOG_NAME
    try:
        log_path.write_text("\n".join(combined))
    except OSError:
        pass

    if result.stdout:
        print("[BUILD OUTPUT]\n%s" % result.stdout, file=sys.stderr)
    if result.stderr:
        print("[BUILD STDERR]\n%s" % result.stderr, file=sys.stderr)

    if result.returncode != 0:
        error_msg = "Build failed (%s) with exit code %d" % (method, result.returncode)
        action_result = to_action_result(
            {"status": "failed", "artifacts": [], "error": error_msg},
            plugin_id=PLUGIN_ID,
            capability=CAPABILITY,
            summary_message=error_msg,
            counts={"issues": 1},
            signals={"build_passed": False, "artifact_count": 0},
            hints=[
                "Build command failed; see %s for details. Command: %s"
                % (_LOG_NAME, " ".join(cmd))
            ],
        )
        print(json.dumps(action_result))
        sys.exit(1)

    dist_files = collect_dist_files(out_dir)
    artifact_entries = [{"name": f.name, "path": str(f)} for f in dist_files]

    action_result = to_action_result(
        {"status": "success", "artifacts": artifact_entries},
        plugin_id=PLUGIN_ID,
        capability=CAPABILITY,
        summary_message="Built %d artifact(s) with %s" % (len(artifact_entries), method),
        counts={"issues": 0},
        signals={"build_passed": True, "artifact_count": len(artifact_entries)},
    )
    print(json.dumps(action_result))


if __name__ == "__main__":
    main()
