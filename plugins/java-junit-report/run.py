#!/usr/bin/env python3
"""
java-junit-report - parse surefire/failsafe XML and block CI on test failures.

This plugin aggregates JUnit-style XML reports produced by Maven (surefire /
failsafe) and Gradle, then emits a single V1 ActionResult on stdout. Test
failures/errors force a fail-closed deny so a failing test suite actually
blocks the pipeline.
"""

import json
import os
import sys
from pathlib import Path
from xml.etree import ElementTree as ET

PLUGIN_ID = "java-junit-report"
CAPABILITY = "test"
LANGUAGE = "java"

# Search locations, relative to repo_path. Order matters only for dedup.
REPORT_PATTERNS = [
    "target/surefire-reports/TEST-*.xml",
    "target/failsafe-reports/TEST-*.xml",
    "build/test-results/test/TEST-*.xml",
    "build/test-results/*Test/*.xml",
]


def log(message):
    """Print a log line to stderr (stdout is reserved for the ActionResult)."""
    print("[java-junit-report] %s" % message, file=sys.stderr)


def _int(value, default=0):
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _float(value, default=0.0):
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


def _esc(value):
    """Minimal HTML escaping for report content."""
    return (
        str(value)
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&#39;")
    )


def _local(tag):
    """Strip an XML namespace, e.g. '{urn}testcase' -> 'testcase'."""
    return tag.split("}", 1)[-1] if isinstance(tag, str) else tag


def parse_argv(argv):
    """Extract --repo and --out from argv (engine fallback contract)."""
    repo = None
    out = None
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--repo" and i + 1 < len(argv):
            repo = argv[i + 1]
            i += 2
        elif arg == "--out" and i + 1 < len(argv):
            out = argv[i + 1]
            i += 2
        else:
            i += 1
    return repo, out


def read_inputs():
    """Read {event, repo_path, artifact_dir} from stdin, argv as fallback."""
    input_data = {}
    repo, out = parse_argv(sys.argv[1:])

    if not sys.stdin.isatty():
        raw = sys.stdin.read()
        if raw and raw.strip():
            try:
                parsed = json.loads(raw)
                if isinstance(parsed, dict):
                    input_data = parsed
                else:
                    log("stdin JSON is not an object; ignoring")
            except (json.JSONDecodeError, ValueError) as e:
                log("invalid stdin JSON: %s" % e)

    return input_data, repo, out


def find_report_files(repo_path):
    """Return sorted, deduped TEST-*.xml paths under the known report dirs."""
    repo = Path(repo_path)
    found = []
    seen = set()
    for pattern in REPORT_PATTERNS:
        for candidate in repo.glob(pattern):
            if not candidate.is_file():
                continue
            resolved = str(candidate.resolve())
            if resolved in seen:
                continue
            seen.add(resolved)
            found.append(candidate)
    return sorted(found, key=lambda p: p.name)


def parse_suite_file(xml_path):
    """Parse one surefire/failsafe XML into a list of leaf suites.

    Returns (suites, error). On a parse/format problem error is a non-empty
    string describing it; suites is empty in that case.
    """
    try:
        tree = ET.parse(str(xml_path))
    except (ET.ParseError, ValueError, OSError) as e:
        return [], "failed to parse XML: %s" % e

    root = tree.getroot()
    if _local(root.tag) not in ("testsuite", "testsuites"):
        return [], "unexpected root element <%s> (not a surefire/failsafe report)" % _local(root.tag)

    suites = []
    for elem in root.iter():
        if _local(elem.tag) != "testsuite":
            continue

        direct_cases = [c for c in elem if _local(c.tag) == "testcase"]
        if not direct_cases:
            # Aggregate wrapper (nested <testsuite> without direct testcases):
            # skip it so nested suites are not double-counted.
            continue

        name = elem.get("name") or xml_path.name
        failures = _int(elem.get("failures"))
        errors = _int(elem.get("errors"))
        skipped = _int(elem.get("skipped"))
        tests = _int(elem.get("tests"), len(direct_cases))
        time_val = _float(elem.get("time"))

        failed_cases = []
        for case in direct_cases:
            child_tags = [_local(ch.tag) for ch in case]
            if "failure" not in child_tags and "error" not in child_tags:
                continue
            message = ""
            for ch in case:
                if _local(ch.tag) in ("failure", "error"):
                    message = (ch.get("message") or "").strip()
                    if message:
                        break
            failed_cases.append({
                "class": case.get("classname") or name,
                "name": case.get("name") or "?",
                "message": message,
            })

        suites.append({
            "name": name,
            "tests": tests,
            "failures": failures,
            "errors": errors,
            "skipped": skipped,
            "time": time_val,
            "failed_cases": failed_cases,
        })

    return suites, None


