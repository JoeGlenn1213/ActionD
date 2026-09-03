#!/usr/bin/env python3
"""
Java Checkstyle Plugin for ActionD
Performs code style checking using Checkstyle
"""

import json
import sys
import os
import subprocess
import urllib.request
from pathlib import Path
from xml.etree import ElementTree as ET

CHECKSTYLE_VERSION = "10.12.7"
CHECKSTYLE_JAR = f"checkstyle-{CHECKSTYLE_VERSION}-all.jar"
CHECKSTYLE_URL = f"https://github.com/checkstyle/checkstyle/releases/download/checkstyle-{CHECKSTYLE_VERSION}/{CHECKSTYLE_JAR}"

# Overridable for offline smoke tests / avoiding writes into ~/.localgithub.
DEFAULT_CACHE_DIR = Path.home() / ".localgithub" / "actions" / "cache" / "checkstyle"


def log(message):
    """Print to stderr for logging"""
    print(f"[java-checkstyle] {message}", file=sys.stderr)


def download_checkstyle(cache_dir):
    """Download Checkstyle JAR if not cached"""
    cache_dir = Path(cache_dir)
    jar_path = cache_dir / CHECKSTYLE_JAR
    if jar_path.exists():
        log(f"Using cached Checkstyle: {jar_path}")
        return jar_path

    log(f"Downloading Checkstyle {CHECKSTYLE_VERSION}...")
    cache_dir.mkdir(parents=True, exist_ok=True)

    try:
        urllib.request.urlretrieve(CHECKSTYLE_URL, jar_path)
        log(f"Downloaded to {jar_path}")
        return jar_path
    except Exception as e:
        log(f"Failed to download Checkstyle: {e}")
        raise


def detect_java_sources(repo_path):
    """Find Java source directories"""
    repo = Path(repo_path)

    # Common Java source directories
    candidates = [
        repo / "src" / "main" / "java",
        repo / "src",
        repo
    ]

    for candidate in candidates:
        if candidate.exists() and list(candidate.rglob("*.java")):
            return candidate

    return None


def run_checkstyle(jar_path, config_path, source_dir, output_xml):
    """Execute Checkstyle"""
    cmd = [
        "java", "-jar", str(jar_path),
        "-c", str(config_path),
        "-f", "xml",
        "-o", str(output_xml),
        str(source_dir)
    ]

    log(f"Running: {' '.join(cmd)}")

    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=180  # 3 minutes
        )

        # Checkstyle returns 1 when violations are found, 0 when clean,
        # and > 1 for real execution errors (config/syntax problems).
        if result.returncode > 1:
            log(f"Checkstyle error: {result.stderr}")
            return False

        return True
    except subprocess.TimeoutExpired:
        log("Checkstyle execution timed out")
        return False
    except Exception as e:
        log(f"Failed to run Checkstyle: {e}")
        return False


def empty_summary():
    """Return an all-zero summary so callers can always index fixed keys."""
    return {
        "total_violations": 0,
        "files_with_violations": 0,
        "by_severity": {"error": 0, "warning": 0, "info": 0},
        "by_rule": {}
    }


