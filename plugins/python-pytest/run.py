#!/usr/bin/env python3
"""
python-pytest - adaptive pytest runner for Python projects

This plugin tries to use the repository's own Python environment first and
falls back to the current interpreter only when needed. It also disables
pytest plugin autoload so globally installed plugins do not pollute unrelated
projects.
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


def read_input() -> Dict[str, object]:
    try:
        return json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)


def load_pyproject(repo_path: Path) -> Dict[str, object]:
    pyproject = repo_path / "pyproject.toml"
    if not pyproject.exists() or tomllib is None:
        return {}
    try:
        return tomllib.loads(pyproject.read_text())
    except Exception:
        return {}


def interpreter_candidates(repo_path: Path) -> List[str]:
    candidates: List[str] = []
    for env_name in (".venv", "venv", "env"):
        python_bin = repo_path / env_name / "bin" / "python"
        if python_bin.exists():
            candidates.append(str(python_bin))

    for fallback in (sys.executable, shutil.which("python3"), shutil.which("python")):
        if fallback and fallback not in candidates:
            candidates.append(fallback)
    return candidates


def repo_local_interpreters(repo_path: Path) -> List[str]:
    candidates: List[str] = []
    for env_name in (".venv", "venv", "env"):
        python_bin = repo_path / env_name / "bin" / "python"
        if python_bin.exists():
            candidates.append(str(python_bin))
    return candidates


def managed_env_dir(repo_path: Path) -> Path:
    home = Path.home()
    # In the isolated-checkout layout the repo path is <root>/<repo>/<sha>:
    # key the shared venv by the repo name so it survives across shas.
    name = repo_path.name
    if _SHA_RE.match(name):
        name = repo_path.parent.name
    slug = name.lower().replace(" ", "-").replace("_", "-")
    return home / ".localgithub" / "actiond-venvs" / slug


def _parse_requires_python(pyproject_data: Dict[str, object]) -> Optional[str]:
    project = pyproject_data.get("project")
    if isinstance(project, dict):
        rp = project.get("requires-python")
        if isinstance(rp, str) and rp.strip():
            return rp.strip()
    return None


def _python_version(python_bin: str) -> Optional[Tuple[int, int]]:
    probe = subprocess.run(
        [python_bin, "-c", "import sys; print(f'{sys.version_info[0]}.{sys.version_info[1]}')"],
        capture_output=True,
        text=True,
    )
    if probe.returncode != 0:
        return None
    try:
        maj, minr = probe.stdout.strip().split(".")
        return int(maj), int(minr)
    except Exception:
        return None


def _version_satisfies(ver: Tuple[int, int], spec: str) -> bool:
    """Best-effort match for common requires-python specs
    (>=3.12, <3.13, ^3.12, ~=3.12, ==3.11.*)."""
    for clause in [c.strip() for c in spec.split(",") if c.strip()]:
        m = re.match(r"^(>=|<=|>|<|==|!=|~=|\^)?\s*(\d+)(?:\.(\d+))?", clause)
        if not m:
            continue
        op = m.group(1) or ">="
        maj, minr = int(m.group(2)), int(m.group(3)) if m.group(3) is not None else None
        if op in ("==", "!="):
            hit = ver[0] == maj and (minr is None or ver[1] == minr)
            if (op == "==" and not hit) or (op == "!=" and hit):
                return False
        elif op == "~=":
            # ~=3.12 → >=3.12,<3.13
            if ver[0] != maj or (minr is not None and ver[1] < minr):
                return False
        elif op == "^":
            # poetry caret: ^3.12 → >=3.12,<4.0
            if ver < (maj, minr or 0):
                return False
        else:
            target = (maj, minr or 0)
            ok = {
                ">=": ver >= target,
                ">": ver > target,
                "<=": ver <= target,
                "<": ver < target,
            }[op]
            if not ok:
                return False
    return True


def _pick_bootstrap_python(pyproject_data: Dict[str, object], logs: List[str]) -> str:
    """Pick an interpreter satisfying the project's requires-python."""
    requires = _parse_requires_python(pyproject_data)
    candidates: List[str] = []
    for cand in [sys.executable, "python3.13", "python3.12", "python3.11", "python3.10", "python3"]:
        if cand and cand not in candidates:
            candidates.append(cand)
    if requires:
        for cand in candidates:
            ver = _python_version(cand)
            if ver and _version_satisfies(ver, requires):
                if cand != sys.executable:
                    logs.append(f"[ENV] requires-python {requires} -> using {cand}")
                return cand
        logs.append(
            f"[ENV] WARNING: no interpreter satisfies requires-python {requires}; "
            f"falling back to {sys.executable}"
        )
    return sys.executable


