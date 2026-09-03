#!/usr/bin/env python3
"""
approval-gate - 人工审批门禁 (fail-closed)

状态机:
    pending -> approved / rejected / expired

门禁语义 (fail-closed):
- 保护分支 (production/main/master/prod/release* 或 tag 发布):
    必须存在 approval-status.json 且 status == "approved" 才放行 (decision=pass)。
    否则 (pending/rejected/expired/缺文件/缺状态) 一律 decision=deny + status=failed + exit 1。
- 非保护分支:
    无审批记录 -> status=success + decision=pass + summary 注明 skipped (非保护分支，不误报)。
    有审批记录 -> 按记录判定 (approved=pass，其余=deny)。

输出 (V1 ActionResult，stdout 最后一行单个 JSON):
    {action_id, plugin_id, capability, language, status, decision,
     summary: {message, counts:{issues}}, hints, artifacts, signals, next_actions}
日志一律打 stderr，不得污染 stdout。
"""

import sys
import json
import os
import uuid
from pathlib import Path
from datetime import datetime, timedelta, timezone
from typing import Optional

# 默认审批超时
DEFAULT_TIMEOUT_HOURS = 24

# 保护分支 / 环境集合
PROTECTED_BRANCHES = {"main", "master", "production", "prod", "release"}
PROTECTED_ENVIRONMENTS = {"production", "prod"}

VALID_STATUSES = {"pending", "approved", "rejected", "expired"}


def _log(msg: str) -> None:
    sys.stderr.write(f"[approval-gate] {msg}\n")


def _normalize_branch(branch: str) -> str:
    """去除 refs/heads/ 前缀与尾部斜杠，取最后一段作为分支短名。"""
    if not branch:
        return ""
    b = branch.strip()
    if b.startswith("refs/heads/"):
        b = b[len("refs/heads/"):]
    b = b.strip("/")
    if "/" in b:
        b = b.rsplit("/", 1)[-1]
    return b.lower()


def _is_protected_branch(branch: str) -> bool:
    b = _normalize_branch(branch)
    if not b:
        return False
    return b in PROTECTED_BRANCHES or b.startswith("release")


def is_protected(branch: str, environment: str, ref: str) -> bool:
    """判断当前上下文是否为受保护分支/发布（需人工审批）。"""
    if environment and environment.strip().lower() in PROTECTED_ENVIRONMENTS:
        return True
    if ref:
        r = ref.strip()
        if r.startswith("refs/tags/"):
            return True
        if r.startswith("refs/heads/") and _is_protected_branch(r):
            return True
    if branch:
        return _is_protected_branch(branch)
    return False


class ApprovalGate:
    def __init__(self, repo_path: str, artifact_dir: str):
        self.repo_path = repo_path
        self.artifact_dir = artifact_dir
        self.approval_file = Path(artifact_dir) / "approval-status.json" if artifact_dir else None

    def load_approval_status(self) -> Optional[str]:
        """读取权威审批状态文件，返回 status 或 None（缺文件/缺状态/无法解析）。"""
        if not self.approval_file or not self.approval_file.exists():
            return None
        try:
            with open(self.approval_file) as f:
                data = json.load(f)
        except Exception as e:
            _log(f"failed to parse approval-status.json: {e}")
            return None
        if not isinstance(data, dict):
            return None

        status = data.get("status")
        if isinstance(status, str) and status.strip().lower() in VALID_STATUSES:
            return status.strip().lower()

        # 兼容 decision 字段 (pass/deny/rejected/expired)
        decision = data.get("decision")
        if isinstance(decision, str):
            d = decision.strip().lower()
            if d == "pass":
                return "approved"
            if d == "deny":
                return "rejected"
            if d in ("rejected", "expired"):
                return d

        return None

    def create_request(self, request_data: dict) -> dict:
        """创建审批请求记录（用于可追溯的 artifact）。"""
        request = {
            "request_id": f"approval-{datetime.now().strftime('%Y%m%d%H%M%S')}-{os.urandom(4).hex()}",
            "status": "pending",
            "created_at": datetime.now().isoformat(),
            "expires_at": (datetime.now() + timedelta(hours=request_data.get("timeout_hours", DEFAULT_TIMEOUT_HOURS))).isoformat(),
            "requester": request_data.get("requester", "actiond"),
            "environment": request_data.get("environment", "unknown"),
            "action": request_data.get("action", "deploy"),
            "summary": request_data.get("summary", "Approval required for deployment"),
            "context": {
                "repo": Path(self.repo_path).name if self.repo_path else "unknown",
                "git_sha": request_data.get("git_sha", "unknown"),
                "git_tag": request_data.get("git_tag"),
                "branch": request_data.get("branch", "unknown"),
            },
            "approvers": {
                "required": request_data.get("required_approvers", 1),
                "approved_by": [],
                "rejected_by": [],
            },
            "comments": [],
        }
        return request

    def save_request(self, request: dict) -> list:
        """保存审批请求 artifact，返回已写入的文件名列表。"""
        written = []
        if self.artifact_dir:
            os.makedirs(self.artifact_dir, exist_ok=True)
            request_file = os.path.join(self.artifact_dir, "approval-request.json")
            with open(request_file, "w") as f:
                json.dump(request, f, indent=2)
            written.append("approval-request.json")
        return written

    def save_status(self, request: dict) -> list:
        """首次运行写入 pending 状态，等待人工审批。已存在决策文件时不得覆盖。"""
        if self.approval_file and not self.approval_file.exists():
            os.makedirs(self.artifact_dir, exist_ok=True)
            with open(self.approval_file, "w") as f:
                json.dump({
                    "request_id": request.get("request_id"),
                    "status": "pending",
                    "updated_at": datetime.now().isoformat(),
                }, f, indent=2)
            return ["approval-status.json"]
        return []


