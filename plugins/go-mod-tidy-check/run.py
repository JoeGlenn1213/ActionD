#!/usr/bin/env python3
"""
go-mod-tidy-check - Go 依赖整洁度门禁

用 `go mod tidy -diff`(Go >= 1.23) 检测 go.mod / go.sum 是否与源码漂移:
- 有 diff(依赖漂移) -> status=failed + decision=deny + exit 1, 提示执行 go mod tidy 后提交
- 无 diff -> status=success + decision=pass + exit 0
- Go < 1.23 / 非 Go 仓库 / go 工具缺失 -> skipped(success, 绝不误报失败)

契约:
- stdin 优先注入 JSON {event, repo_path, artifact_dir}, argv(--repo/--out) 作 fallback
- stdout 最后一行输出单个 V1 ActionResult JSON(必须含 action_id), 日志一律 stderr
- 非零退出码 = 失败
"""

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

PLUGIN_ID = "go-mod-tidy-check"
CAPABILITY = "deps"
LANGUAGE = "go"

_GO_VERSION_RE = re.compile(r"^go(\d+)\.(\d+)")


def log(msg):
    print(f"[{PLUGIN_ID}] {msg}", file=sys.stderr)


def read_input():
    """读取 {event, repo_path, artifact_dir}: 优先 stdin JSON, argv 作 fallback。"""
    data = {}

    # 引擎契约: stdin 注入 JSON(非 TTY 时才读, 避免交互式阻塞)
    if not sys.stdin.isatty():
        try:
            raw = sys.stdin.read()
            if raw and raw.strip():
                parsed = json.loads(raw)
                if isinstance(parsed, dict):
                    data.update(parsed)
        except (json.JSONDecodeError, ValueError):
            pass

    # argv fallback: --repo <path> / --out <dir>(stdin 已提供的键不被覆盖)
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
    """stdout 输出单个 JSON 后按给定退出码退出。"""
    print(json.dumps(action_result))
    sys.exit(exit_code)


def artifact_entry(path):
    return {"name": os.path.basename(path), "path": path}


def go_version_tuple():
    """返回 (major, minor) 元组; go 缺失或无法解析时返回 None。"""
    try:
        out = subprocess.run(
            ["go", "env", "GOVERSION"],
            capture_output=True,
            text=True,
            timeout=30,
        ).stdout.strip()
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    m = _GO_VERSION_RE.match(out)
    if not m:
        return None
    return (int(m.group(1)), int(m.group(2)))


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

    # 非 Go 仓库 -> 故意跳过(不适用)
    if not os.path.isfile(os.path.join(repo_path, "go.mod")):
        log("skipped: 非 Go 仓库(缺少 go.mod)")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: 非 Go 仓库(缺少 go.mod)",
            {"issues": 0}, [], [], {},
        )
        emit(ar, 0)

    # 检测 go 工具与版本
    version = go_version_tuple()
    if version is None:
        log("skipped: go 工具不可用")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            "skipped: go 工具不可用, 无法执行 go mod tidy -diff",
            {"issues": 0},
            ["请安装 Go 工具链后再运行本门禁"],
            [], {},
        )
        emit(ar, 0)

    if version < (1, 23):
        log(f"skipped: Go {version[0]}.{version[1]} < 1.23 不支持 -diff")
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "success", "pass",
            f"skipped: Go {version[0]}.{version[1]} < 1.23, 不支持 go mod tidy -diff",
            {"issues": 0},
            ["go mod tidy -diff 需 Go >= 1.23, 请升级工具链后启用本门禁"],
            [], {},
        )
        emit(ar, 0)

    # GOCACHE 指向共享临时目录, 避免污染用户缓存, 也避免每个任务目录
    # 各自携带一份 ~200MB 的重复构建缓存 (曾导致 actions 目录膨胀到 10GB)。
    env = os.environ.copy()
    gocache = os.path.join(tempfile.gettempdir(), f"actd-gocache-{PLUGIN_ID}")
    os.makedirs(gocache, exist_ok=True)
    env["GOCACHE"] = gocache

    # 执行 go mod tidy -diff(只打印 diff, 不写文件)
    try:
        proc = subprocess.run(
            ["go", "mod", "tidy", "-diff"],
            cwd=repo_path,
            env=env,
            capture_output=True,
            text=True,
            timeout=300,
        )
    except subprocess.TimeoutExpired:
        ar = to_action_result(
            PLUGIN_ID, CAPABILITY, "failed", "deny",
            "go mod tidy -diff 执行超时(300s), 无法验证依赖整洁度",
            {"issues": 1},
            ["go mod tidy -diff 超时, 请检查依赖规模或网络"],
            [], {},
        )
        emit(ar, 1)

    diff_output = proc.stdout or ""
    err_output = proc.stderr or ""
    if err_output.strip():
        log(f"go mod tidy -diff stderr: {err_output.strip()[:500]}")

    if proc.returncode != 0:
        if diff_output.strip():
            # 依赖漂移: 写出 diff, 阻断
            saved = []
            if artifact_dir:
                diff_path = os.path.join(artifact_dir, "mod-tidy.diff")
                with open(diff_path, "w") as f:
                    f.write(diff_output)
                saved.append(artifact_entry(diff_path))
                log("依赖漂移, 已写出 mod-tidy.diff")
            ar = to_action_result(
                PLUGIN_ID, CAPABILITY, "failed", "deny",
                "检测到依赖漂移: go.mod/go.sum 与源码不一致",
                {"issues": 1},
                ["依赖漂移: 请执行 `go mod tidy` 后重新提交(artifact 见 mod-tidy.diff)"],
                saved,
                {"tidy_drift": True},
            )
            emit(ar, 1)
        else:
            # 工具本身报错 -> fail-closed(无法验证则阻断)
            detail = err_output.strip() or "(无输出)"
            ar = to_action_result(
                PLUGIN_ID, CAPABILITY, "failed", "deny",
                f"go mod tidy -diff 执行失败: {detail[:300]}",
                {"issues": 1},
                [f"go mod tidy -diff 报错: {detail[:300]}"],
                [], {},
            )
            emit(ar, 1)

    # 干净 -> success/pass
    log("go.mod/go.sum 整洁(无依赖漂移)")
    ar = to_action_result(
        PLUGIN_ID, CAPABILITY, "success", "pass",
        "go.mod/go.sum 已整洁(无依赖漂移)",
        {"issues": 0},
        [], [], {"tidy_drift": False},
    )
    emit(ar, 0)


if __name__ == "__main__":
    main()