def run(cmd: List[str], cwd: Path, env: Optional[Dict[str, str]] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=str(cwd),
        capture_output=True,
        text=True,
        env=env or os.environ.copy(),
    )


def has_module(python_bin: str, module_name: str, cwd: Path) -> bool:
    probe = run([python_bin, "-c", f"import {module_name}"], cwd)
    return probe.returncode == 0


def module_version(python_bin: str, module_name: str, cwd: Path) -> Optional[str]:
    probe = run(
        [python_bin, "-c", f"import {module_name}; print(getattr({module_name}, '__version__', ''))"],
        cwd,
    )
    if probe.returncode != 0:
        return None
    version = probe.stdout.strip()
    return version or None


def pick_python(repo_path: Path) -> str:
    candidates = interpreter_candidates(repo_path)
    for python_bin in candidates:
        if has_module(python_bin, "pytest", repo_path):
            return python_bin
    return candidates[0]


def ensure_managed_env(repo_path: Path) -> Tuple[Optional[str], List[str]]:
    logs: List[str] = []
    env_dir = managed_env_dir(repo_path)
    python_bin = env_dir / "bin" / "python"
    ready_stamp = env_dir / ".actiond-ready"

    pyproject = repo_path / "pyproject.toml"
    requirements = repo_path / "requirements.txt"
    pyproject_data = load_pyproject(repo_path) if pyproject.exists() else {}

    # A missing ready stamp means a previously failed (poisoned) bootstrap —
    # rebuild instead of reusing. A reused env whose interpreter no longer
    # satisfies requires-python (e.g. a 3.9 venv created while the daemon ran
    # under a minimal PATH) is equally poisoned — rebuild it too.
    needs_bootstrap = not python_bin.exists() or not ready_stamp.exists()
    if not needs_bootstrap and pyproject_data:
        requires = _parse_requires_python(pyproject_data)
        if requires:
            venv_ver = _python_version(str(python_bin))
            if venv_ver is None or not _version_satisfies(venv_ver, requires):
                logs.append(
                    f"[ENV] managed env python {venv_ver} does not satisfy "
                    f"requires-python {requires}; rebuilding"
                )
                needs_bootstrap = True
    if needs_bootstrap and not pyproject.exists() and not requirements.exists():
        return None, logs

    pip_env = os.environ.copy()
    pip_env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"

    if needs_bootstrap:
        # Remove any poisoned env left by a previous failed bootstrap.
        if env_dir.exists():
            logs.append(f"[ENV] Removing stale env before bootstrap: {env_dir}")
            shutil.rmtree(env_dir, ignore_errors=True)
        logs.append(f"[ENV] Creating managed env: {env_dir}")
        env_dir.parent.mkdir(parents=True, exist_ok=True)

        bootstrap_python = _pick_bootstrap_python(pyproject_data, logs)
        create = subprocess.run(
            [bootstrap_python, "-m", "venv", str(env_dir)],
            capture_output=True,
            text=True,
        )
        logs.extend(capture_step(f"{bootstrap_python} -m venv", create))
        if create.returncode != 0 or not python_bin.exists():
            shutil.rmtree(env_dir, ignore_errors=True)
            return None, logs
    else:
        logs.append(f"[ENV] Reusing managed env: {env_dir}")

    install_steps: List[List[str]] = [[str(python_bin), "-m", "pip", "install", "--upgrade", "pip"]]
    if pyproject.exists():
        install_steps.append([str(python_bin), "-m", "pip", "install", "-e", ".[dev]"])
        install_steps.append([str(python_bin), "-m", "pip", "install", "-e", ".", "pytest>=8,<10", "pytest-cov", "pytest-asyncio"])
    elif requirements.exists():
        install_steps.append([str(python_bin), "-m", "pip", "install", "-r", "requirements.txt", "pytest>=8,<10", "pytest-cov", "pytest-asyncio"])

    upgrade = run(install_steps[0], repo_path, pip_env)
    logs.extend(capture_step("pip install --upgrade pip", upgrade))
    if upgrade.returncode != 0:
        shutil.rmtree(env_dir, ignore_errors=True)
        return None, logs

    # Always (re)install project deps, not only on bootstrap: pip is
    # incremental/idempotent, and skipping this on env reuse left CI stale
    # whenever the project added a dependency.
    if pyproject.exists():
        primary = run(install_steps[1], repo_path, pip_env)
        logs.extend(capture_step("pip install -e .[dev]", primary))
        if primary.returncode != 0:
            fallback = run(install_steps[2], repo_path, pip_env)
            logs.extend(capture_step("pip install fallback toolchain", fallback))
            if fallback.returncode != 0:
                shutil.rmtree(env_dir, ignore_errors=True)
                return None, logs
    elif requirements.exists():
        requirements_install = run(install_steps[1], repo_path, pip_env)
        logs.extend(capture_step("pip install -r requirements.txt", requirements_install))
        if requirements_install.returncode != 0:
            shutil.rmtree(env_dir, ignore_errors=True)
            return None, logs

    # Toolchain completion: fill gaps only. pytest's major version is the
    # project's choice — never force-downgrade a newer pytest the repo or
    # bootstrap step already installed (pytest 9.x projects were being
    # silently downgraded to 8.4 by the old "pytest<9" pin, breaking their
    # fixtures).
    needs_toolchain = (
        not has_module(str(python_bin), "pytest", repo_path)
        or not has_module(str(python_bin), "pytest_asyncio", repo_path)
        or not has_module(str(python_bin), "pytest_cov", repo_path)
        or not has_module(str(python_bin), "greenlet", repo_path)
    )
    if needs_toolchain:
        toolchain = run(
            [
                str(python_bin), "-m", "pip", "install",
                "pytest>=8,<10",
                "pytest-cov",
                "pytest-asyncio>=0.23",
                "greenlet",
            ],
            repo_path,
            pip_env,
        )
        logs.extend(capture_step("pip install pinned pytest toolchain", toolchain))
        if toolchain.returncode != 0:
            shutil.rmtree(env_dir, ignore_errors=True)
            return None, logs

    ok = has_module(str(python_bin), "pytest", repo_path)
    if not ok:
        shutil.rmtree(env_dir, ignore_errors=True)
        return None, logs

    # Ready stamp marks a fully working env; its absence forces a rebuild.
    # Record the venv's own interpreter version (not the plugin runner's) so
    # the reuse check above can detect version drift.
    try:
        venv_ver = _python_version(str(python_bin))
        ver_str = f"{venv_ver[0]}.{venv_ver[1]}" if venv_ver else "?"
        ready_stamp.write_text(json.dumps({"python": ver_str, "ok": True}))
    except Exception:
        pass

    return (str(python_bin), logs)


