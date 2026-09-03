#!/usr/bin/env python3
"""
python-ruff - adaptive ruff runner for Python projects

This plugin prefers a repository-local virtual environment. If none is
available, it falls back to an ActionD-managed environment that only installs
ruff, keeping lint runs lightweight.
"""

import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Dict, List, Optional, Tuple

try:
    import tomllib
except ModuleNotFoundError:  # pragma: no cover
    tomllib = None

# 40-hex basename marks an isolated-checkout sha directory (<root>/<repo>/<sha>).
_SHA_RE = re.compile(r"^[0-9a-f]{40}$")

# tomllib-free fallback signals for Python < 3.11 interpreters (e.g. the
# macOS system python 3.9 under a minimal launchd PATH): a [tool.ruff]
# section (any subtable) or a ruff entry in a dependency line.
_RUFF_SECTION_RE = re.compile(r"^\[tool\.ruff(?:\.[^\]]*)?\]\s*$", re.MULTILINE)
_RUFF_DEP_RE = re.compile(r'^\s*["\']?ruff[>"\'=~\s]', re.MULTILINE)


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


def load_pyproject(repo_path: Path) -> Dict[str, object]:
    pyproject = repo_path / "pyproject.toml"
    if not pyproject.exists():
        return {}
    text = pyproject.read_text()
    if tomllib is None:
        # Python < 3.11 has no stdlib tomllib. Keep the raw text so the
        # [tool.ruff] presence check can still run without a full parse.
        return {"__raw__": text}
    try:
        return tomllib.loads(text)
    except Exception:
        return {}


def repo_local_interpreters(repo_path: Path) -> List[str]:
    candidates: List[str] = []
    for env_name in (".venv", "venv", "env"):
        python_bin = repo_path / env_name / "bin" / "python"
        if python_bin.exists():
            candidates.append(str(python_bin))
    return candidates


def managed_env_dir(repo_path: Path) -> Path:
    # In the isolated-checkout layout the repo path is <root>/<repo>/<sha>:
    # key the shared venv by the repo name so it survives across shas.
    name = repo_path.name
    if _SHA_RE.match(name):
        name = repo_path.parent.name
    slug = name.lower().replace(" ", "-").replace("_", "-")
    return Path.home() / ".localgithub" / "actiond-venvs" / slug


def capture_step(label: str, result: subprocess.CompletedProcess) -> List[str]:
    lines = [f"$ {label}"]
    if result.stdout:
        lines.extend(["[stdout]", result.stdout.strip()])
    if result.stderr:
        lines.extend(["[stderr]", result.stderr.strip()])
    lines.append(f"[exit] {result.returncode}")
    lines.append("")
    return lines


def has_module(python_bin: str, module_name: str, cwd: Path) -> bool:
    probe = run([python_bin, "-c", f"import {module_name}"], cwd)
    return probe.returncode == 0


def supports_ruff(repo_path: Path, pyproject_data: Dict[str, object]) -> bool:
    if (repo_path / "ruff.toml").exists() or (repo_path / ".ruff.toml").exists():
        return True

    # tomllib-free path: raw text was stashed by load_pyproject on Python < 3.11.
    raw = pyproject_data.get("__raw__")
    if isinstance(raw, str):
        return _RUFF_SECTION_RE.search(raw) is not None or _RUFF_DEP_RE.search(raw) is not None

    tool = pyproject_data.get("tool")
    if isinstance(tool, dict) and isinstance(tool.get("ruff"), dict):
        return True

    project = pyproject_data.get("project")
    if isinstance(project, dict):
        optional = project.get("optional-dependencies")
        if isinstance(optional, dict):
            for deps in optional.values():
                if isinstance(deps, list):
                    for dep in deps:
                        if isinstance(dep, str) and dep.lower().startswith("ruff"):
                            return True

    return False


def ensure_managed_env(repo_path: Path) -> Tuple[Optional[str], List[str]]:
    logs: List[str] = []
    env_dir = managed_env_dir(repo_path)
    python_bin = env_dir / "bin" / "python"
    ready_stamp = env_dir / ".actiond-ready"

    # A missing ready stamp means a previously failed (poisoned) bootstrap —
    # rebuild instead of reusing.
    needs_bootstrap = not python_bin.exists() or not ready_stamp.exists()

    if needs_bootstrap:
        if env_dir.exists():
            logs.append(f"[ENV] Removing stale env before bootstrap: {env_dir}")
            shutil.rmtree(env_dir, ignore_errors=True)
        logs.append(f"[ENV] Creating managed env: {env_dir}")
        env_dir.parent.mkdir(parents=True, exist_ok=True)
        create = subprocess.run(
            [sys.executable, "-m", "venv", str(env_dir)],
            capture_output=True,
            text=True,
        )
        logs.extend(capture_step("python -m venv", create))
        if create.returncode != 0 or not python_bin.exists():
            shutil.rmtree(env_dir, ignore_errors=True)
            return None, logs
    else:
        logs.append(f"[ENV] Reusing managed env: {env_dir}")

    pip_env = os.environ.copy()
    pip_env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"

    upgrade = run([str(python_bin), "-m", "pip", "install", "--upgrade", "pip"], repo_path, pip_env)
    logs.extend(capture_step("pip install --upgrade pip", upgrade))
    if upgrade.returncode != 0:
        shutil.rmtree(env_dir, ignore_errors=True)
        return None, logs

    if not has_module(str(python_bin), "ruff", repo_path):
        install = run([str(python_bin), "-m", "pip", "install", "ruff"], repo_path, pip_env)
        logs.extend(capture_step("pip install ruff", install))
        if install.returncode != 0:
            shutil.rmtree(env_dir, ignore_errors=True)
            return None, logs

    ok = has_module(str(python_bin), "ruff", repo_path)
    if not ok:
        shutil.rmtree(env_dir, ignore_errors=True)
        return None, logs

    try:
        ready_stamp.write_text(
            json.dumps({"python": f"{sys.version_info[0]}.{sys.version_info[1]}", "ok": True})
        )
    except Exception:
        pass

    return (str(python_bin), logs)


