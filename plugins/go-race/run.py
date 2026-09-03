#!/usr/bin/env python3
"""
go-race - Go 数据竞争检测门禁

用 `go test -race ./...` 检测数据竞争:
- 测试失败或发现 data race -> status=failed + decision=deny + exit 1
- 全部通过 -> status=success + decision=pass + exit 0
- 非 Go 仓库 / go 工具缺失 -> skipped(success, 绝不误报失败)

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

PLUGIN_ID = "go-race"
CAPABILITY = "test"
LANGUAGE = "go"

RACE_MARKERS = ("WARNING: DATA RACE", "DATA RACE", "race detected during execution")


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

    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

    # 非 Go 仓库 -> 故意跳过
    if not os.path.isfile(os.path.join(repo_path, "go.mod")):
        log("skipped: 非 Go 仓库(缺少 go.mod)")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: 非 Go 仓库(缺少 go.mod)",
            {"issues": 0}, [], [], {},
        )
        emit(ar, 0)

    # go 工具缺失 -> 故意跳过(工具缺失不误报失败)
    if not shutil.which("go"):
        log("skipped: go 工具不可用")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: go 工具不可用, 无法执行 go test -race",
            {"issues": 0},
            ["请安装 Go 工具链后再运行本门禁"],
            [], {},
        )
        emit(ar, 0)

    # GOCACHE 指向共享临时目录, 避免污染用户缓存, 也避免每个任务目录
    # 各自携带一份 ~200MB 的重复构建缓存 (曾导致 actions 目录膨胀到 10GB)。
    env = os.environ.copy()
    gocache = os.path.join(tempfile.gettempdir(), f"actd-gocache-{PLUGIN_ID}")
    os.makedirs(gocache, exist_ok=True)
    env["GOCACHE"] = gocache

    cmd = ["go", "test", "-race", "./..."]
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
            "go test -race 执行超时(300s), 无法验证数据竞争",
            {"issues": 1},
            ["go test -race 超时, 请检查测试耗时"],
            [], {},
        )
        emit(ar, 1)

    combined = ""
    if proc.stdout:
        combined += proc.stdout
    if proc.stderr:
        if combined and not combined.endswith("\n"):
            combined += "\n"
        combined += proc.stderr

    # 写出 race-report.txt
    saved = []
    if artifact_dir:
        report_path = os.path.join(artifact_dir, "race-report.txt")
        with open(report_path, "w") as f:
            f.write(combined or "(无输出)\n")
        saved.append(artifact_entry(report_path))
        log(f"已写出 race-report.txt")

    # 检测 data race 标志
    races_detected = any(marker in (proc.stdout or "") or marker in (proc.stderr or "")
                         for marker in RACE_MARKERS)
    failed = proc.returncode != 0

    if failed or races_detected:
        if races_detected:
            message = "go test -race 检测到数据竞争(data race)"
            hints = ["检测到数据竞争, 请修复并发访问后重新提交(详见 race-report.txt)"]
        else:
            message = f"go test -race 失败(exit {proc.returncode})"
            tail = (proc.stderr or proc.stdout or "").strip()[-500:]
            hints = ["go test -race 失败, 请修复测试后重新提交(详见 race-report.txt)"]
            if tail:
                hints.append(f"失败详情: {tail}")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            message,
            {"issues": 1},
            hints,
            saved,
            {"tests_passed": False, "races_detected": races_detected},
        )
        emit(ar, 1)

    log("go test -race 全部通过")
    ar = to_action_result(
        PLUGIN_ID, CAPABILITY, "success", "pass",
        "go test -race 全部通过",
        {"issues": 0},
        [], saved,
        {"tests_passed": True, "races_detected": False},
    )
    emit(ar, 0)


if __name__ == "__main__":
    main()