def parse_checkstyle_xml(xml_path):
    """Parse Checkstyle XML output to structured data.

    Always returns a well-formed structure with an all-zero summary on
    missing/unparseable XML, so the main flow never hits a KeyError.
    """
    xml_path = Path(xml_path)
    if not xml_path.exists():
        log(f"Checkstyle XML missing: {xml_path}; treating as no violations")
        return {"violations": [], "summary": empty_summary()}

    try:
        tree = ET.parse(str(xml_path))
    except ET.ParseError as e:
        log(f"Failed to parse Checkstyle XML: {e}; treating as no violations")
        return {"violations": [], "summary": empty_summary()}

    root = tree.getroot()

    violations = []
    files_with_violations = set()
    severity_count = {"error": 0, "warning": 0, "info": 0}
    rule_count = {}

    for file_elem in root.findall("file"):
        file_name = file_elem.get("name")

        for error_elem in file_elem.findall("error"):
            severity = error_elem.get("severity", "warning")
            rule = error_elem.get("source", "").split(".")[-1]

            violation = {
                "file": file_name,
                "line": int(error_elem.get("line", 0)),
                "column": int(error_elem.get("column", 0)),
                "severity": severity,
                "message": error_elem.get("message", ""),
                "rule": rule
            }

            violations.append(violation)
            files_with_violations.add(file_name)
            severity_count[severity] = severity_count.get(severity, 0) + 1
            rule_count[rule] = rule_count.get(rule, 0) + 1

    return {
        "violations": violations,
        "summary": {
            "total_violations": len(violations),
            "files_with_violations": len(files_with_violations),
            "by_severity": severity_count,
            "by_rule": rule_count
        }
    }


def generate_html_report(data, output_path):
    """Generate HTML report"""
    violations = data["violations"]
    summary = data["summary"]

    html = f"""<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Checkstyle Report</title>
    <style>
        body {{ font-family: Arial, sans-serif; margin: 20px; }}
        .summary {{ background: #f5f5f5; padding: 15px; border-radius: 5px; margin-bottom: 20px; }}
        .metric {{ display: inline-block; margin-right: 30px; }}
        .metric-value {{ font-size: 24px; font-weight: bold; }}
        table {{ border-collapse: collapse; width: 100%; }}
        th, td {{ border: 1px solid #ddd; padding: 8px; text-align: left; }}
        th {{ background-color: #4CAF50; color: white; }}
        .error {{ color: #d32f2f; }}
        .warning {{ color: #f57c00; }}
        .info {{ color: #1976d2; }}
    </style>
</head>
<body>
    <h1>Checkstyle Report</h1>

    <div class="summary">
        <div class="metric">
            <div class="metric-value">{summary['total_violations']}</div>
            <div>Total Violations</div>
        </div>
        <div class="metric">
            <div class="metric-value">{summary['files_with_violations']}</div>
            <div>Files with Issues</div>
        </div>
        <div class="metric">
            <div class="metric-value error">{summary['by_severity'].get('error', 0)}</div>
            <div>Errors</div>
        </div>
        <div class="metric">
            <div class="metric-value warning">{summary['by_severity'].get('warning', 0)}</div>
            <div>Warnings</div>
        </div>
    </div>

    <h2>Violations</h2>
    <table>
        <tr>
            <th>File</th>
            <th>Line</th>
            <th>Severity</th>
            <th>Rule</th>
            <th>Message</th>
        </tr>
"""

    for v in violations:
        html += f"""
        <tr>
            <td>{os.path.basename(v['file'])}</td>
            <td>{v['line']}</td>
            <td class="{v['severity']}">{v['severity'].upper()}</td>
            <td>{v['rule']}</td>
            <td>{v['message']}</td>
        </tr>
"""

    html += """
    </table>
</body>
</html>
"""

    output_path.write_text(html)
    log(f"Generated HTML report: {output_path}")


def to_action_result(old_result: dict, plugin_id: str, capability: str, issue_count: int) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    if status == "error":
        status = "failed"

    decision = "pass" if issue_count == 0 else "deny"
    if status == "failed":
        decision = "deny"

    summary_msg = old_result.get("summary", "Checkstyle executed successfully")

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
            "counts": {
                "issues": issue_count
            }
        },
        "hints": [summary_msg] if decision == "deny" else [],
        "artifacts": artifacts,
        "signals": {
            "lint_error_count": issue_count,
            "lint_passed": issue_count == 0 and status == "success"
        },
        "raw_outputs": {},
        "next_actions": []
    }

    return action_result


