#!/usr/bin/env python3
"""
observability-export - 可观测性数据导出

导出格式:
- Prometheus (metrics)
- OpenTelemetry (traces/metrics/logs)
- Town Runtime / RMS
- 结构化 JSON

输出:
- metrics.json: 指标数据
- traces.json: 追踪数据
- actiond-otel.json: OTLP 格式
"""

import sys
import json
import os
from pathlib import Path
from datetime import datetime
from typing import Any

def collect_actiond_metrics(input_data: dict) -> dict:
    """收集 ActionD 执行指标"""
    metrics = {
        "timestamp": datetime.now().isoformat(),
        "metrics": []
    }

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    # 基础指标
    base_labels = {
        "repo": Path(repo_path).name if repo_path else "unknown",
        "branch": "unknown",
        "sha": "unknown"
    }

    # 尝试从其他 artifacts 读取指标
    if artifact_dir:
        artifacts_path = Path(artifact_dir)
        if artifacts_path.exists():
            # 收集测试指标
            for test_file in artifacts_path.glob("*test*.json"):
                try:
                    with open(test_file) as f:
                        data = json.load(f)
                        passed = data.get("passed", data.get("stats", {}).get("passed", 0))
                        failed = data.get("failed", data.get("stats", {}).get("failed", 0))
                        total = passed + failed

                        metrics["metrics"].append({
                            "name": "actiond_tests_total",
                            "type": "gauge",
                            "value": total,
                            "labels": {**base_labels, "status": "total"}
                        })
                        metrics["metrics"].append({
                            "name": "actiond_tests_passed",
                            "type": "gauge",
                            "value": passed,
                            "labels": base_labels
                        })
                        metrics["metrics"].append({
                            "name": "actiond_tests_failed",
                            "type": "gauge",
                            "value": failed,
                            "labels": base_labels
                        })
                except:
                    pass

            # 收集覆盖率指标
            for cov_file in artifacts_path.glob("*coverage*.json"):
                try:
                    with open(cov_file) as f:
                        data = json.load(f)
                        coverage = data.get("average_coverage", data.get("line_coverage", 0))

                        metrics["metrics"].append({
                            "name": "actiond_coverage_percent",
                            "type": "gauge",
                            "value": coverage,
                            "labels": base_labels
                        })
                except:
                    pass

            # 收集安全扫描指标
            for sec_file in artifacts_path.glob("*security*.json"):
                try:
                    with open(sec_file) as f:
                        data = json.load(f)
                        summary = data.get("summary", {})

                        metrics["metrics"].append({
                            "name": "actiond_security_vulnerabilities",
                            "type": "gauge",
                            "value": summary.get("total_vulnerabilities", 0),
                            "labels": base_labels
                        })
                        metrics["metrics"].append({
                            "name": "actiond_security_critical",
                            "type": "gauge",
                            "value": summary.get("critical_issues", 0),
                            "labels": base_labels
                        })
                except:
                    pass

    return metrics

def collect_actiond_traces(input_data: dict) -> dict:
    """收集 ActionD 执行追踪"""
    traces = {
        "timestamp": datetime.now().isoformat(),
        "traces": []
    }

    repo_path = input_data.get("repo_path")
    artifact_dir = input_data.get("artifact_dir")

    # 创建一个 trace span 代表整个 ActionD 执行
    main_span = {
        "trace_id": os.urandom(16).hex(),
        "span_id": os.urandom(8).hex(),
        "operation": "actiond.pipeline",
        "start_time": datetime.now().isoformat(),
        "duration_ms": 0,
        "attributes": {
            "repo": Path(repo_path).name if repo_path else "unknown"
        },
        "events": []
    }

    traces["traces"].append(main_span)

    # 从 artifacts 收集插件执行信息
    if artifact_dir:
        for result_file in Path(artifact_dir).glob("*.json"):
            try:
                with open(result_file) as f:
                    data = json.load(f)

                # 为每个插件创建 span
                plugin_span = {
                    "trace_id": main_span["trace_id"],
                    "span_id": os.urandom(8).hex(),
                    "parent_span_id": main_span["span_id"],
                    "operation": f"actiond.plugin.{result_file.stem}",
                    "start_time": data.get("timestamp", datetime.now().isoformat()),
                    "duration_ms": data.get("duration_ms", 0),
                    "attributes": {
                        "status": data.get("status", "unknown"),
                        "artifacts_count": len(data.get("artifacts", []))
                    },
                    "events": []
                }

                if data.get("status") == "error":
                    plugin_span["events"].append({
                        "name": "exception",
                        "timestamp": datetime.now().isoformat(),
                        "attributes": {
                            "exception.message": data.get("error", "unknown error")
                        }
                    })

                traces["traces"].append(plugin_span)

            except:
                pass

    return traces

def to_prometheus_format(metrics: dict) -> str:
    """转换为 Prometheus 格式"""
    lines = []
    seen_metrics = set()

    for m in metrics.get("metrics", []):
        name = m["name"]
        value = m["value"]
        labels = m.get("labels", {})

        # 添加 HELP 和 TYPE (只一次)
        if name not in seen_metrics:
            metric_type = m.get("type", "gauge")
            lines.append(f"# HELP {name} ActionD metric")
            lines.append(f"# TYPE {name} {metric_type}")
            seen_metrics.add(name)

        # 格式化 labels
        label_str = ""
        if labels:
            label_parts = [f'{k}="{v}"' for k, v in labels.items()]
            label_str = "{" + ", ".join(label_parts) + "}"

        lines.append(f"{name}{label_str} {value}")

    return '\n'.join(lines)

