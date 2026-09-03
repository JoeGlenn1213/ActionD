#!/usr/bin/env python3
"""
security_scan - 安全扫描

扫描:
- dependency_vulnerabilities: 依赖漏洞
- secret_leakage: 密钥泄漏
- license_compliance: 许可证合规
- sast: 静态代码分析

工具:
- Python: pip-audit, bandit
- Go: govulncheck, gosec
- Node: npm audit
- Java: OWASP Dependency Check (可选)

fail-closed 门禁:
- 密钥命中 -> 一律 critical -> status=failed + decision=deny + exit 1
- 依赖漏洞 severity 归一化后: critical/high/unknown -> 阻断; medium/low/info -> success + 提示
  (unknown 按最保守处理视为高危: 无严重度分层时宁可阻断, 取舍见报告)
"""

import sys
import json
import subprocess
import os
from pathlib import Path

# 敏感信息模式
SECRET_PATTERNS = [
    ("api_key", r"(?i)(api[_-]?key|apikey)\s*[=:]\s*['\"]?[a-zA-Z0-9_\-]{20,}['\"]?"),
    ("aws_access_key", r"AKIA[0-9A-Z]{16}"),
    ("aws_secret", r"(?i)aws[_-]?secret[_-]?access[_-]?key\s*[=:]\s*['\"]?[a-zA-Z0-9/+=]{40}['\"]?"),
    ("private_key", r"-----BEGIN (?:RSA |EC )?PRIVATE KEY-----"),
    ("password", r"(?i)password\s*[=:]\s*['\"]?[^'\"]{8,}['\"]?"),
    ("token", r"(?i)(auth[_-]?token|bearer)\s*[=:]\s*['\"]?[a-zA-Z0-9_\-\.]{20,}['\"]?"),
    ("github_token", r"ghp_[a-zA-Z0-9]{36}"),
]

# fail-closed 门禁: 这些严重度必须阻断
BLOCKING_SEVERITIES = {"critical", "high", "unknown"}
# 非阻断严重度: 仅提示, 避免每次 push 全红
NON_BLOCKING_SEVERITIES = {"medium", "low", "info"}


def normalize_severity(sev) -> str:
    """把各种工具输出的严重度归一化为 critical/high/medium/low/info/unknown。

    - npm audit 用 moderate -> medium
    - govulncheck 用大写 HIGH/MEDIUM/LOW/CRITICAL
    - 无法识别/缺失 -> unknown (按 fail-closed 视为阻断, 见 NOTES)
    """
    if sev is None:
        return "unknown"
    if not isinstance(sev, str):
        return "unknown"
    s = sev.strip().lower()
    if s in ("moderate", "mod"):
        return "medium"
    if s in ("informational", "notice"):
        return "info"
    if s in ("critical", "high", "medium", "low", "info", "unknown"):
        return s
    # 例如 "CRITICAL"/"HIGH" 已被 lower, 其它未知字符串
    return "unknown"


def is_blocking_severity(sev) -> bool:
    return normalize_severity(sev) in BLOCKING_SEVERITIES


def scan_secrets(repo_path: str) -> dict:
    """扫描敏感信息泄漏"""
    import re

    findings = []
    repo = Path(repo_path)

    # 排除目录
    exclude_dirs = {".git", "node_modules", "vendor", "__pycache__", "dist", "build", "target", ".deepwiki_temp", ".venv", "venv", ".tox", ".mypy_cache", "site-packages"}

    for file_path in repo.rglob("*"):
        if not file_path.is_file():
            continue
        if any(part in exclude_dirs for part in file_path.parts):
            continue

        try:
            with open(file_path, "r", errors="ignore") as f:
                content = f.read()
                rel_path = str(file_path.relative_to(repo))

                for pattern_name, pattern in SECRET_PATTERNS:
                    matches = re.findall(pattern, content)
                    if matches:
                        findings.append({
                            "type": "secret_leak",
                            "severity": "critical",
                            "pattern": pattern_name,
                            "file": rel_path,
                            "count": len(matches)
                        })
        except Exception:
            pass

    return {"findings": findings, "count": len(findings)}


def _osv_severity(osv: dict) -> str:
    """从 govulncheck/OSV 记录中提取严重度, 缺失时返回 unknown。"""
    sev = osv.get("severity")
    if isinstance(sev, str):
        return normalize_severity(sev)
    db = osv.get("database_specific")
    if isinstance(db, dict):
        s = db.get("severity")
        if isinstance(s, str):
            return normalize_severity(s)
    return "unknown"


