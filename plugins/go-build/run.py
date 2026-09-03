#!/usr/bin/env python3
import json
import os
import subprocess
import sys


def run_command(cmd, cwd, env=None):
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
    )


def detect_main_packages(repo_path):
    cmd = [
        "go",
        "list",
        "-f",
        "{{if eq .Name \"main\"}}{{.ImportPath}}::{{.Dir}}{{end}}",
        "./...",
    ]
    result = run_command(cmd, repo_path)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "failed to detect Go entrypoints")

    packages = []
    for line in result.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        import_path, package_dir = line.split("::", 1)
        package_dir = os.path.normpath(package_dir)
        base_name = os.path.basename(package_dir)
        if base_name == "." or base_name == "":
            base_name = os.path.basename(os.path.normpath(repo_path))
        packages.append({
            "import_path": import_path,
            "package_dir": package_dir,
            "binary_name": base_name,
        })

    if not packages:
        raise RuntimeError("no main packages found")

    packages.sort(key=lambda item: item["import_path"])
    return packages


def host_target(repo_path):
    goos = run_command(["go", "env", "GOOS"], repo_path)
    goarch = run_command(["go", "env", "GOARCH"], repo_path)
    if goos.returncode != 0 or goarch.returncode != 0:
        raise RuntimeError("failed to detect host Go target")
    return {
        "os": goos.stdout.strip(),
        "arch": goarch.stdout.strip(),
    }

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
    summary_msg = old_result.get("error", "Go build passed")
    if status == "success":
        summary_msg = "Go build completed successfully"

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
            "counts": {}
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "build_passed": status == "success"
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result

def main():
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)

    if not artifact_dir:
        # If no artifact dir, we can't save binaries reasonably (or just leave in repo).
        # We'll fail for now as build needs output.
        print(json.dumps({"status": "error", "error": "No artifact_dir provided for build output"}))
        sys.exit(1)

    os.makedirs(artifact_dir, exist_ok=True)

    saved_artifacts = []
    errors = []

    try:
        target = host_target(repo_path)
        packages = detect_main_packages(repo_path)
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e), "artifacts": []}))
        sys.exit(1)

    env = os.environ.copy()
    env["GOOS"] = target["os"]
    env["GOARCH"] = target["arch"]

    for pkg in packages:
        binary_name = f"{pkg['binary_name']}-{target['os']}-{target['arch']}"
        output_path = os.path.join(artifact_dir, binary_name)
        cmd = ["go", "build", "-o", output_path, pkg["import_path"]]

        try:
            result = run_command(cmd, repo_path, env=env)
            if result.returncode != 0:
                errors.append(
                    f"Failed {pkg['import_path']} for {target['os']}/{target['arch']}: {result.stderr}"
                )
            else:
                saved_artifacts.append(binary_name)
        except Exception as e:
            errors.append(str(e))

    if errors:
        response = {
            "status": "error",
            "error": "\n".join(errors),
            "artifacts": saved_artifacts
        }
    else:
        response = {
            "status": "success",
            "artifacts": saved_artifacts
        }

    action_result = to_action_result(response, plugin_id="go-build", capability="build")
    print(json.dumps(action_result))

if __name__ == "__main__":
    main()