def to_otel_format(metrics: dict, traces: dict) -> dict:
    """转换为 OpenTelemetry 格式"""
    otel = {
        "resourceSpans": [{
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "actiond"}},
                    {"key": "service.version", "value": {"stringValue": "1.0.0"}}
                ]
            },
            "scopeSpans": [{
                "scope": {"name": "actiond.plugins"},
                "spans": []
            }]
        }],
        "resourceMetrics": [{
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "actiond"}}
                ]
            },
            "scopeMetrics": [{
                "scope": {"name": "actiond.metrics"},
                "metrics": []
            }]
        }]
    }

    # 转换 traces
    for trace in traces.get("traces", []):
        span = {
            "traceId": trace.get("trace_id", ""),
            "spanId": trace.get("span_id", ""),
            "name": trace.get("operation", "unknown"),
            "kind": 1,  # INTERNAL
            "startTimeUnixNano": int(datetime.now().timestamp() * 1e9),
            "endTimeUnixNano": int(datetime.now().timestamp() * 1e9),
            "attributes": []
        }

        if "parent_span_id" in trace:
            span["parentSpanId"] = trace["parent_span_id"]

        for k, v in trace.get("attributes", {}).items():
            span["attributes"].append({
                "key": k,
                "value": {"stringValue": str(v)}
            })

        otel["resourceSpans"][0]["scopeSpans"][0]["spans"].append(span)

    # 转换 metrics
    for m in metrics.get("metrics", []):
        metric = {
            "name": m["name"],
            "unit": "1",
            "gauge": {
                "dataPoints": [{
                    "asDouble": float(m["value"]),
                    "timeUnixNano": int(datetime.now().timestamp() * 1e9),
                    "attributes": []
                }]
            }
        }

        for k, v in m.get("labels", {}).items():
            metric["gauge"]["dataPoints"][0]["attributes"].append({
                "key": k,
                "value": {"stringValue": str(v)}
            })

        otel["resourceMetrics"][0]["scopeMetrics"][0]["metrics"].append(metric)

    return otel

def to_town_runtime_format(metrics: dict, traces: dict, input_data: dict) -> dict:
    """转换为 Town Runtime / RMS 格式"""
    town = {
        "action_id": f"actiond-{datetime.now().strftime('%Y%m%d%H%M%S')}",
        "timestamp": datetime.now().isoformat(),
        "source": "actiond",
        "action_type": "ci_pipeline",
        "context": {
            "repo": input_data.get("repo_path", "unknown"),
            "run_id": os.urandom(8).hex()
        },
        "metrics": {},
        "traces": [],
        "memory_writable": True  # 可写入 RMS
    }

    # 压缩 metrics
    for m in metrics.get("metrics", []):
        name = m["name"]
        if name not in town["metrics"]:
            town["metrics"][name] = []
        town["metrics"][name].append({
            "value": m["value"],
            "labels": m.get("labels", {}),
            "timestamp": m.get("timestamp", datetime.now().isoformat())
        })

    # 简化 traces
    for t in traces.get("traces", []):
        town["traces"].append({
            "operation": t.get("operation", "unknown"),
            "duration_ms": t.get("duration_ms", 0),
            "status": t.get("attributes", {}).get("status", "unknown")
        })

    return town

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

    # 收集数据
    metrics = collect_actiond_metrics(input_data)
    traces = collect_actiond_traces(input_data)

    # 生成各格式
    prometheus_output = to_prometheus_format(metrics)
    otel_output = to_otel_format(metrics, traces)
    town_output = to_town_runtime_format(metrics, traces, input_data)

    result = {
        "status": "success",
        "timestamp": datetime.now().isoformat(),
        "metrics_count": len(metrics.get("metrics", [])),
        "traces_count": len(traces.get("traces", [])),
        "formats": ["json", "prometheus", "otel", "town_runtime"]
    }

    # 保存 artifacts
    saved_artifacts = []
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)

        # JSON metrics
        metrics_path = os.path.join(artifact_dir, "metrics.json")
        with open(metrics_path, "w") as f:
            json.dump(metrics, f, indent=2)
        saved_artifacts.append("metrics.json")

        # JSON traces
        traces_path = os.path.join(artifact_dir, "traces.json")
        with open(traces_path, "w") as f:
            json.dump(traces, f, indent=2)
        saved_artifacts.append("traces.json")

        # Prometheus
        prom_path = os.path.join(artifact_dir, "metrics.prom")
        with open(prom_path, "w") as f:
            f.write(prometheus_output)
        saved_artifacts.append("metrics.prom")

        # OpenTelemetry
        otel_path = os.path.join(artifact_dir, "actiond-otel.json")
        with open(otel_path, "w") as f:
            json.dump(otel_output, f, indent=2)
        saved_artifacts.append("actiond-otel.json")

        # Town Runtime
        town_path = os.path.join(artifact_dir, "actiond-town.json")
        with open(town_path, "w") as f:
            json.dump(town_output, f, indent=2)
        saved_artifacts.append("actiond-town.json")

    result["artifacts"] = saved_artifacts
    print(json.dumps(result))

if __name__ == "__main__":
    main()
