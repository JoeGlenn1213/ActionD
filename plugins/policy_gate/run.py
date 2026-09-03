#!/usr/bin/env python3
"""
policy_gate - 策略门禁

执行策略判断:
- merge_allowed: 是否允许合并
- release_allowed: 是否允许发布
- deploy_allowed: 是否允许部署

规则:
- lint 必须通过
- 单测通过率 >= 阈值
- coverage 不低于基线
- security critical = 0
- build artifact 完整
"""

import sys
import json
import os
from pathlib import Path
from typing import Dict, Any, List

# 默认策略规则
DEFAULT_RULES = {
    "merge": {
        "lint_pass": True,
        "test_pass_rate": 0.8,
        "min_coverage": 0,
        "no_critical_vulnerabilities": False,
        "artifact_required": False
    },
    "release": {
        "lint_pass": True,
        "test_pass_rate": 1.0,
        "min_coverage": 50,
        "no_critical_vulnerabilities": True,
        "artifact_required": True
    },
    "deploy": {
        "lint_pass": True,
        "test_pass_rate": 1.0,
        "min_coverage": 60,
        "no_critical_vulnerabilities": True,
        "artifact_required": True
    }
}

class PolicyGate:
    def __init__(self, rules: dict = None):
        self.rules = rules or DEFAULT_RULES
        self.results = {
            "merge": None,
            "release": None,
            "deploy": None
        }
        self.violations = []
        self.warnings = []

    def check_lint(self, artifact_dir: str) -> bool:
        """检查 lint 结果"""
        lint_reports = [
            "lint-report.json",
            "ruff-report.json",
            "checkstyle-report.json"
        ]

        for report in lint_reports:
            report_path = Path(artifact_dir) / report
            if report_path.exists():
                with open(report_path) as f:
                    data = json.load(f)
                    # 根据不同工具判断
                    if data.get("status") == "success":
                        return True
                    # golangci-lint 输出格式
                    if isinstance(data, list) and len(data) == 0:
                        return True

        # 没有找到 lint 报告，假设通过
        self.warnings.append("No lint report found, assuming passed")
        return True

    def check_test_pass_rate(self, artifact_dir: str) -> float:
        """检查测试通过率"""
        test_reports = [
            "test-results.json",
            "pytest-report.json",
            "junit-report.xml"
        ]

        for report in test_reports:
            report_path = Path(artifact_dir) / report
            if report_path.exists():
                if report.endswith(".json"):
                    with open(report_path) as f:
                        data = json.load(f)
                        # 解析测试结果
                        total = data.get("total", data.get("tests", 1))
                        passed = data.get("passed", data.get("passed", 0))
                        return passed / total if total > 0 else 1.0

        # 没有找到测试报告，假设通过
        self.warnings.append("No test report found, assuming 100% pass rate")
        return 1.0

    def check_coverage(self, artifact_dir: str) -> float:
        """检查覆盖率"""
        coverage_reports = [
            "coverage-report.json",
            "coverage.json"
        ]

        for report in coverage_reports:
            report_path = Path(artifact_dir) / report
            if report_path.exists():
                with open(report_path) as f:
                    data = json.load(f)
                    return data.get("average_coverage", data.get("line_coverage", 0))

        # 没有找到覆盖率报告
        self.warnings.append("No coverage report found")
        return 0

    def check_security(self, artifact_dir: str) -> bool:
        """检查安全扫描结果"""
        security_report = Path(artifact_dir) / "security-report.json"

        if security_report.exists():
            with open(security_report) as f:
                data = json.load(f)
                summary = data.get("summary", {})
                return summary.get("passed", True)

        # 没有找到安全报告，假设通过
        self.warnings.append("No security report found, assuming passed")
        return True

    def check_artifact(self, artifact_dir: str) -> bool:
        """检查构建产物"""
        manifest_report = Path(artifact_dir) / "artifact-manifest.json"

        if manifest_report.exists():
            with open(manifest_report) as f:
                data = json.load(f)
                artifacts = data.get("artifacts", [])
                return len(artifacts) > 0

        # 检查常见的构建产物目录
        artifact_dirs = ["dist", "build", "target"]
        for d in artifact_dirs:
            if (Path(artifact_dir).parent / d).exists():
                return True

        self.warnings.append("No build artifacts found")
        return False

    def evaluate(self, artifact_dir: str) -> dict:
        """执行策略评估"""
        
        # 收集 ActionResult 中的 signals
        action_results = []
        if artifact_dir and Path(artifact_dir).exists():
            for result_file in Path(artifact_dir).glob("**/result.json"):
                try:
                    with open(result_file) as f:
                        data = json.load(f)
                        if "action_id" in data:
                            action_results.append(data)
                except Exception:
                    pass

        # 收集检查结果
        checks = {
            "lint_pass": self.check_lint(artifact_dir),
            "test_pass_rate": self.check_test_pass_rate(artifact_dir),
            "coverage": self.check_coverage(artifact_dir),
            "no_critical_vulnerabilities": self.check_security(artifact_dir),
            "artifact_present": self.check_artifact(artifact_dir)
        }
        
        # Override with values from signals if present in action_results
        for res in action_results:
            signals = res.get("signals", {})
            if "lint_error_count" in signals:
                checks["lint_pass"] = checks.get("lint_pass", True) and (signals["lint_error_count"] == 0)
            if "tests_passed" in signals:
                if not signals["tests_passed"]:
                    checks["test_pass_rate"] = 0.0
            if "coverage_percent" in signals:
                checks["coverage"] = max(checks.get("coverage", 0), signals["coverage_percent"])

        # 评估每个策略
        for policy_name, policy_rules in self.rules.items():
            passed = True
            reasons = []

            # lint 检查
            if policy_rules.get("lint_pass") and not checks["lint_pass"]:
                passed = False
                reasons.append("Lint checks failed")

            # 测试通过率检查
            required_rate = policy_rules.get("test_pass_rate", 0)
            if checks["test_pass_rate"] < required_rate:
                passed = False
                reasons.append(f"Test pass rate {checks['test_pass_rate']:.0%} < required {required_rate:.0%}")

            # 覆盖率检查
            min_coverage = policy_rules.get("min_coverage", 0)
            if checks["coverage"] < min_coverage:
                passed = False
                reasons.append(f"Coverage {checks['coverage']:.0f}% < required {min_coverage}%")

            # 安全检查
            if policy_rules.get("no_critical_vulnerabilities") and not checks["no_critical_vulnerabilities"]:
                passed = False
                reasons.append("Critical security vulnerabilities found")

            # 产物检查
            if policy_rules.get("artifact_required") and not checks["artifact_present"]:
                passed = False
                reasons.append("No build artifacts present")

            self.results[policy_name] = {
                "allowed": passed,
                "reasons": reasons if not passed else ["All checks passed"]
            }

        return {
            "checks": checks,
            "decisions": self.results,
            "warnings": self.warnings,
            "violations": self.violations
        }