def to_action_result(plugin_id, capability, status, decision, message, artifacts,
                     signals, hints=None, issues=0):
    """构造 V1 ActionResult。"""
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    return {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "*",
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
            "counts": {"issues": issues},
        },
        "hints": hints if hints is not None else ([] if decision == "pass" else [message]),
        "artifacts": [{"name": a, "path": a} for a in artifacts],
        "signals": signals,
        "raw_outputs": {},
        "next_actions": [],
    }


def main():
    try:
        input_data = json.load(sys.stdin)
    except Exception:
        print(json.dumps({"status": "failed", "error": "Invalid JSON input"}))
        sys.exit(1)

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "failed", "error": "No repo_path provided"}))
        sys.exit(1)

    event = input_data.get("event") or {}
    payload = event.get("payload") or {}
    ref = event.get("ref") or payload.get("ref") or input_data.get("ref") or ""
    branch = input_data.get("branch") or payload.get("branch") or ""
    environment = input_data.get("environment") or payload.get("environment") or ""

    if not branch and ref.startswith("refs/heads/"):
        branch = ref[len("refs/heads/"):]

    protected = is_protected(branch, environment, ref)

    gate = ApprovalGate(repo_path, artifact_dir)

    # 读取权威审批状态（人工通过 actiond approve 写入 approval-status.json）
    decision_status = gate.load_approval_status()

    # 生成请求 artifact（可追溯）
    request = gate.create_request({
        "environment": environment or "unknown",
        "action": input_data.get("action", "deploy"),
        "summary": input_data.get("summary", "Deployment approval required"),
        "git_sha": input_data.get("git_sha") or event.get("new"),
        "git_tag": input_data.get("git_tag"),
        "branch": branch or ref or "unknown",
        "timeout_hours": input_data.get("timeout_hours", DEFAULT_TIMEOUT_HOURS),
        "required_approvers": input_data.get("required_approvers", 1),
    })
    if decision_status:
        request["status"] = decision_status

    artifacts = gate.save_request(request)
    # 无既有决策时写入 pending，等待人工审批；已有决策文件不覆盖
    if decision_status is None:
        artifacts.extend(gate.save_status(request))

    # fail-closed 门禁判定
    if protected:
        if decision_status == "approved":
            status, decision = "success", "pass"
            issues = 0
            exit_code = 0
            message = "Approval granted for protected branch/tag. Safe to proceed."
        else:
            reason = decision_status or "missing"
            status, decision = "failed", "deny"
            issues = 1
            exit_code = 1
            message = (
                f"Protected branch/tag '{branch or ref or environment or 'unknown'}' "
                f"requires approval; current approval status: {reason}. Blocking."
            )
    else:
        if decision_status is None:
            status, decision = "success", "pass"
            issues = 0
            exit_code = 0
            message = f"Non-protected branch '{branch or 'unknown'}': no approval required. Skipped approval gate."
        elif decision_status == "approved":
            status, decision = "success", "pass"
            issues = 0
            exit_code = 0
            message = "Approval granted."
        else:
            status, decision = "failed", "deny"
            issues = 1
            exit_code = 1
            message = (
                f"Non-protected branch '{branch or 'unknown'}' has approval record "
                f"status={decision_status}; blocking."
            )

    signals = {
        "approval_status": decision_status or "missing",
        "protected": protected,
        "approved": decision_status == "approved",
        "branch": branch or "",
        "environment": environment or "",
    }

    action_result = to_action_result(
        plugin_id="approval-gate",
        capability="governance",
        status=status,
        decision=decision,
        message=message,
        artifacts=artifacts,
        signals=signals,
        issues=issues,
    )

    # stdout 最后一行输出单个 JSON（日志已走 stderr）
    print(json.dumps(action_result))

    if exit_code:
        sys.exit(exit_code)


if __name__ == "__main__":
    main()