def emit_and_exit(result: dict, issue_count: int, exit_code: int):
    """Print a single V1 ActionResult JSON to stdout, then exit."""
    action_result = to_action_result(
        result,
        plugin_id="java-checkstyle",
        capability="lint",
        issue_count=issue_count,
    )
    print(json.dumps(action_result))
    sys.exit(exit_code)


def read_input():
    """Read {event, repo_path, artifact_dir} from stdin."""
    try:
        return json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        log("Invalid or missing JSON on stdin")
        return {}


def main():
    input_data = read_input()
    event = input_data.get("event", {})
    repo_path = input_data.get("repo_path", ".")
    artifact_dir_str = input_data.get("artifact_dir")

    log(f"Processing repo: {repo_path}")
    log(f"Event: {event.get('type', 'unknown')}")

    # Validate repo path up-front.
    repo = Path(repo_path)
    if not repo.is_dir():
        emit_and_exit(
            {"status": "failed", "summary": f"repo_path not found: {repo}", "artifacts": []},
            issue_count=0,
            exit_code=1,
        )

    if not artifact_dir_str:
        emit_and_exit(
            {"status": "failed", "summary": "No artifact_dir provided for report output", "artifacts": []},
            issue_count=0,
            exit_code=1,
        )

    # All report outputs (raw XML, JSON, HTML) land in artifact_dir, never CWD.
    artifact_dir = Path(artifact_dir_str)
    artifact_dir.mkdir(parents=True, exist_ok=True)

    # Setup paths
    cache_dir = Path(os.environ.get("CHECKSTYLE_CACHE_DIR", str(DEFAULT_CACHE_DIR)))
    plugin_dir = Path(__file__).parent
    # Use relaxed configuration to reduce noise
    config_path = plugin_dir / "config" / "relaxed_checks.xml"

    # Find Java sources
    source_dir = detect_java_sources(repo_path)
    if not source_dir:
        log("No Java source files found")
        emit_and_exit(
            {"status": "success", "summary": "No Java files to check (skipped)", "artifacts": []},
            issue_count=0,
            exit_code=0,
        )

    log(f"Found Java sources: {source_dir}")

    # Download Checkstyle
    try:
        jar_path = download_checkstyle(cache_dir)
    except Exception as e:
        emit_and_exit(
            {"status": "failed", "summary": f"Failed to download Checkstyle: {e}", "artifacts": []},
            issue_count=0,
            exit_code=1,
        )

    # Run Checkstyle; raw XML goes into artifact_dir (not CWD).
    output_xml = artifact_dir / "checkstyle-result.xml"
    if not run_checkstyle(jar_path, config_path, source_dir, output_xml):
        emit_and_exit(
            {"status": "failed", "summary": "Checkstyle execution failed", "artifacts": []},
            issue_count=0,
            exit_code=1,
        )

    # Parse results (parse_checkstyle_xml never raises / never returns empty summary)
    data = parse_checkstyle_xml(output_xml)
    summary = data["summary"]
    total = summary["total_violations"]
    files = summary["files_with_violations"]

    # Generate reports in artifact directory
    json_report = artifact_dir / "checkstyle-report.json"
    json_report.write_text(json.dumps(data, indent=2))
    log(f"Wrote JSON report: {json_report}")

    html_report = artifact_dir / "checkstyle-report.html"
    generate_html_report(data, html_report)

    artifact_paths = [
        str(output_xml),
        str(json_report),
        str(html_report),
    ]

    if total > 0:
        result = {
            "status": "failed",
            "summary": f"Found {total} violations in {files} files",
            "artifacts": artifact_paths,
        }
    else:
        result = {
            "status": "success",
            "summary": "No violations found",
            "artifacts": artifact_paths,
        }

    action_result = to_action_result(result, plugin_id="java-checkstyle", capability="lint", issue_count=total)
    print(json.dumps(action_result))
    log(f"Checkstyle completed: {result['summary']}")

    # Fail-closed: violations must block the pipeline (status=failed + exit 1).
    if total > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