def to_action_result(old_result: dict, plugin_id: str, capability: str) -> dict:
    """
    Convert legacy result to the new ActionResult specification
    """
    from datetime import datetime
    import uuid

    status = old_result.get("status", "failed")
    # Map status
    if status == "error":
        status = "failed"
        
    # Decide decision based on overall policy evaluation (fail-closed)
    merge_allowed = old_result.get("summary", {}).get("merge_allowed", False)
    decision = "pass" if (merge_allowed and status == "success") else "deny"
    if status == "failed":
        decision = "deny"

    # 违规计数：merge 不允许时统计原因条数
    reasons = old_result.get("decisions", {}).get("merge", {}).get("reasons", [])
    issues = len(reasons) if not merge_allowed else 0

    summary_msg = old_result.get("recommendation", "Policy evaluated")

    now = datetime.utcnow().isoformat() + "Z"

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
            "commit_sha": "unknown",
            "trigger": "unknown",
            "profile": "unknown"
        },
        "summary": {
            "message": summary_msg,
            "counts": {
                "issues": issues
            }
        },
        "hints": [old_result.get("recommendation", "")] if "recommendation" in old_result else [],
        "artifacts": [{"name": a, "path": a} for a in old_result.get("artifacts", [])],
        "signals": {
            "merge_allowed": merge_allowed,
            "release_allowed": old_result.get("summary", {}).get("release_allowed", False),
            "deploy_allowed": old_result.get("summary", {}).get("deploy_allowed", False)
        },
        "raw_outputs": {},
        "next_actions": []
    }
    
    # Extract detailed decisions into signals for easier querying
    decisions = old_result.get("decisions", {})
    for k, v in decisions.items():
        if isinstance(v, dict):
            action_result["signals"][f"{k}_allowed"] = v.get("allowed", False)

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

    # 加载自定义规则（如果有）
    rules = DEFAULT_RULES
    rules_file = Path(repo_path) / ".actiond" / "policy-rules.json"
    if rules_file.exists():
        with open(rules_file) as f:
            custom_rules = json.load(f)
            rules.update(custom_rules)

    # 执行策略评估
    gate = PolicyGate(rules)

    # 使用 artifact_dir 或 repo 路径下的 artifacts 目录
    check_dir = artifact_dir or str(Path(repo_path) / ".artifacts")

    evaluation = gate.evaluate(check_dir)

    # 汇总结果
    merge_allowed = evaluation["decisions"]["merge"]["allowed"]
    release_allowed = evaluation["decisions"]["release"]["allowed"]
    deploy_allowed = evaluation["decisions"]["deploy"]["allowed"]

    # fail-closed: merge 门禁（git.push 主门禁）违规即失败
    violation = not merge_allowed
    result = {
        "status": "failed" if violation else "success",
        "summary": {
            "merge_allowed": merge_allowed,
            "release_allowed": release_allowed,
            "deploy_allowed": deploy_allowed
        },
        "checks": evaluation["checks"],
        "decisions": evaluation["decisions"],
        "warnings": evaluation["warnings"]
    }

    # 添加建议
    if not result["summary"]["merge_allowed"]:
        result["recommendation"] = "Fix the following issues before merging: " + "; ".join(
            evaluation["decisions"]["merge"]["reasons"]
        )
    elif not result["summary"]["release_allowed"]:
        result["recommendation"] = "Ready to merge. Address these issues before release: " + "; ".join(
            evaluation["decisions"]["release"]["reasons"]
        )
    else:
        result["recommendation"] = "All quality gates passed. Ready for merge and release."

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        decision_path = os.path.join(artifact_dir, "policy-decision.json")
        with open(decision_path, "w") as f:
            json.dump(result, f, indent=2)
        saved_artifacts.append("policy-decision.json")

    result["artifacts"] = saved_artifacts
    
    # Wrap as ActionResult
    action_result = to_action_result(result, plugin_id="policy-gate", capability="policy")
    print(json.dumps(action_result))

    # fail-closed: 违规时以非零退出码双保险阻断
    if violation:
        sys.exit(1)

if __name__ == "__main__":
    main()
