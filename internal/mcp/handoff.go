// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Handoff Package (ASSURANCE Phase C)
//
// actiond_handoff_pack renders the ASSURANCE §2 Handoff Package: an envelope +
// payload aggregated from git log, ActionD verdicts and (optionally) the RMS
// task report. The Gate experiment (docs/ASSURANCE.md §2) feeds an incoming
// agent ONLY the rendered package and measures task completion + onboarding
// time — this tool is the package generator, not the experiment itself.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// HandoffSchemaVersion is the schema tag for packages produced by this tool.
const HandoffSchemaVersion = "assurance-handoff/v1"

// HandoffPackage is the concrete form of the ASSURANCE §2 contract.
type HandoffPackage struct {
	SchemaVersion string          `json:"schema_version"`
	Envelope      HandoffEnvelope `json:"envelope"`
	Payload       HandoffPayload  `json:"payload"`
	Validation    []string        `json:"validation,omitempty"` // 校验问题（warnings）
	Markdown      string          `json:"markdown"`             // Agent B 的唯一输入
}

// HandoffEnvelope carries the routing/identity fields of the contract.
type HandoffEnvelope struct {
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent"`
	Project   string `json:"project"`
	TaskID    string `json:"task_id,omitempty"`
	Revision  string `json:"revision"` // HEAD commit sha
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// HandoffState is a tri-state current-state summary with evidence level (R1).
type HandoffState struct {
	Status        string `json:"status"` // known | partial | unknown
	EvidenceLevel int    `json:"evidence_level"`
	Detail        string `json:"detail"`
}

// HandoffPayload is the content of the contract.
type HandoffPayload struct {
	Task                string           `json:"task"`
	Goal                string           `json:"goal,omitempty"`
	CurrentState        HandoffState     `json:"current_state"`
	CompletedWork       []string         `json:"completed_work"`
	PendingWork         []string         `json:"pending_work,omitempty"`
	Artifacts           []string         `json:"artifacts,omitempty"`
	Decisions           []string         `json:"decisions,omitempty"`
	Constraints         []string         `json:"constraints,omitempty"`
	KnownFailures       []string         `json:"known_failures,omitempty"`
	VerificationState   VerificationInfo `json:"verification_state"`
	ExpectedRevision    string           `json:"expected_revision"`
	SuggestedNextAction string           `json:"suggested_next_action"`
}

// registerHandoffTool registers actiond_handoff_pack.
func registerHandoffTool(s *server.MCPServer, client ActionDClient) {
	s.AddTool(
		mcp.NewTool("actiond_handoff_pack",
			mcp.WithDescription(`生成 Handoff Package（ASSURANCE Phase C）：按 §2 契约聚合 git log + ActionD verdict + RMS task report，
输出结构化 JSON + 可直接投喂给接手 Agent 的 Markdown。

Gate 实验用法：接手 Agent 只读 markdown 字段（禁止读原始会话），
衡量任务完成率与接手上手时间。unknown 显式三态，绝不假装知道。`),
			mcp.WithString("path",
				mcp.Description("仓库路径（默认当前目录）"),
			),
			mcp.WithString("task_id",
				mcp.Description("任务 id；提供后同时查询 RMS task report 补全 goal/decisions"),
			),
			mcp.WithString("from_agent",
				mcp.Description("交接发起方（如 dsh:codex）"),
			),
			mcp.WithString("to_agent",
				mcp.Description("接手方（如 dsh:claude-window）"),
			),
			mcp.WithString("suggested_next_action",
				mcp.Description("建议接手方执行的下一步"),
			),
			mcp.WithString("goal",
				mcp.Description("任务目标一句话"),
			),
			mcp.WithString("pending_work",
				mcp.Description("待办清单（逗号分隔）"),
			),
			mcp.WithNumber("ttl_hours",
				mcp.Description("交接有效期小时（默认 24）"),
			),
			withInteger("ttl_hours"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleHandoffPack(client, ctx, request)
		},
	)
}

// handleHandoffPack builds and returns the package as structured output.
func handleHandoffPack(client ActionDClient, _ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	repoPath := getString(args, "path")
	taskID := getString(args, "task_id")
	fromAgent := getString(args, "from_agent")
	toAgent := getString(args, "to_agent")
	goal := getString(args, "goal")
	nextAction := getString(args, "suggested_next_action")
	pendingWork := getString(args, "pending_work")
	projectID := getString(args, "project_id")
	ttlHours := getInt(args, "ttl_hours")
	if ttlHours <= 0 {
		ttlHours = 24
	}

	pkg, err := buildHandoffPackage(client, repoPath, taskID, fromAgent, toAgent, goal, nextAction, pendingWork, projectID, ttlHours)
	if err != nil {
		return mcp.NewToolResultError("Failed to build handoff package: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(pkg, "", "  ")
	return mcp.NewToolResultStructured(pkg, string(data)), nil
}

// buildHandoffPackage aggregates the §2 fields from real data.
func buildHandoffPackage(client ActionDClient, repoPath, taskID, fromAgent, toAgent, goal, nextAction, pendingWork, projectID string, ttlHours int) (*HandoffPackage, error) {
	if repoPath == "" {
		repoPath = "."
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	repoNameRaw := filepath.Base(absPath)
	repoName := normalizeRepoName(repoNameRaw)
	if projectID == "" {
		projectID = repoName
	}

	// Reuse the Phase A six-question machinery as the evidence backbone.
	changes, err := readGitLog(absPath, "", 5)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	revision := ""
	if len(changes) > 0 {
		revision = changes[0].CommitSHA
	}
	// Verification must align with the CURRENT revision (HEAD), not the
	// repo-wide history — Gate round-1 finding: the package showed
	// "20 passed / 1 failed / 7 unknown" aggregated over all jobs while the
	// fixture repo only had one test file (misleading).
	verdicts, _ := collectVerdicts(client, repoName, repoNameRaw, revision)
	verification := buildVerification(verdicts, "")

	// Completed work = recent commits; known failures = failed verdicts.
	completed := make([]string, 0, len(changes))
	for _, c := range changes {
		completed = append(completed, fmt.Sprintf("%s: %s", shortSHA(c.CommitSHA), c.Message))
	}
	var knownFailures []string
	for _, v := range verdicts {
		if v.Status == "fail" {
			knownFailures = append(knownFailures, fmt.Sprintf("%s (job %s)", v.Plugin, v.JobID))
		}
	}

	// Optional RMS task report enrichment.
	var decisions []string
	if taskID != "" {
		if report, qerr := queryTaskReport(projectID, taskID); qerr == nil {
			if s := firstString(report, "status"); s != "" {
				goal = firstNonEmpty(goal, firstString(report, "summary"))
			}
			decisions = stringSlice(report, "decisions")
		}
	}

	now := time.Now()
	pkg := &HandoffPackage{
		SchemaVersion: HandoffSchemaVersion,
		Envelope: HandoffEnvelope{
			FromAgent: fromAgent,
			ToAgent:   toAgent,
			Project:   projectID,
			TaskID:    taskID,
			Revision:  revision,
			CreatedAt: now.Format(time.RFC3339),
			ExpiresAt: now.Add(time.Duration(ttlHours) * time.Hour).Format(time.RFC3339),
		},
		Payload: HandoffPayload{
			Task:                firstNonEmpty(taskID, "unknown——未提供 task_id（显式 unknown）"),
			Goal:                goal,
			CurrentState:        buildHandoffState(verdicts, changes),
			CompletedWork:       completed,
			PendingWork:         splitList(pendingWork),
			Decisions:           decisions,
			KnownFailures:       knownFailures,
			VerificationState:   verification,
			ExpectedRevision:    revision,
			SuggestedNextAction: nextAction,
		},
	}

	pkg.Validation = validateHandoff(pkg)
	pkg.Markdown = renderHandoffMarkdown(pkg)
	return pkg, nil
}

// buildHandoffState summarizes where the work stands, tri-state + L0-4.
func buildHandoffState(verdicts []VerdictEntry, changes []ChangeEntry) HandoffState {
	if len(changes) == 0 {
		return HandoffState{Status: "unknown", EvidenceLevel: 0, Detail: "L0：git 历史不可读"}
	}
	overall := overallVerdict(verdicts)
	switch overall {
	case "pass":
		return HandoffState{Status: "known", EvidenceLevel: 3, Detail: "L3：有提交历史 + 全量 verdict pass（但未做交接双向校验）"}
	case "fail":
		return HandoffState{Status: "known", EvidenceLevel: 3, Detail: "L3：有提交历史 + 存在 fail verdict（见 known_failures）"}
	default:
		return HandoffState{Status: "partial", EvidenceLevel: 1, Detail: "L1：有提交历史，但 verdict 不完整/未知（unknown 显式保留）"}
	}
}

// validateHandoff checks the contract's required fields, returning warnings
// (never hard errors — a degraded package may still be better than none).
func validateHandoff(pkg *HandoffPackage) []string {
	var warnings []string
	if pkg.Envelope.FromAgent == "" {
		warnings = append(warnings, "from_agent 为空：无法追溯交接发起方")
	}
	if pkg.Envelope.ToAgent == "" {
		warnings = append(warnings, "to_agent 为空：交接无明确接手方")
	}
	if pkg.Envelope.Revision == "" {
		warnings = append(warnings, "revision 为空：无 expected revision（L0）")
	}
	if pkg.Payload.VerificationState.Status == "unknown" {
		warnings = append(warnings, "verification 未知：接手方不应把当前状态当作已验证")
	}
	if pkg.Payload.SuggestedNextAction == "" {
		warnings = append(warnings, "suggested_next_action 为空：接手方需要自行推断下一步（降低上手速度）")
	}
	return warnings
}

// renderHandoffMarkdown renders the package as the ONLY input an incoming
// agent receives in the Gate experiment.
func renderHandoffMarkdown(pkg *HandoffPackage) string {
	p := pkg.Payload
	var b strings.Builder
	b.WriteString("# Handoff Package\n\n")
	fmt.Fprintf(&b, "- **from**: %s\n- **to**: %s\n- **project**: %s\n", pkg.Envelope.FromAgent, pkg.Envelope.ToAgent, pkg.Envelope.Project)
	fmt.Fprintf(&b, "- **task**: %s\n- **revision**: %s\n- **created**: %s\n- **expires**: %s\n",
		p.Task, pkg.Envelope.Revision, pkg.Envelope.CreatedAt, pkg.Envelope.ExpiresAt)
	if p.Goal != "" {
		fmt.Fprintf(&b, "\n## Goal\n%s\n", p.Goal)
	}
	fmt.Fprintf(&b, "\n## Current State\n%s\n", p.CurrentState.Detail)
	if len(p.CompletedWork) > 0 {
		b.WriteString("\n## Completed Work\n")
		for _, item := range p.CompletedWork {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(p.PendingWork) > 0 {
		b.WriteString("\n## Pending Work\n")
		for _, item := range p.PendingWork {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(p.KnownFailures) > 0 {
		b.WriteString("\n## Known Failures\n")
		for _, item := range p.KnownFailures {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(p.Decisions) > 0 {
		b.WriteString("\n## Decisions\n")
		for _, item := range p.Decisions {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	fmt.Fprintf(&b, "\n## Verification State\n%s (%d passed / %d failed / %d unknown)\n",
		p.VerificationState.Status, p.VerificationState.JobsPassed, p.VerificationState.JobsFailed, p.VerificationState.JobsUnknown)
	if p.SuggestedNextAction != "" {
		fmt.Fprintf(&b, "\n## Suggested Next Action\n%s\n", p.SuggestedNextAction)
	}
	if len(pkg.Validation) > 0 {
		b.WriteString("\n## Warnings\n")
		for _, w := range pkg.Validation {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}

// shortSHA truncates a commit sha for display.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// splitList splits a comma-separated list into trimmed items.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
