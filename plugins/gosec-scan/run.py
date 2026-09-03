#!/usr/bin/env python3
"""
gosec-scan - Go 安全扫描门禁

用 gosec 对 Go 仓库做静态安全扫描:
- gosec 二进制不可用 -> skipped(success) + 提示安装
- HIGH 严重度 -> status=failed + decision=deny + exit 1
- MEDIUM/LOW 严重度 -> status=success + 提示(噪音控制, 与 security_scan 分层一致)
- 无问题 -> status=success + decision=pass

契约:
- stdin 优先注入 JSON {event, repo_path, artifact_dir}, argv(--repo/--out) 作 fallback
- stdout 最后一行输出单个 V1 ActionResult JSON(必须含 action_id), 日志一律 stderr
- 非零退出码 = 失败
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile

PLUGIN_ID = "gosec-scan"
CAPABILITY = "security"
LANGUAGE = "go"

# 阻断严重度: 仅 HIGH(与 security_scan 的 critical/high 阻断语义对齐, gosec 无 critical)
BLOCKING_SEVERITIES = {"HIGH"}
# 非阻断严重度: 仅提示
NON_BLOCKING_SEVERITIES = {"MEDIUM", "LOW"}

GOSEC_INSTALL_HINT = "gosec 未安装, 请执行: go install github.com/securego/gosec/v2/cmd/gosec@latest"


def log(msg):
    print(f"[{PLUGIN_ID}] {msg}", file=sys.stderr)


def read_input():
    """读取 {event, repo_path, artifact_dir}: 优先 stdin JSON, argv 作 fallback。"""
    data = {}

    if not sys.stdin.isatty():
        try:
            raw = sys.stdin.read()
            if raw and raw.strip():
                parsed = json.loads(raw)
                if isinstance(parsed, dict):
                    data.update(parsed)
        except (json.JSONDecodeError, ValueError):
            pass

    argv = sys.argv[1:]
    for i, arg in enumerate(argv):
        if arg == "--repo" and i + 1 < len(argv):
            data.setdefault("repo_path", argv[i + 1])
        elif arg == "--out" and i + 1 < len(argv):
            data.setdefault("artifact_dir", argv[i + 1])
        elif arg.startswith("--repo="):
            data.setdefault("repo_path", arg[len("--repo="):])
        elif arg.startswith("--out="):
            data.setdefault("artifact_dir", arg[len("--out="):])

    return data


def to_action_result(plugin_id, capability, status, decision, message, counts,
                     hints, artifacts, signals):
    """构造 V1 ActionResult(含 action_id)。"""
    from datetime import datetime
    import uuid

    now = datetime.utcnow().isoformat() + "Z"
    return {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
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
    print(json.dumps(action_result))
    sys.exit(exit_code)


def artifact_entry(path):
    return {"name": os.path.basename(path), "path": path}


def parse_gosec_report(report_path):
    """解析 gosec JSON 报告, 返回 (counts, high_findings, non_blocking_findings, error)。"""
    counts = {"high": 0, "medium": 0, "low": 0, "total": 0}
    high_findings = []
    non_blocking_findings = []

    try:
        with open(report_path, "r") as f:
            data = json.load(f)
    except FileNotFoundError:
        return counts, high_findings, non_blocking_findings, f"报告不存在: {report_path}"
    except (json.JSONDecodeError, ValueError) as e:
        return counts, high_findings, non_blocking_findings, f"报告解析失败: {e}"

    issues = data.get("Issues", []) if isinstance(data, dict) else []
    for issue in issues:
        if not isinstance(issue, dict):
            continue
        severity = (issue.get("severity") or "").strip().upper()
        counts["total"] += 1
        if severity in BLOCKING_SEVERITIES:
            counts["high"] += 1
            high_findings.append(issue)
        elif severity == "MEDIUM":
            counts["medium"] += 1
            non_blocking_findings.append(issue)
        else:
            # LOW 或未知严重度(如 "")按 LOW 处理, 仅提示
            counts["low"] += 1
            non_blocking_findings.append(issue)

    return counts, high_findings, non_blocking_findings, None


def _finding_label(issue):
    rule = issue.get("rule_id") or "unknown"
    file_ = issue.get("file") or "unknown"
    line = issue.get("line") or "?"
    sev = (issue.get("severity") or "?").upper()
    return f"[{sev}] {rule} {file_}:{line}"


def main():
    input_data = read_input()
    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path or not os.path.isdir(repo_path):
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            f"repo_path 无效或不存在: {repo_path}",
            {"issues": 1}, [f"repo_path 无效: {repo_path}"], [], {},
        )
        emit(ar, 1)

    # 非 Go 仓库 -> 故意跳过
    if not os.path.isfile(os.path.join(repo_path, "go.mod")):
        log("skipped: 非 Go 仓库(缺少 go.mod)")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: 非 Go 仓库(缺少 go.mod)",
            {"issues": 0}, [], [], {},
        )
        emit(ar, 0)

    # gosec 二进制不可用 -> 故意跳过 + 安装提示
    if not shutil.which("gosec"):
        log("skipped: gosec 未安装")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: gosec 未安装, 跳过安全扫描",
            {"issues": 0},
            [GOSEC_INSTALL_HINT],
            [], {},
        )
        emit(ar, 0)

    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

    # GOCACHE 指向共享临时目录, 避免污染用户缓存, 也避免每个任务目录
    # 各自携带一份 ~200MB 的重复构建缓存 (曾导致 actions 目录膨胀到 10GB)。
    env = os.environ.copy()
    gocache = os.path.join(tempfile.gettempdir(), f"actd-gocache-{PLUGIN_ID}")
    os.makedirs(gocache, exist_ok=True)
    env["GOCACHE"] = gocache

    report_path = (
        os.path.join(artifact_dir, "gosec-report.json")
        if artifact_dir
        else os.path.join(tempfile.gettempdir(), f"actd-gosec-{os.path.basename(os.path.abspath(repo_path))}.json")
    )

    cmd = ["gosec", "-fmt=json", f"-out={report_path}", f"{repo_path}/..."]
    log(f"运行: {' '.join(cmd)}")
    try:
        proc = subprocess.run(
            cmd,
            cwd=repo_path,
            env=env,
            capture_output=True,
            text=True,
            timeout=300,
        )
    except subprocess.TimeoutExpired:
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            "gosec 执行超时(300s), 无法完成安全扫描",
            {"issues": 1},
            ["gosec 超时, 请检查仓库规模"],
            [], {},
        )
        emit(ar, 1)

    if (proc.stdout or "").strip():
        log(f"gosec stdout: {(proc.stdout or '').strip()[:500]}")
    if (proc.stderr or "").strip():
        log(f"gosec stderr: {(proc.stderr or '').strip()[:500]}")

    # 解析报告(fail-closed: 报告缺失/解析失败 -> 阻断)
    counts, high_findings, non_blocking_findings, parse_error = parse_gosec_report(report_path)
    if parse_error:
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            f"gosec 报告不可用: {parse_error}",
            {"issues": 1},
            [f"gosec 报告解析失败: {parse_error}"],
            [], {},
        )
        emit(ar, 1)

    saved = []
    if artifact_dir and os.path.isfile(report_path):
        saved.append(artifact_entry(report_path))

    if counts["high"] > 0:
        hints = [_finding_label(i) for i in high_findings[:20]]
        if len(high_findings) > 20:
            hints.append(f"... 另有 {len(high_findings) - 20} 个高危问题")
        hints.append("请修复高危安全问题后重新提交(详见 gosec-report.json)")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            f"gosec 检测到 {counts['high']} 个高危(HIGH)安全问题",
            {"issues": counts["total"], "high": counts["high"], "medium": counts["medium"], "low": counts["low"]},
            hints,
            saved,
            {"security_passed": False, "gosec_high": counts["high"]},
        )
        emit(ar, 1)

    # medium/low -> 仅提示, 不阻断(噪音控制)
    if counts["medium"] > 0 or counts["low"] > 0:
        hints = [_finding_label(i) for i in non_blocking_findings[:20]]
        if len(non_blocking_findings) > 20:
            hints.append(f"... 另有 {len(non_blocking_findings) - 20} 个中低危问题")
        hints.append("中低危问题不阻断, 建议逐步修复(详见 gosec-report.json)")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            f"gosec 扫描通过(存在 {counts['medium'] + counts['low']} 个中低危问题, 见 hints)",
            {"issues": counts["total"], "high": counts["high"], "medium": counts["medium"], "low": counts["low"]},
            hints,
            saved,
            {"security_passed": True, "gosec_high": 0},
        )
        emit(ar, 0)

    log("gosec 扫描通过, 无问题")
    ar = to_action_result(
        PLUGIN_ID, CAPABILITY, "success", "pass",
        "gosec 扫描通过, 无安全问题",
        {"issues": 0, "high": 0, "medium": 0, "low": 0},
        [], saved,
        {"security_passed": True, "gosec_high": 0},
    )
    emit(ar, 0)


if __name__ == "__main__":
    main()