def capture_step(label: str, result: subprocess.CompletedProcess) -> List[str]:
    lines = [f"$ {label}"]
    if result.stdout:
        lines.extend(["[stdout]", result.stdout.strip()])
    if result.stderr:
        lines.extend(["[stderr]", result.stderr.strip()])
    lines.append(f"[exit] {result.returncode}")
    lines.append("")
    return lines


def pytest_plugins(python_bin: str, repo_path: Path) -> List[str]:
    plugins: List[str] = []
    if has_module(python_bin, "pytest_cov", repo_path):
        plugins.extend(["-p", "pytest_cov"])
    if has_module(python_bin, "pytest_asyncio", repo_path):
        plugins.extend(["-p", "pytest_asyncio.plugin"])
    return plugins


def normalized_project_name(pyproject_data: Dict[str, object]) -> Optional[str]:
    project = pyproject_data.get("project")
    if not isinstance(project, dict):
        return None
    name = project.get("name")
    if not isinstance(name, str) or not name.strip():
        return None
    return name.strip().replace("-", "_")


def coverage_targets(pyproject_data: Dict[str, object], repo_path: Path) -> List[str]:
    tool = pyproject_data.get("tool")
    if isinstance(tool, dict):
        coverage = tool.get("coverage")
        if isinstance(coverage, dict):
            run_cfg = coverage.get("run")
            if isinstance(run_cfg, dict):
                source = run_cfg.get("source")
                if isinstance(source, list):
                    targets = [str(item) for item in source if isinstance(item, str) and item.strip()]
                    if targets:
                        return targets

        pytest_cfg = tool.get("pytest")
        if isinstance(pytest_cfg, dict):
            ini_options = pytest_cfg.get("ini_options")
            if isinstance(ini_options, dict):
                addopts = ini_options.get("addopts")
                if isinstance(addopts, list):
                    for opt in addopts:
                        if isinstance(opt, str) and opt.startswith("--cov="):
                            return [opt.split("=", 1)[1]]

    inferred = normalized_project_name(pyproject_data)
    if inferred and (repo_path / inferred).exists():
        return [inferred]

    candidates = []
    for entry in repo_path.iterdir():
        if not entry.is_dir():
            continue
        if entry.name.startswith(".") or entry.name in {"tests", "test", "docs", "scripts", "build", "dist", "__pycache__"}:
            continue
        if (entry / "__init__.py").exists():
            candidates.append(entry.name)
    return candidates[:1]


