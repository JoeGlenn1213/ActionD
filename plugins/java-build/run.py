#!/usr/bin/env python3
"""Java Build Plugin - Build packages using Maven or Gradle."""

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path


def log(message):
    """Print a log line to stderr so stdout stays JSON-only."""
    print(f"[java-build] {message}", file=sys.stderr)


def detect_build_tool(repo_path: Path) -> str:
    """Detect which build tool to use."""
    if (repo_path / "pom.xml").exists():
        return "maven"
    if (repo_path / "build.gradle").exists() or (repo_path / "build.gradle.kts").exists():
        return "gradle"
    return "unknown"


def collect_artifacts(repo_path: Path, artifact_dir: Path):
    """Copy jar/war build outputs from the repo into artifact_dir.

    Returns a list of {name, path} dicts pointing at the copied files
    (real paths inside artifact_dir), so the engine can consume them.
    """
    artifacts = []
    search_roots = [repo_path / "target", repo_path / "build" / "libs"]

    for root in search_roots:
        if not root.exists():
            continue
        for pattern in ("*.jar", "*.war"):
            for f in sorted(root.glob(pattern)):
                if not f.is_file():
                    continue
                dest = artifact_dir / f.name
                try:
                    shutil.copy2(f, dest)
                    log(f"Collected artifact: {f} -> {dest}")
                    artifacts.append({"name": dest.name, "path": str(dest)})
                except OSError as exc:
                    log(f"Failed to copy artifact {f}: {exc}")

    return artifacts


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
    summary_msg = old_result.get("error", "Java build passed")
    if status == "success":
        summary_msg = old_result.get("summary", "Java build completed successfully")

    now = datetime.utcnow().isoformat() + "Z"

    # Normalize artifacts into [{name, path}] with real paths.
    artifacts = []
    for a in old_result.get("artifacts", []):
        if isinstance(a, str):
            artifacts.append({"name": os.path.basename(a) or a, "path": a})
        elif isinstance(a, dict) and a.get("path"):
            artifacts.append(
                {
                    "name": a.get("name") or os.path.basename(a["path"]),
                    "path": a["path"],
                }
            )

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "java",
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
            "counts": old_result.get("counts", {})
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": artifacts,
        "signals": {
            "build_passed": status == "success"
        },
        "raw_outputs": {},
        "next_actions": []
    }

    return action_result


def fail(plugin_id: str, capability: str, message: str, exit_code: int = 1):
    """Emit a failed V1 ActionResult and exit non-zero (fail-closed)."""
    result = to_action_result(
        {"status": "failed", "error": message, "artifacts": []},
        plugin_id=plugin_id,
        capability=capability,
    )
    print(json.dumps(result))
    log(message)
    sys.exit(exit_code)


def read_input():
    """Read {event, repo_path, artifact_dir} from stdin.

    REPO_PATH env var is kept as a fallback for backwards compatibility.
    Never raises: falls back to {} on malformed/absent input.
    """
    try:
        return json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        log("Invalid or missing JSON on stdin")
        return {}


def main():
    input_data = read_input()
    event = input_data.get("event", {})
    log(f"Event: {event.get('type', 'unknown')}")

    repo_path_str = input_data.get("repo_path") or os.environ.get("REPO_PATH")
    artifact_dir_str = input_data.get("artifact_dir")

    if not repo_path_str:
        fail("java-build", "build", "No repo_path provided (stdin repo_path or REPO_PATH env)")
    if not artifact_dir_str:
        fail("java-build", "build", "No artifact_dir provided for build output")

    repo_path = Path(repo_path_str).resolve()
    if not repo_path.is_dir():
        fail("java-build", "build", f"repo_path not found: {repo_path}")

    artifact_dir = Path(artifact_dir_str).resolve()
    artifact_dir.mkdir(parents=True, exist_ok=True)

    tool = detect_build_tool(repo_path)
    log(f"Repository: {repo_path}")
    log(f"Detected build tool: {tool}")

    if tool == "maven":
        cmd = ["mvn", "package", "-DskipTests"]
    elif tool == "gradle":
        gradlew = repo_path / "gradlew"
        if gradlew.exists():
            cmd = ["./gradlew", "build", "-x", "test"]
        else:
            cmd = ["gradle", "build", "-x", "test"]
    else:
        fail("java-build", "build", "No supported build tool found (expected pom.xml or build.gradle)")

    log(f"Running: {' '.join(cmd)}")

    try:
        proc = subprocess.run(
            cmd,
            cwd=str(repo_path),
            capture_output=True,
            text=True,
        )
    except FileNotFoundError as exc:
        fail("java-build", "build", f"Build tool not found: {exc}")
    except OSError as exc:
        fail("java-build", "build", f"Failed to run build command: {exc}")

    # Keep stdout JSON-only: route build output to stderr.
    if proc.stdout:
        log(proc.stdout[-4000:])
    if proc.stderr:
        log(proc.stderr[-4000:])

    artifacts = []
    if proc.returncode == 0:
        artifacts = collect_artifacts(repo_path, artifact_dir)
        if not artifacts:
            log("Build succeeded but no jar/war artifacts were collected")
        response = {
            "status": "success",
            "summary": f"Java build completed successfully ({len(artifacts)} artifact(s))",
            "artifacts": artifacts,
            "counts": {"artifacts": len(artifacts)},
        }
    else:
        response = {
            "status": "failed",
            "error": f"Build failed with exit code {proc.returncode}",
            "artifacts": artifacts,
        }

    action_result = to_action_result(response, plugin_id="java-build", capability="build")
    print(json.dumps(action_result))

    # Fail-closed: non-zero exit on build failure (double insurance).
    if proc.returncode != 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