def pick_python(repo_path: Path) -> Tuple[Optional[str], List[str]]:
    for candidate in repo_local_interpreters(repo_path):
        if has_module(candidate, "ruff", repo_path):
            return candidate, []
    return ensure_managed_env(repo_path)


def lint_targets(repo_path: Path) -> List[str]:
    targets = [name for name in ("src", "tests", "test", "scripts") if (repo_path / name).exists()]
    return targets or ["."]


def copy_if_exists(src: Path, artifact_dir: Optional[str], name: str, saved: List[str]) -> None:
    if not artifact_dir or not src.exists():
        return
    os.makedirs(artifact_dir, exist_ok=True)
    shutil.copy(src, os.path.join(artifact_dir, name))
    saved.append(name)


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
    summary_msg = old_result.get("error", "Ruff linting passed")
    if status == "success":
        summary_msg = "Ruff linting passed"

    now = datetime.utcnow().isoformat() + "Z"

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "python",
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
            "counts": {}
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "lint_passed": status == "success",
            "lint_error_count": 0 if status == "success" else 1
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result

def main() -> None:
    input_data = read_input()
    repo_path_raw = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not isinstance(repo_path_raw, str) or not repo_path_raw.strip():
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)

    repo_path = Path(repo_path_raw)
    pyproject_data = load_pyproject(repo_path)

    if not supports_ruff(repo_path, pyproject_data):
        log_path = repo_path / "ruff-report.txt"
        log_path.write_text("SKIPPED: no Ruff configuration or dependency detected\n")
        saved_artifacts: List[str] = []
        copy_if_exists(log_path, artifact_dir if isinstance(artifact_dir, str) else None, "ruff-report.txt", saved_artifacts)
        action_result = to_action_result({"status": "skipped", "artifacts": saved_artifacts, "error": "No Ruff config"}, plugin_id="python-ruff", capability="lint")
        print(json.dumps(action_result))
        return

    python_bin, env_bootstrap_logs = pick_python(repo_path)
    if not python_bin:
        print(json.dumps({"status": "error", "error": "ruff not available and managed env bootstrap failed"}))
        sys.exit(1)

    env = os.environ.copy()
    python_parent = str(Path(python_bin).resolve().parent)
    env["PATH"] = f"{python_parent}:{env.get('PATH', '')}"
    if python_parent.endswith("/bin"):
        env["VIRTUAL_ENV"] = str(Path(python_parent).parent)

    cmd = [python_bin, "-m", "ruff", "check", *lint_targets(repo_path)]
    result = run(cmd, repo_path, env)

    combined_output = []
    combined_output.append("$ " + " ".join(cmd))
    combined_output.append("")
    combined_output.append("[stdout]")
    combined_output.append(result.stdout or "")
    combined_output.append("")
    combined_output.append("[stderr]")
    combined_output.append(result.stderr or "")
    if env_bootstrap_logs:
        combined_output.append("")
        combined_output.append("[env-bootstrap]")
        combined_output.extend(env_bootstrap_logs)

    log_path = repo_path / "ruff-report.txt"
    log_path.write_text("\n".join(combined_output))

    if result.stdout:
        print(f"[RUFF OUTPUT]\n{result.stdout}", file=sys.stderr)
    if result.stderr:
        print(f"[RUFF STDERR]\n{result.stderr}", file=sys.stderr)

    saved_artifacts: List[str] = []
    copy_if_exists(log_path, artifact_dir if isinstance(artifact_dir, str) else None, "ruff-report.txt", saved_artifacts)

    payload = {
        "status": "success" if result.returncode == 0 else "error",
        "artifacts": saved_artifacts,
        "python": python_bin,
    }
    if result.returncode != 0:
        payload["error"] = f"ruff exited with code {result.returncode}"
        
    action_result = to_action_result(payload, plugin_id="python-ruff", capability="lint")
    print(json.dumps(action_result))


if __name__ == "__main__":
    main()
