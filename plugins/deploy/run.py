#!/usr/bin/env python3
"""
deploy - 发布和回滚管理

功能:
- 多环境发布 (dev/staging/prod)
- 发布前检查
- 发布执行
- 回滚计划生成
- 发布记录

输出:
- deploy-manifest.json
- rollback-plan.json
"""

import sys
import json
import subprocess
import os
from pathlib import Path
from datetime import datetime
from typing import Optional

# 环境配置
ENVIRONMENTS = {
    "dev": {
        "auto_deploy": True,
        "requires_approval": False,
    },
    "staging": {
        "auto_deploy": True,
        "requires_approval": False,
    },
    "prod": {
        "auto_deploy": False,
        "requires_approval": True,
    }
}

def get_git_info(repo_path: str) -> dict:
    """获取 Git 信息"""
    info = {}

    try:
        # SHA
        result = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        info["sha"] = result.stdout.strip()[:12] if result.returncode == 0 else "unknown"

        # Tag
        result = subprocess.run(
            ["git", "describe", "--tags", "--exact-match", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        info["tag"] = result.stdout.strip() if result.returncode == 0 else None

        # Branch
        result = subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        info["branch"] = result.stdout.strip() if result.returncode == 0 else "unknown"

        # Commit message
        result = subprocess.run(
            ["git", "log", "-1", "--pretty=%s"],
            cwd=repo_path,
            capture_output=True,
            text=True
        )
        info["commit_message"] = result.stdout.strip() if result.returncode == 0 else "unknown"

    except Exception:
        pass

    return info

def get_previous_deploy(repo_path: str, environment: str) -> Optional[dict]:
    """获取上次发布信息"""
    deploy_file = Path(repo_path) / ".deploy" / f"{environment}.json"
    if deploy_file.exists():
        with open(deploy_file) as f:
            return json.load(f)
    return None

def pre_deploy_checks(repo_path: str) -> dict:
    """发布前检查"""
    checks = {
        "tests_passed": False,
        "build_available": False,
        "security_scan_passed": False,
        "issues": []
    }

    # 检查测试结果
    artifact_dirs = [
        Path(repo_path) / ".artifacts",
        Path(repo_path) / "test-results",
    ]

    for artifact_dir in artifact_dirs:
        if artifact_dir.exists():
            # 检查测试结果
            for result_file in artifact_dir.glob("*test*.json"):
                try:
                    with open(result_file) as f:
                        data = json.load(f)
                        if data.get("status") == "success" or data.get("passed", 0) > 0:
                            checks["tests_passed"] = True
                            break
                except:
                    pass

            # 检查构建产物
            for manifest in artifact_dir.glob("*manifest*.json"):
                try:
                    with open(manifest) as f:
                        data = json.load(f)
                        if data.get("artifacts"):
                            checks["build_available"] = True
                            break
                except:
                    pass

            # 检查安全扫描
            for security in artifact_dir.glob("*security*.json"):
                try:
                    with open(security) as f:
                        data = json.load(f)
                        summary = data.get("summary", {})
                        if summary.get("passed", True):
                            checks["security_scan_passed"] = True
                            break
                except:
                    pass

    # 收集问题
    if not checks["tests_passed"]:
        checks["issues"].append("No passing test results found")
    if not checks["build_available"]:
        checks["issues"].append("No build artifacts found")

    return checks

def generate_deploy_manifest(repo_path: str, environment: str, git_info: dict) -> dict:
    """生成发布清单"""
    manifest = {
        "deploy_id": f"{environment}-{git_info.get('sha', 'unknown')}-{datetime.now().strftime('%Y%m%d%H%M%S')}",
        "environment": environment,
        "git": git_info,
        "timestamp": datetime.now().isoformat(),
        "status": "pending",
        "artifacts": [],
        "config": ENVIRONMENTS.get(environment, {})
    }

    # 收集构建产物
    artifact_dir = Path(repo_path) / ".artifacts"
    if artifact_dir.exists():
        for f in artifact_dir.glob("**/*"):
            if f.is_file() and not f.name.endswith(".json"):
                manifest["artifacts"].append({
                    "name": f.name,
                    "path": str(f.relative_to(repo_path)),
                    "size": f.stat().st_size
                })

    return manifest

def generate_rollback_plan(repo_path: str, environment: str, current_deploy: dict, previous_deploy: Optional[dict]) -> dict:
    """生成回滚计划"""
    plan = {
        "environment": environment,
        "current_version": {
            "sha": current_deploy.get("git", {}).get("sha", "unknown"),
            "tag": current_deploy.get("git", {}).get("tag"),
            "deployed_at": current_deploy.get("timestamp")
        },
        "rollback_to": None,
        "rollback_steps": [],
        "status": "planned"
    }

    if previous_deploy:
        plan["rollback_to"] = {
            "sha": previous_deploy.get("git", {}).get("sha", "unknown"),
            "tag": previous_deploy.get("git", {}).get("tag"),
            "deployed_at": previous_deploy.get("timestamp")
        }
        plan["rollback_steps"] = [
            f"1. Checkout to {previous_deploy.get('git', {}).get('sha', 'previous')}",
            "2. Rebuild artifacts",
            "3. Deploy to " + environment,
            "4. Verify health checks",
            "5. Update deployment record"
        ]
    else:
        plan["rollback_steps"] = [
            "No previous deployment found - rollback not possible",
            "Consider manual intervention"
        ]
        plan["status"] = "no_rollback_available"

    return plan

def execute_deploy(repo_path: str, environment: str, dry_run: bool = True) -> dict:
    """执行发布"""
    result = {
        "status": "skipped",
        "dry_run": dry_run,
        "steps_executed": []
    }

    if dry_run:
        result["status"] = "dry_run_complete"
        result["steps_planned"] = [
            "1. Pre-deploy checks",
            "2. Pull latest images/artifacts",
            "3. Update configuration",
            "4. Deploy new version",
            "5. Run health checks",
            "6. Update routing/ingress",
            "7. Verify deployment"
        ]
        return result

    # 实际发布逻辑 (需要具体实现)
    result["status"] = "success"
    result["message"] = "Deploy executed (placeholder)"

    return result

def to_action_result(old_result: dict, plugin_id: str, capability: str) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    import uuid

    can_deploy = old_result.get("can_deploy", False)
    status = "success" if can_deploy else "failed"
    
    decision = "pass" if can_deploy else "deny"
    env = old_result.get("environment", "unknown")
    
    summary_msg = f"Deployment to {env} planned/executed successfully"
    if not can_deploy:
        summary_msg = f"Deployment to {env} blocked: " + ", ".join(old_result.get("blockers", []))

    now = datetime.now().isoformat() + "Z"

    action_result = {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": "*",
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
            "commit_sha": old_result.get("git", {}).get("sha", "unknown"),
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": summary_msg,
            "counts": {}
        },
        "hints": old_result.get("blockers", []),
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", []) if isinstance(a, str)],
        "signals": {
            "can_deploy": can_deploy,
            "environment": env,
            "dry_run": old_result.get("dry_run", True)
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
    environment = input_data.get("environment", "dev")  # dev/staging/prod
    dry_run = input_data.get("dry_run", True)

    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        sys.exit(1)

    # 获取信息
    git_info = get_git_info(repo_path)
    previous_deploy = get_previous_deploy(repo_path, environment)
    pre_checks = pre_deploy_checks(repo_path)

    # 生成发布清单
    deploy_manifest = generate_deploy_manifest(repo_path, environment, git_info)

    # 生成回滚计划
    rollback_plan = generate_rollback_plan(repo_path, environment, deploy_manifest, previous_deploy)

    # 执行发布
    deploy_result = execute_deploy(repo_path, environment, dry_run)

    # 汇总结果
    result = {
        "status": deploy_result["status"],
        "environment": environment,
        "dry_run": dry_run,
        "git": git_info,
        "pre_deploy_checks": pre_checks,
        "deploy_manifest": deploy_manifest,
        "rollback_plan": rollback_plan,
        "deploy_result": deploy_result,
        "timestamp": datetime.now().isoformat()
    }

    # 判断是否可以发布
    can_deploy = True
    blockers = []

    if pre_checks.get("issues"):
        blockers.extend(pre_checks["issues"])
        can_deploy = False

    env_config = ENVIRONMENTS.get(environment, {})
    if env_config.get("requires_approval") and dry_run:
        blockers.append(f"Production deployment requires approval (set dry_run=false after approval)")
        can_deploy = False

    result["can_deploy"] = can_deploy
    result["blockers"] = blockers

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

        # 发布清单
        manifest_path = os.path.join(artifact_dir, "deploy-manifest.json")
        with open(manifest_path, "w") as f:
            json.dump(deploy_manifest, f, indent=2)
        saved_artifacts.append("deploy-manifest.json")

        # 回滚计划
        rollback_path = os.path.join(artifact_dir, "rollback-plan.json")
        with open(rollback_path, "w") as f:
            json.dump(rollback_plan, f, indent=2)
        saved_artifacts.append("rollback-plan.json")

        # 记录发布
        deploy_dir = Path(repo_path) / ".deploy"
        deploy_dir.mkdir(exist_ok=True)
        deploy_record = deploy_dir / f"{environment}.json"
        with open(deploy_record, "w") as f:
            json.dump(deploy_manifest, f, indent=2)

    result["artifacts"] = saved_artifacts
    
    # Wrap as ActionResult
    action_result = to_action_result(result, plugin_id="deploy", capability="deploy")
    print(json.dumps(action_result))

if __name__ == "__main__":
    main()