def generate_html_report(report, failed_hints, output_path):
    """Write a minimal human-readable HTML report."""
    suites_rows = []
    for s in report["suites"]:
        suites_rows.append(
            "<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%.3f</td></tr>"
            % (_esc(s["name"]), s["tests"], s["failures"], s["errors"], s["time"])
        )

    failed_rows = []
    for hint in failed_hints:
        failed_rows.append("<li>%s</li>" % _esc(hint))

    html = """<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title>JUnit Test Report</title>
<style>
body { font-family: Arial, sans-serif; margin: 20px; }
.summary { background: #f5f5f5; padding: 15px; border-radius: 5px; margin-bottom: 20px; }
.metric { display: inline-block; margin-right: 30px; }
.metric-value { font-size: 24px; font-weight: bold; }
.fail { color: #d32f2f; }
table { border-collapse: collapse; width: 100%%; margin-bottom: 20px; }
th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
th { background-color: #4CAF50; color: white; }
h2 { margin-top: 30px; }
</style>
</head>
<body>
<h1>JUnit Test Report</h1>
<div class="summary">
<div class="metric"><div class="metric-value">%(total)d</div><div>Total</div></div>
<div class="metric"><div class="metric-value fail">%(failures)d</div><div>Failures</div></div>
<div class="metric"><div class="metric-value fail">%(errors)d</div><div>Errors</div></div>
<div class="metric"><div class="metric-value">%(skipped)d</div><div>Skipped</div></div>
</div>
<h2>Suites</h2>
<table>
<tr><th>Suite</th><th>Tests</th><th>Failures</th><th>Errors</th><th>Time (s)</th></tr>
%(suites_rows)s
</table>
<h2>Failed Cases</h2>
<ul>
%(failed_rows)s
</ul>
</body>
</html>
""" % {
        "total": report["total"],
        "failures": report["failures"],
        "errors": report["errors"],
        "skipped": report["skipped"],
        "suites_rows": "\n".join(suites_rows),
        "failed_rows": "\n".join(failed_rows) if failed_rows else "<li>None</li>",
    }

    with open(output_path, "w") as f:
        f.write(html)


def build_action_result(status, decision, message, counts, hints, artifacts, signals):
    """Construct the V1 ActionResult dictionary."""
    from datetime import datetime
    import uuid

    now = datetime.utcnow().isoformat() + "Z"
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
            "counts": counts,
        },
        "hints": hints,
        "artifacts": artifacts,
        "signals": signals,
        "raw_outputs": {},
        "next_actions": [],
    }


def emit(action_result, exit_code):
    """Print the ActionResult JSON to stdout (last line) and exit."""
    print(json.dumps(action_result))
    sys.exit(exit_code)


