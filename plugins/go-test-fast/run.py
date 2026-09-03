#!/usr/bin/env python3
"""
go-test-fast plugin for ActionD
Runs quick unit tests (go test -short) on push events.
"""

import json
import subprocess
import sys
import os

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
    summary_msg = old_result.get("error", "Tests passed")
    if status == "success":
        summary_msg = "All fast tests passed"

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
            "tests_passed": status == "success"
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    return action_result

def main():
    # Read input from stdin
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path")
    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)
        
    artifact_dir = input_data.get("artifact_dir", "/tmp")

    # Run go test -short
    cmd = ["go", "test", "-short", "-json", "./..."]

    try:
        result = subprocess.run(
            cmd,
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=120  # 2 minute timeout for fast tests
        )

        exit_code = result.returncode
        test_output = result.stdout
        test_error = result.stderr

        # Save test output to artifact
        saved_artifacts = []
        if artifact_dir:
            os.makedirs(artifact_dir, exist_ok=True)
            output_file = os.path.join(artifact_dir, "test-fast-output.json")
            with open(output_file, "w") as f:
                f.write(test_output)
            saved_artifacts.append("test-fast-output.json")

            if test_error:
                error_file = os.path.join(artifact_dir, "test-fast-error.log")
                with open(error_file, "w") as f:
                    f.write(test_error)
                saved_artifacts.append("test-fast-error.log")

        response = {
            "status": "success" if exit_code == 0 else "error",
            "error": f"Tests failed with exit code {exit_code}" if exit_code != 0 else "",
            "artifacts": saved_artifacts
        }

        # Wrap as ActionResult
        action_result = to_action_result(response, plugin_id="go-test-fast", capability="test-fast")
        print(json.dumps(action_result))

    except subprocess.TimeoutExpired:
        print(json.dumps({"status": "error", "error": "Test timeout (2 min)"}))
        sys.exit(1)
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    main()
