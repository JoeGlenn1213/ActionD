#!/usr/bin/env python3
"""
web-build - build web applications (Next.js / Vite / Create React App) for ActionD.

Detects the frontend framework, installs dependencies when ``node_modules`` is
missing (pnpm/yarn/npm, lockfile-driven), runs the build, and reports the
produced output directories (``.next``, ``out``, ``dist``, ``build``) as
artifacts.

Output contract: a single V1 ActionResult JSON on stdout (all logs go to
stderr). Fail-closed: a build failure blocks with status=failed + decision=deny
and exit code 1; a repo without a supported framework is skipped (success).
"""

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Dict, List, Optional, Tuple

PLUGIN_ID = "web-build"
CAPABILITY = "build"
LANGUAGE = "web"

# Default output directory produced by each framework's build.
_FRAMEWORK_OUTPUT_DIRS = {
    "next": [".next", "out"],
    "vite": ["dist"],
    "cra": ["build"],
}

# Direct build command used when package.json declares no "build" script.
_FRAMEWORK_BUILD_CMD = {
    "next": ["next", "build"],
    "vite": ["vite", "build"],
    "cra": ["react-scripts", "build"],
}


def log(message: str) -> None:
    print(f"[{PLUGIN_ID}] {message}", file=sys.stderr)


def read_input() -> Dict:
    """Read {event, repo_path, artifact_dir} from stdin, with argv fallback."""
    data: Dict = {}
    try:
        parsed = json.load(sys.stdin)
        if isinstance(parsed, dict):
            data = parsed
    except (json.JSONDecodeError, ValueError):
        data = {}

    argv = sys.argv[1:]

    def _argv_value(flag: str) -> Optional[str]:
        try:
            return argv[argv.index(flag) + 1]
        except (ValueError, IndexError):
            return None

    if not data.get("repo_path"):
        data["repo_path"] = _argv_value("--repo") or ""
    if not data.get("artifact_dir"):
        data["artifact_dir"] = _argv_value("--out")
    return data


def run(cmd: List[str], cwd: Path, env: Optional[Dict[str, str]] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=str(cwd),
        capture_output=True,
        text=True,
        env=env or os.environ.copy(),
    )


def read_package_json(repo_path: Path) -> Dict:
    pkg = repo_path / "package.json"
    if not pkg.exists():
        return {}
    try:
        data = json.loads(pkg.read_text(encoding="utf-8"))
        return data if isinstance(data, dict) else {}
    except (json.JSONDecodeError, ValueError, OSError):
        return {}


def detect_package_manager(repo_path: Path, package_data: Dict) -> str:
    """Return 'pnpm' / 'yarn' / 'npm' from lockfiles, then the packageManager field."""
    if (repo_path / "pnpm-lock.yaml").exists():
        return "pnpm"
    if (repo_path / "yarn.lock").exists():
        return "yarn"
    if (repo_path / "package-lock.json").exists():
        return "npm"
    pm_field = str(package_data.get("packageManager", "")).split("@")[0].lower()
    if pm_field in ("pnpm", "yarn", "npm"):
        return pm_field
    return "npm"


def install_dependencies(repo_path: Path, pm: str, logs: List[str]) -> Tuple[bool, str]:
    """Ensure node_modules exists. Returns (ok, hint)."""
    if (repo_path / "node_modules").exists():
        logs.append("[deps] node_modules exists; skipping install")
        return True, ""

    if shutil.which(pm) is None:
        hint = f"package manager '{pm}' not found on PATH; cannot install dependencies"
        logs.append(f"[deps] {hint}")
        return False, hint

    if pm == "pnpm":
        cmd = ["pnpm", "install", "--frozen-lockfile"] if (repo_path / "pnpm-lock.yaml").exists() else ["pnpm", "install"]
    elif pm == "yarn":
        cmd = ["yarn", "install", "--frozen-lockfile"] if (repo_path / "yarn.lock").exists() else ["yarn", "install"]
    else:
        cmd = ["npm", "ci"] if (repo_path / "package-lock.json").exists() else ["npm", "install"]

    logs.append("[deps] " + " ".join(cmd))
    proc = run(cmd, repo_path)
    if proc.stdout:
        logs.append("[deps stdout] " + proc.stdout.strip())
    if proc.stderr:
        logs.append("[deps stderr] " + proc.stderr.strip())
    if proc.returncode != 0:
        hint = f"dependency install failed ({' '.join(cmd)} exited {proc.returncode})"
        logs.append(f"[deps] {hint}")
        return False, hint
    logs.append("[deps] install completed")
    return True, ""


