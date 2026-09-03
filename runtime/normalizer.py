import json
from datetime import datetime

def create_action_result(action_id, plugin_id, capability, status, decision, message, duration_ms, language=None, signals=None, hints=None):
    """
    辅助函数：创建标准的 ActionResult 对象
    """
    now = datetime.utcnow().isoformat() + "Z"
    
    result = {
        "action_id": action_id,
        "plugin_id": plugin_id,
        "capability": capability,
        "language": language,
        "status": status,
        "decision": decision,
        "timing": {
            "started_at": now,  # 简化，实际应为真实开始时间
            "finished_at": now,
            "duration_ms": duration_ms
        },
        "context": {
            "repo": "unknown",     # 实际应从输入上下文提取
            "module": "unknown",
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": message,
            "counts": {}
        },
        "hints": hints or [],
        "artifacts": [],
        "signals": signals or {},
        "raw_outputs": {},
        "next_actions": []
    }
    
    return result

# 测试输出
if __name__ == "__main__":
    result = create_action_result(
        action_id="act_123",
        plugin_id="python-ruff",
        capability="lint",
        language="python",
        status="success",
        decision="pass",
        message="Ruff completed with 0 errors",
        duration_ms=1500,
        signals={"lint_error_count": 0}
    )
    print(json.dumps(result, indent=2))