def main():
    input_data, argv_repo, argv_out = read_inputs()

    repo_path = input_data.get("repo_path") or argv_repo
    artifact_dir = input_data.get("artifact_dir") or argv_out

    # Fail-closed on a missing/invalid repo path.
    if not repo_path or not os.path.isdir(repo_path):
        result = build_action_result(
            "failed", "deny",
            "repo_path not found or not a directory: %s" % repo_path,
            {"issues": 1}, [],
            [], {"tests_passed": False},
        )
        log("repo_path invalid: %s" % repo_path)
        emit(result, 1)

    log("Processing repo: %s" % repo_path)

    files = find_report_files(repo_path)
    if not files:
        # No test reports: intentional skip (success, not a failure).
        result = build_action_result(
            "success", "pass",
            "skipped: 未找到测试报告 (surefire/failsafe XML)。请先运行测试构建 (如 mvn test / gradle test)。",
            {"issues": 0, "tests": 0, "failures": 0, "errors": 0, "skipped": 0},
            [],
            [],
            {"tests": 0, "failures": 0, "errors": 0, "skipped": 0, "tests_passed": True},
        )
        log("no test reports found under %s; skipping" % repo_path)
        emit(result, 0)

    log("Found %d test report file(s)" % len(files))

    all_suites = []
    parse_errors = []
    for f in files:
        suites, err = parse_suite_file(f)
        if err:
            parse_errors.append("%s: %s" % (f.name, err))
            log("parse error in %s: %s" % (f, err))
        else:
            all_suites.extend(suites)

    # Fail-closed: any unparseable report means we cannot confirm results.
    if parse_errors:
        message = "XML 解析失败，无法确认测试结果: %s" % "; ".join(parse_errors[:5])
        result = build_action_result(
            "failed", "deny", message,
            {"issues": 1}, [message], [],
            {"tests_passed": False},
        )
        emit(result, 1)

    total_tests = sum(s["tests"] for s in all_suites)
    total_failures = sum(s["failures"] for s in all_suites)
    total_errors = sum(s["errors"] for s in all_suites)
    total_skipped = sum(s["skipped"] for s in all_suites)
    issues = total_failures + total_errors

    # Collect failing test cases (top 10) for hints.
    failed_hints = []
    for s in all_suites:
        for fc in s["failed_cases"]:
            label = "%s.%s" % (fc["class"], fc["name"])
            if fc["message"]:
                label += ": %s" % fc["message"]
            failed_hints.append(label)
            if len(failed_hints) >= 10:
                break
        if len(failed_hints) >= 10:
            break

    if issues > 0 and not failed_hints:
        failed_hints = ["存在 %d 个失败/错误用例" % issues]

    report = {
        "total": total_tests,
        "failures": total_failures,
        "errors": total_errors,
        "skipped": total_skipped,
        "suites": [
            {
                "name": s["name"],
                "tests": s["tests"],
                "failures": s["failures"],
                "errors": s["errors"],
                "time": s["time"],
            }
            for s in all_suites
        ],
    }

    # Write artifacts (real paths) into artifact_dir when provided.
    artifacts = []
    if artifact_dir:
        try:
            os.makedirs(artifact_dir, exist_ok=True)
            json_path = os.path.join(artifact_dir, "junit-report.json")
            html_path = os.path.join(artifact_dir, "junit-report.html")
            with open(json_path, "w") as f:
                json.dump(report, f, indent=2)
            generate_html_report(report, failed_hints, html_path)
            artifacts = [
                {"name": "junit-report.json", "path": json_path},
                {"name": "junit-report.html", "path": html_path},
            ]
            log("wrote %s and %s" % (json_path, html_path))
        except OSError as e:
            message = "failed to write artifacts: %s" % e
            result = build_action_result(
                "failed", "deny", message,
                {"issues": 1}, [message], [],
                {"tests": total_tests, "failures": total_failures, "errors": total_errors,
                 "skipped": total_skipped, "tests_passed": False},
            )
            emit(result, 1)

    signals = {
        "tests": total_tests,
        "failures": total_failures,
        "errors": total_errors,
        "skipped": total_skipped,
        "tests_passed": issues == 0,
    }
    counts = {
        "issues": issues,
        "tests": total_tests,
        "failures": total_failures,
        "errors": total_errors,
        "skipped": total_skipped,
    }

    if issues > 0:
        message = "测试失败: %d 个失败, %d 个错误 (共 %d 个用例, %d skipped)" % (
            total_failures, total_errors, total_tests, total_skipped)
        result = build_action_result(
            "failed", "deny", message, counts,
            failed_hints[:10], artifacts, signals)
        log(message)
        emit(result, 1)

    message = "全部 %d 个用例通过 (%d skipped)" % (total_tests, total_skipped)
    result = build_action_result(
        "success", "pass", message, counts,
        [], artifacts, signals)
    log(message)
    emit(result, 0)


if __name__ == "__main__":
    main()