def save_report(artifact_dir: Optional[str], name: str, lines: List[str]) -> List[Dict]:
    """Write a text report into artifact_dir; returns [{name, path}] or []."""
    if not artifact_dir:
        return []
    os.makedirs(artifact_dir, exist_ok=True)
    path = os.path.abspath(os.path.join(artifact_dir, name))
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
    except OSError:
        return []
    return [{"name": name, "path": path}]


def to_action_result(old_result: Dict, plugin_id: str, capability: str, issue_count: int, language: str) -> Dict:
    """Convert a legacy result into the V1 ActionResult specification."""
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"
    elif status == "skipped":
        status = "success"  # intentional skip is a success, not a failure

    decision = "deny" if status in ("failed", "warning") else "pass"

    summary_msg = old_result.get("summary") or old_result.get("error") or "completed"

    now = datetime.utcnow().isoformat() + "Z"

    artifacts = []
    for a in old_result.get("artifacts", []):
        if isinstance(a, str) and a:
            artifacts.append({"name": os.path.basename(a) or a, "path": a})
        elif isinstance(a, dict) and a.get("path"):
            artifacts.append(
                {
                    "name": a.get("name") or os.path.basename(str(a["path"])),
                    "path": a["path"],
                }
            )

    hints = list(old_result.get("hints") or [])
    if not hints and decision == "deny":
        hints = [summary_msg]

    signals = dict(old_result.get("signals") or {})

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
            "duration_ms": 0,
        },
        "context": {
            "repo": "unknown",
            "module": "unknown",
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown",
        },
        "summary": {"message": summary_msg, "counts": {"issues": issue_count}},
        "hints": hints,
        "artifacts": artifacts,
        "signals": signals,
        "raw_outputs": {},
        "next_actions": list(old_result.get("next_actions") or []),
    }


def emit(action_result: Dict, exit_code: int) -> None:
    print(json.dumps(action_result))
    sys.exit(exit_code)


def detect_framework(repo_path: Path, package_data: Dict) -> Optional[str]:
    """Detect next / vite / cra. Returns None when no supported framework is found."""
    scripts = package_data.get("scripts") or {}
    build_script = str(scripts.get("build", ""))
    deps: Dict = {}
    deps.update(package_data.get("dependencies") or {})
    deps.update(package_data.get("devDependencies") or {})

    next_configs = ("next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts")
    if any((repo_path / c).exists() for c in next_configs) or "next" in deps or "next build" in build_script:
        return "next"

    vite_configs = ("vite.config.js", "vite.config.mjs", "vite.config.cjs", "vite.config.ts")
    if any((repo_path / c).exists() for c in vite_configs) or "vite" in deps or "vite build" in build_script:
        return "vite"

    if "react-scripts" in deps or "react-scripts build" in build_script:
        return "cra"

    return None