def scan_go_vulnerabilities(repo_path: str) -> dict:
    """Go 漏洞扫描"""
    vulnerabilities = []

    try:
        # govulncheck
        result = subprocess.run(
            ["govulncheck", "-json", "./..."],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=120
        )

        for line in result.stdout.strip().split("\n"):
            if line.strip() and line.startswith("{"):
                try:
                    entry = json.loads(line)
                    if entry.get("osv"):
                        vuln = entry["osv"]
                        vulnerabilities.append({
                            "id": vuln.get("id"),
                            "severity": _osv_severity(vuln),
                            "package": (vuln.get("affected") or [{}])[0].get("package", {}).get("name", "unknown"),
                            "summary": vuln.get("summary", "")
                        })
                except Exception:
                    pass
    except FileNotFoundError:
        return {"status": "skipped", "reason": "govulncheck not installed", "vulnerabilities": [], "count": 0}
    except Exception as e:
        return {"status": "error", "error": str(e), "vulnerabilities": [], "count": 0}

    return {"vulnerabilities": vulnerabilities, "count": len(vulnerabilities)}


def scan_python_vulnerabilities(repo_path: str) -> dict:
    """Python 漏洞扫描"""
    vulnerabilities = []

    try:
        # pip-audit: 发现漏洞时以非零退出码返回, 但 JSON 仍写往 stdout。
        # 必须无条件解析 stdout (旧实现仅 returncode==0 才解析, 导致漏洞永不失败)。
        result = subprocess.run(
            ["pip-audit", "-f", "json"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=120
        )

        if result.stdout.strip():
            try:
                audits = json.loads(result.stdout)
                for audit in audits:
                    vulns = audit.get("vulns", [])
                    for v in vulns:
                        vulnerabilities.append({
                            "id": v.get("id"),
                            # pip-audit JSON 通常不携带 severity -> unknown (fail-closed)
                            "severity": normalize_severity(v.get("severity")),
                            "package": audit.get("name", "unknown"),
                            "version": audit.get("version", "unknown"),
                            "fix_version": v.get("fix_versions", ["unknown"])[0] if v.get("fix_versions") else None
                        })
            except Exception:
                pass
    except FileNotFoundError:
        # 备用: safety
        try:
            result = subprocess.run(
                ["safety", "check", "--json"],
                cwd=repo_path,
                capture_output=True,
                text=True,
                timeout=60
            )
            if result.stdout.strip():
                data = json.loads(result.stdout)
                for vuln in data:
                    vulnerabilities.append({
                        "id": vuln[0],
                        "severity": "high",
                        "package": vuln[4],
                        "version": vuln[5],
                        "description": vuln[3]
                    })
        except Exception:
            return {"status": "skipped", "reason": "pip-audit/safety not installed", "vulnerabilities": [], "count": 0}
    except Exception as e:
        return {"status": "error", "error": str(e), "vulnerabilities": [], "count": 0}

    return {"vulnerabilities": vulnerabilities, "count": len(vulnerabilities)}


def scan_npm_vulnerabilities(repo_path: str) -> dict:
    """NPM 漏洞扫描"""
    vulnerabilities = []

    try:
        result = subprocess.run(
            ["npm", "audit", "--json"],
            cwd=repo_path,
            capture_output=True,
            text=True,
            timeout=60
        )

        if result.stdout.strip():
            audit = json.loads(result.stdout)
            for vuln_list in audit.get("vulnerabilities", {}).values():
                if isinstance(vuln_list, dict) and "via" in vuln_list:
                    for v in vuln_list.get("via", []):
                        if isinstance(v, dict):
                            vulnerabilities.append({
                                "id": v.get("url", "unknown"),
                                "severity": normalize_severity(v.get("severity")),
                                "package": v.get("name", vuln_list.get("name", "unknown")),
                                "title": v.get("title", "")
                            })
    except FileNotFoundError:
        return {"status": "skipped", "reason": "npm not installed", "vulnerabilities": [], "count": 0}
    except Exception as e:
        return {"status": "error", "error": str(e), "vulnerabilities": [], "count": 0}

    return {"vulnerabilities": vulnerabilities, "count": len(vulnerabilities)}


def scan_licenses(repo_path: str, languages: list) -> dict:
    """许可证扫描"""
    licenses = []
    repo = Path(repo_path)

    # 检查项目许可证
    for lic_file in ["LICENSE", "LICENSE.md", "LICENSE.txt"]:
        lic_path = repo / lic_file
        if lic_path.exists():
            licenses.append({
                "type": "project",
                "file": lic_file,
                "detected": True
            })
            break

    # 简化的许可证检测
    # 实际实现可以使用 license-checker 等工具

    return {"licenses": licenses, "warning": "Full license scanning requires additional tools"}


def classify_vulnerabilities(vuln_results: dict) -> tuple:
    """汇总各语言漏洞结果, 返回 (severity计数dict, 阻断漏洞列表)。"""
    counts = {"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0, "unknown": 0, "total": 0}
    blocking = []

    for lang, vres in (vuln_results or {}).items():
        if not isinstance(vres, dict):
            continue
        for v in vres.get("vulnerabilities", []):
            sev = normalize_severity(v.get("severity"))
            counts[sev] = counts.get(sev, 0) + 1
            counts["total"] += 1
            if sev in BLOCKING_SEVERITIES:
                blocking.append({
                    "language": lang,
                    "severity": sev,
                    "package": v.get("package", v.get("name", "unknown")),
                    "id": v.get("id", v.get("title", "unknown")),
                })

    return counts, blocking


def to_action_result(plugin_id: str, capability: str, status: str, decision: str,
                     message: str, counts: dict, hints: list, artifacts: list,
                     signals: dict, language: str = "*") -> dict:
    """构造 V1 ActionResult。"""
    from datetime import datetime
    import uuid

    now = datetime.utcnow().isoformat() + "Z"
    return {
        "action_id": f"act_{uuid.uuid4().hex[:8]}",
        "plugin_id": plugin_id,
        "capability": capability,
        "language": language,
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
            "message": message,
            "counts": counts
        },
        "hints": hints,
        "artifacts": [{"name": a, "path": a} if isinstance(a, str) else a for a in artifacts],
        "signals": signals,
        "raw_outputs": {},
        "next_actions": []
    }


def main() -> int:
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print(json.dumps({"status": "error", "error": "Invalid JSON input"}))
        return 1

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    if not repo_path:
        print(json.dumps({"status": "error", "error": "No repo_path provided"}))
        return 1

    # 检测语言
    languages = []
    if (Path(repo_path) / "go.mod").exists():
        languages.append("go")
    if (Path(repo_path) / "pyproject.toml").exists() or list(Path(repo_path).glob("requirements*.txt")):
        languages.append("python")
    if (Path(repo_path) / "package.json").exists():
        languages.append("node")
    if (Path(repo_path) / "pom.xml").exists() or (Path(repo_path) / "build.gradle").exists():
        languages.append("java")

    # 执行扫描
    results = {
        "status": "success",
        "languages_scanned": languages
    }

    # 密钥扫描 (一律 critical)
    results["secrets"] = scan_secrets(repo_path)

    # 漏洞扫描
    results["vulnerabilities"] = {}
    if "go" in languages:
        results["vulnerabilities"]["go"] = scan_go_vulnerabilities(repo_path)
    if "python" in languages:
        results["vulnerabilities"]["python"] = scan_python_vulnerabilities(repo_path)
    if "node" in languages:
        results["vulnerabilities"]["node"] = scan_npm_vulnerabilities(repo_path)

    # 许可证扫描
    results["licenses"] = scan_licenses(repo_path, languages)

    # 统计与门禁
    secret_count = results["secrets"].get("count", 0)
    vuln_counts, blocking_vulns = classify_vulnerabilities(results["vulnerabilities"])

    counts = {
        "secrets": secret_count,
        "vulnerabilities": vuln_counts["total"],
        "critical": vuln_counts["critical"],
        "high": vuln_counts["high"],
        "medium": vuln_counts["medium"],
        "low": vuln_counts["low"],
        "info": vuln_counts["info"],
        "unknown": vuln_counts["unknown"],
        "blocking": secret_count + len(blocking_vulns)
    }

    blocked = secret_count > 0 or len(blocking_vulns) > 0

    hints = []
    for s in results["secrets"].get("findings", []):
        hints.append(f"[secret:{s.get('pattern')}] {s.get('file')} (x{s.get('count')})")
    for b in blocking_vulns:
        hints.append(f"[{b['severity']}] {b['package']}: {b['id']} ({b['language']})")

    non_blocking = vuln_counts["medium"] + vuln_counts["low"] + vuln_counts["info"]
    if non_blocking > 0:
        hints.append(f"{non_blocking} low/medium/info vulnerability(ies) recorded (non-blocking)")

    status = "failed" if blocked else "success"
    decision = "deny" if blocked else "pass"

    if blocked:
        message = (f"Security gate blocked: {secret_count} secret leak(s), "
                   f"{len(blocking_vulns)} critical/high vulnerability(ies)")
    elif non_blocking > 0:
        message = (f"Security scan passed with {non_blocking} low/medium finding(s) (hints below)")
    else:
        message = "Security scan passed with no issues"

    results["summary"] = {
        "total_vulnerabilities": vuln_counts["total"],
        "critical_issues": secret_count + vuln_counts["critical"],
        "blocking_issues": secret_count + len(blocking_vulns),
        "passed": not blocked
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        report_path = os.path.join(artifact_dir, "security-report.json")
        with open(report_path, "w") as f:
            json.dump(results, f, indent=2)
        saved_artifacts.append(report_path)

    action_result = to_action_result(
        plugin_id="security_scan",
        capability="security",
        status=status,
        decision=decision,
        message=message,
        counts=counts,
        hints=hints,
        artifacts=saved_artifacts,
        signals={
            "security_critical": counts["critical"],
            "vulnerabilities_count": counts["vulnerabilities"],
            "secrets_count": secret_count,
            "security_passed": not blocked,
        },
    )
    print(json.dumps(action_result))
    return 1 if blocked else 0


if __name__ == "__main__":
    sys.exit(main())