def test_paths(pyproject_data: Dict[str, object], repo_path: Path) -> List[str]:
    tool = pyproject_data.get("tool")
    if isinstance(tool, dict):
        pytest_cfg = tool.get("pytest")
        if isinstance(pytest_cfg, dict):
            ini_options = pytest_cfg.get("ini_options")
            if isinstance(ini_options, dict):
                configured = ini_options.get("testpaths")
                if isinstance(configured, list):
                    paths = [str(item) for item in configured if isinstance(item, str) and (repo_path / item).exists()]
                    if paths:
                        return paths

    defaults = [name for name in ("tests", "test", "agent") if (repo_path / name).exists()]
    return defaults


def build_pytest_command(python_bin: str, repo_path: Path, pyproject_data: Dict[str, object]) -> List[str]:
    cmd = [python_bin, "-m", "pytest"]
    cmd.extend(pytest_plugins(python_bin, repo_path))

    targets = test_paths(pyproject_data, repo_path)
    if targets:
        cmd.extend(targets)

    cmd.extend(["-v", "--tb=short", "--junitxml=test-results.xml"])

    if has_module(python_bin, "pytest_cov", repo_path):
        for cov_target in coverage_targets(pyproject_data, repo_path):
            cmd.append(f"--cov={cov_target}")
        cmd.append("--cov-report=json:coverage.json")

    return cmd


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
    # Map status
    if status == "error":
        status = "failed"
        
    # Decide decision based on status
    decision = "pass" if status == "success" else "deny"
    
    summary_msg = old_result.get("error", "Pytest executed successfully")
    if status == "success":
        summary_msg = "All tests passed"

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
        "hints": old_result.get("hints", []),
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", [])],
        "signals": {
            "tests_passed": status == "success"
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
    env_bootstrap_logs: List[str] = []

    local_candidates = repo_local_interpreters(repo_path)
    local_python = None
    for candidate in local_candidates:
        if has_module(candidate, "pytest", repo_path):
            local_python = candidate
            break

    if local_python is not None:
        python_bin = local_python
    else:
        managed_python, env_bootstrap_logs = ensure_managed_env(repo_path)
        python_bin = managed_python or pick_python(repo_path)

    if not has_module(python_bin, "pytest", repo_path):
        print(json.dumps({
            "status": "error",
            "error": f"pytest not available in interpreter: {python_bin}",
        }))
        sys.exit(1)

    env = os.environ.copy()
    env["PYTEST_DISABLE_PLUGIN_AUTOLOAD"] = "1"

    python_parent = str(Path(python_bin).resolve().parent)
    env["PATH"] = f"{python_parent}:{env.get('PATH', '')}"
    if python_parent.endswith("/bin"):
        venv_root = str(Path(python_parent).parent)
        env["VIRTUAL_ENV"] = venv_root

    cmd = build_pytest_command(python_bin, repo_path, pyproject_data)
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

    log_path = repo_path / "pytest-output.txt"
    log_path.write_text("\n".join(combined_output))

    if result.stdout:
        print(f"[PYTEST OUTPUT]\n{result.stdout}", file=sys.stderr)
    if result.stderr:
        print(f"[PYTEST STDERR]\n{result.stderr}", file=sys.stderr)

    saved_artifacts: List[str] = []
    copy_if_exists(repo_path / "coverage.json", artifact_dir if isinstance(artifact_dir, str) else None, "coverage.json", saved_artifacts)
    copy_if_exists(repo_path / "test-results.xml", artifact_dir if isinstance(artifact_dir, str) else None, "test-results.xml", saved_artifacts)
    copy_if_exists(log_path, artifact_dir if isinstance(artifact_dir, str) else None, "pytest-output.txt", saved_artifacts)

    response = {
        "status": "success" if result.returncode == 0 else "error",
        "artifacts": saved_artifacts,
        "python": python_bin,
    }

    if result.returncode != 0:
        response["error"] = f"pytest exited with code {result.returncode}"

    # Wrap as ActionResult
    action_result = to_action_result(response, plugin_id="python-pytest", capability="test-fast")
    print(json.dumps(action_result))


if __name__ == "__main__":
    main()