def main() -> None:
    input_data = read_input()
    repo_path_raw = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not isinstance(repo_path_raw, str) or not repo_path_raw.strip():
        emit(
            to_action_result({"status": "failed", "summary": "no repo_path provided", "artifacts": []},
                             PLUGIN_ID, CAPABILITY, 0, LANGUAGE),
            1,
        )

    repo_path = Path(repo_path_raw)
    if not repo_path.is_dir():
        emit(
            to_action_result({"status": "failed", "summary": f"repo_path not found: {repo_path}", "artifacts": []},
                             PLUGIN_ID, CAPABILITY, 0, LANGUAGE),
            1,
        )

    package_data = read_package_json(repo_path)
    if not package_data:
        emit(
            to_action_result({"status": "success", "summary": "skipped: no package.json found (not a web project)", "artifacts": []},
                             PLUGIN_ID, CAPABILITY, 0, LANGUAGE),
            0,
        )

    framework = detect_framework(repo_path, package_data)
    scripts = package_data.get("scripts") or {}
    build_script = scripts.get("build")

    if framework is None:
        artifacts = save_report(
            artifact_dir,
            "build-report.txt",
            ["SKIPPED: no supported build framework detected (expected next / vite / react-scripts)"],
        )
        emit(
            to_action_result(
                {
                    "status": "success",
                    "summary": "skipped: no next/vite/cra build detected",
                    "artifacts": artifacts,
                    "signals": {"build_passed": False, "build_skipped": True},
                },
                PLUGIN_ID, CAPABILITY, 0, LANGUAGE,
            ),
            0,
        )

    pm = detect_package_manager(repo_path, package_data)
    logs: List[str] = []

    ok, hint = install_dependencies(repo_path, pm, logs)
    if not ok:
        artifacts = save_report(artifact_dir, "build-report.txt", logs)
        emit(
            to_action_result(
                {
                    "status": "failed",
                    "summary": hint,
                    "artifacts": artifacts,
                    "hints": [hint + "; run it manually inside the repo and retry"],
                    "signals": {"build_passed": False},
                },
                PLUGIN_ID, CAPABILITY, 1, LANGUAGE,
            ),
            1,
        )

    if shutil.which(pm) is None:
        artifacts = save_report(artifact_dir, "build-report.txt", logs + [f"[error] '{pm}' not found on PATH"])
        emit(
            to_action_result(
                {
                    "status": "failed",
                    "summary": f"package manager '{pm}' not found on PATH",
                    "artifacts": artifacts,
                    "hints": [f"install '{pm}' and retry"],
                    "signals": {"build_passed": False},
                },
                PLUGIN_ID, CAPABILITY, 1, LANGUAGE,
            ),
            1,
        )

    if build_script:
        cmd = [pm, "run", "build"]
    else:
        cmd = ["npx", "--no-install"] + _FRAMEWORK_BUILD_CMD[framework]

    env = os.environ.copy()
    env["CI"] = "1"

    logs.append("$ " + " ".join(cmd))
    try:
        proc = run(cmd, repo_path, env)
    except OSError as exc:
        logs.append(f"[error] failed to run build command: {exc}")
        artifacts = save_report(artifact_dir, "build-report.txt", logs)
        emit(
            to_action_result(
                {
                    "status": "failed",
                    "summary": f"failed to run build command: {exc}",
                    "artifacts": artifacts,
                    "hints": [f"ensure '{pm}' is installed and dependencies are present"],
                    "signals": {"build_passed": False},
                },
                PLUGIN_ID, CAPABILITY, 1, LANGUAGE,
            ),
            1,
        )

    if proc.stdout:
        logs.append("[stdout]")
        logs.append(proc.stdout.rstrip())
    if proc.stderr:
        logs.append("[stderr]")
        logs.append(proc.stderr.rstrip())
    logs.append(f"[exit] {proc.returncode}")

    artifacts = save_report(artifact_dir, "build-report.txt", logs)

    if proc.returncode != 0:
        tail = (proc.stderr or proc.stdout or "").strip().splitlines()
        head = tail[0][:200] if tail else f"exit code {proc.returncode}"
        summary = f"build failed (exit {proc.returncode}): {head}"
        emit(
            to_action_result(
                {
                    "status": "failed",
                    "summary": summary,
                    "artifacts": artifacts,
                    "hints": ["build failed; inspect build-report.txt for the full log"],
                    "signals": {"build_passed": False},
                },
                PLUGIN_ID, CAPABILITY, 1, LANGUAGE,
            ),
            1,
        )

    for rel in _FRAMEWORK_OUTPUT_DIRS.get(framework, []):
        p = repo_path / rel
        if p.is_dir():
            artifacts.append({"name": rel, "path": str(p)})

    emit(
        to_action_result(
            {
                "status": "success",
                "summary": f"build succeeded ({framework})",
                "artifacts": artifacts,
                "signals": {"build_passed": True},
            },
            PLUGIN_ID, CAPABILITY, 0, LANGUAGE,
        ),
        0,
    )


if __name__ == "__main__":
    main()
