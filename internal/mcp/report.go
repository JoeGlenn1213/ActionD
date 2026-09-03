// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Goal Run Report (ASSURANCE Phase A skeleton)
//
// Zero-new-storage projection: aggregates git log + ActionD jobs + RMS task
// report into a six-question report. Honesty rules from docs/ASSURANCE.md:
//   - unknown is an explicit first-class value (never defaulted to pass/fail)
//   - evidence levels L0-L4 (R1) are printed, not booleans
//   - coverage is dual-number (native/reconstructed) with the R2 anti-gaming rule
//   - unimplemented guarantees are listed in Limitations (anti-self-deception)

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Report model -----------------------------------------------------------

// RunReport is the Phase A Goal Run Report. All verdicts are tri-state:
// "pass" / "fail" / "unknown".
type RunReport struct {
	Repo         string `json:"repo"`
	GeneratedAt  string `json:"generated_at"`
	CommitFilter string `json:"commit_filter,omitempty"`
	TaskID       string `json:"task_id,omitempty"`

	WhatChanged  []ChangeEntry    `json:"what_changed"`
	Intent       IntentInfo       `json:"intent"`
	Verification VerificationInfo `json:"verification"`
	Trust        TrustInfo        `json:"trust"`
	Handoff      HandoffInfo      `json:"handoff"`
	Recovery     RecoveryInfo     `json:"recovery"`
	Coverage     CoverageInfo     `json:"coverage"`

	// Limitations lists what the assurance layer does NOT yet guarantee.
	// Anti-self-deception: the report admits what it cannot know.
	Limitations []string `json:"limitations"`
}

// ChangeEntry is one workspace mutation (currently always reconstructed from
// git history, because the native mutation contract is not implemented yet).
type ChangeEntry struct {
	CommitSHA string `json:"commit_sha"`
	Author    string `json:"author"`
	Message   string `json:"message"`
	Source    string `json:"source"`           // native | reconstructed
	Intent    string `json:"intent,omitempty"` // task id when known; omitted = unknown
}

// IntentInfo answers "为什么做" with an evidence level, never a bare boolean.
type IntentInfo struct {
	Status        string `json:"status"` // known | unknown
	TaskID        string `json:"task_id,omitempty"`
	EvidenceLevel int    `json:"evidence_level"` // L0-4 (R1)
	Detail        string `json:"detail"`
}

// VerificationInfo answers "做对了吗" over all collected jobs.
type VerificationInfo struct {
	Status      string         `json:"status"` // pass | fail | unknown
	JobsTotal   int            `json:"jobs_total"`
	JobsPassed  int            `json:"jobs_passed"`
	JobsFailed  int            `json:"jobs_failed"`
	JobsUnknown int            `json:"jobs_unknown"`
	Detail      string         `json:"detail,omitempty"`
	Verdicts    []VerdictEntry `json:"verdicts,omitempty"`
}

// VerdictEntry is one job mapped to the tri-state verdict vocabulary,
// with ASSURANCE Phase B tier + verifier provenance.
type VerdictEntry struct {
	JobID            string `json:"job_id"`
	Plugin           string `json:"plugin"`
	CommitSHA        string `json:"commit_sha,omitempty"` // "" = unknown (not fetched)
	Status           string `json:"status"`               // pass | fail | unknown
	DurationMs       int64  `json:"duration_ms,omitempty"`
	Tier             string `json:"tier"`                  // FAST | FULL | unknown（TARGETED 未实现）
	VerifierID       string `json:"verifier_id,omitempty"` // plugin name
	Intent           string `json:"intent,omitempty"`      // dispatch-recorded task id (native v1)
	VerifierVersion  string `json:"verifier_version,omitempty"`
	PromotionAllowed bool   `json:"promotion_allowed"` // true only for FULL pass（晋升闸门）
}

// TrustInfo answers "结果可信么" with the Phase B tier machinery.
type TrustInfo struct {
	MaxTier            string `json:"max_tier"`            // 最高档位：FULL > FAST > unknown
	TierImplemented    bool   `json:"tier_implemented"`    // 按 profile 粗粒度映射
	VerifierProvenance bool   `json:"verifier_provenance"` // 至少一条 verdict 有插件版本
	HasFullPass        bool   `json:"has_full_pass"`       // 存在 FULL pass（可视为充分验证）
	Confidence         string `json:"confidence"`          // "unknown" unless diagnose ran
	Detail             string `json:"detail"`
}

// HandoffInfo answers "下一位能继续吗" via the RMS task report (optional).
type HandoffInfo struct {
	Status       string   `json:"status"` // found | not_found | unknown
	TaskID       string   `json:"task_id,omitempty"`
	ReportStatus string   `json:"report_status,omitempty"`
	NextActions  []string `json:"next_actions,omitempty"`
	Detail       string   `json:"detail,omitempty"`
}

// RecoveryInfo answers "退得回吗" with the R1 evidence level.
type RecoveryInfo struct {
	EvidenceLevel  int      `json:"evidence_level"` // L0-4
	LevelDetail    string   `json:"level_detail"`
	RecoveryPoints []string `json:"recovery_points,omitempty"` // commit shas (L2 candidates)
}

// CoverageInfo is the dual-number assurance coverage (R2 anti-gaming).
type CoverageInfo struct {
	MutationsTotal         int     `json:"mutations_total"`
	NativeMutations        int     `json:"native_mutations"`
	ReconstructedMutations int     `json:"reconstructed_mutations"`
	NativeCoverage         float64 `json:"native_coverage"`       // 0..1
	VerificationCoverage   float64 `json:"verification_coverage"` // verified mutations / total
	UnverifiedMutations    int     `json:"unverified_mutations"`
}

// --- Tool registration ------------------------------------------------------

// registerRunReportTool registers the actiond_run_report MCP tool.
func registerRunReportTool(s *server.MCPServer, client ActionDClient) {
	s.AddTool(
		mcp.NewTool("actiond_run_report",
			mcp.WithDescription(`生成 Goal Run Report（ASSURANCE Phase A 骨架，零新存储投影）。

聚合 git log + ActionD 任务 + RMS task report，回答六问：
1. 做了什么（git 变更，标注 native/reconstructed）
2. 为什么做（意图/task，unknown 显式）
3. 做对了吗（verdict 三态：pass/fail/unknown）
4. 结果可信么（tier/verifier 出处——Phase B 前诚实标注未落地）
5. 下一位能继续吗（RMS task report 交接状态）
6. 退得回吗（rollback 证据等级 L0-4 + 可回滚点）

报告自带 Limitations 清单，显式列出 assurance 层尚不能保证什么（防自欺原则）。`),
			mcp.WithString("path",
				mcp.Description("仓库路径（默认当前目录）"),
			),
			mcp.WithString("commit",
				mcp.Description("聚焦某个 commit（缺省为最近 N 个提交）"),
			),
			mcp.WithString("task_id",
				mcp.Description("RMS task id；提供后报告会查询任务报告与交接状态"),
			),
			mcp.WithString("project_id",
				mcp.Description("RMS project id（默认取仓库名）"),
			),
			mcp.WithNumber("limit",
				mcp.Description("git log 条数（默认 10）"),
			),
			withInteger("limit"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleRunReport(client, ctx, request)
		},
	)
}

// handleRunReport builds the report and returns it as structured output.
func handleRunReport(client ActionDClient, _ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	repoPath := getString(args, "path")
	commit := getString(args, "commit")
	taskID := getString(args, "task_id")
	projectID := getString(args, "project_id")
	limit := getInt(args, "limit")
	if limit <= 0 {
		limit = 10
	}

	report, err := buildRunReport(client, repoPath, commit, taskID, projectID, limit)
	if err != nil {
		return mcp.NewToolResultError("Failed to build run report: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	return mcp.NewToolResultStructured(report, string(data)), nil
}

// --- Builder ----------------------------------------------------------------

// buildRunReport assembles the report. Git failures are fatal (no repo = no
// report); ActionD/RMS unavailability degrades to explicit unknown instead.
func buildRunReport(client ActionDClient, repoPath, commit, taskID, projectID string, limit int) (*RunReport, error) {
	if repoPath == "" {
		repoPath = "."
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	// repoNameRaw keeps the original case for event-log matching, which is
	// case-sensitive against lgh event records ("ActionD" ≠ "actiond").
	repoNameRaw := filepath.Base(absPath)
	repoName := normalizeRepoName(repoNameRaw)
	if projectID == "" {
		projectID = repoName
	}

	changes, err := readGitLog(absPath, commit, limit)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	// Commit-level intent from message convention task:<id> / [task:<id>].
	intentTaskID := taskID
	if intentTaskID == "" {
		intentTaskID = intentFromChanges(changes)
	}

	verdicts, verificationDetail := collectVerdicts(client, repoName, repoNameRaw, commit)

	report := &RunReport{
		Repo:         repoName,
		GeneratedAt:  time.Now().Format(time.RFC3339),
		CommitFilter: commit,
		TaskID:       intentTaskID,
		WhatChanged:  changes,
		Intent:       buildIntent(intentTaskID, taskID != ""),
		Verification: buildVerification(verdicts, verificationDetail),
		Trust:        buildTrust(verdicts),
		Handoff:      buildHandoff(projectID, intentTaskID),
		Recovery:     buildRecovery(changes),
		Coverage:     buildCoverage(changes, verdicts),
		Limitations:  buildLimitations(),
	}

	return report, nil
}

// --- Git ---------------------------------------------------------------

// readGitLog reads commit history for the given path. Every entry is
// reconstructed by definition in Phase A (the native contract is not landed).
func readGitLog(repoPath, commit string, limit int) ([]ChangeEntry, error) {
	var args []string
	if commit != "" {
		args = []string{"-C", repoPath, "log", "-n", "1", "--format=%H|%an|%s", commit}
	} else {
		args = []string{"-C", repoPath, "log", "-n", fmt.Sprintf("%d", limit), "--format=%H|%an|%s"}
	}

	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	var changes []ChangeEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		entry := ChangeEntry{
			CommitSHA: parts[0],
			Author:    parts[1],
			Message:   parts[2],
			Source:    "reconstructed",
		}
		entry.Intent = job.IntentFromMessage(parts[2])
		changes = append(changes, entry)
	}
	return changes, nil
}

// intentFromChanges returns the first non-empty intent declared in history.
func intentFromChanges(changes []ChangeEntry) string {
	for _, c := range changes {
		if c.Intent != "" {
			return c.Intent
		}
	}
	return ""
}

// --- ActionD jobs ------------------------------------------------------

// maxDetailFetches caps per-job detail calls in the no-event fallback path.
const maxDetailFetches = 30

// collectVerdicts maps ActionD jobs to tri-state verdicts for the repo
// (and commit, when given). The event-log path is preferred for a focused
// commit; otherwise recent actions are filtered by repo name. rawRepo keeps
// the original case because findEventIDFromLog matches case-sensitively.
func collectVerdicts(client ActionDClient, repoName, rawRepo, commit string) ([]VerdictEntry, string) {
	var infos []ActionInfo
	var err error
	detail := ""

	if commit != "" {
		if eventID, evErr := findEventIDFromLog(commit, rawRepo); evErr == nil && eventID != "" {
			infos, err = client.GetActionsByEventID(eventID)
			if err == nil {
				return verdictsFromInfos(client, infos, repoName, commit, ""), ""
			}
			detail = fmt.Sprintf("event log lookup failed, fallback to recent actions: %v", err)
		} else if evErr != nil {
			detail = fmt.Sprintf("event log unreadable, fallback to recent actions: %v", evErr)
		}
	}

	if infos == nil {
		infos, err = client.GetActions(200)
		if err != nil {
			return nil, fmt.Sprintf("actiond 不可用，verification 显式 unknown: %v", err)
		}
	}

	verdicts := verdictsFromInfos(client, infos, repoName, commit, detail)
	return verdicts, detail
}

// verdictsFromInfos filters infos by repo, maps statuses to verdicts, and
// resolves commit shas + tier/verifier provenance via per-job details
// (capped). A focused-commit report pins every entry to the filter commit.
func verdictsFromInfos(client ActionDClient, infos []ActionInfo, repoName, commit, detail string) []VerdictEntry {
	var matched []ActionInfo
	for _, info := range infos {
		if normalizeRepoName(info.Repo) != repoName {
			continue
		}
		matched = append(matched, info)
	}
	// Stable ordering: most recent first.
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].CreatedAt > matched[j].CreatedAt
	})

	var verdicts []VerdictEntry
	fetched := 0
	for _, info := range matched {
		entry := VerdictEntry{
			JobID:      info.ID,
			Plugin:     info.PluginName,
			Status:     statusToVerdict(info.Status),
			DurationMs: info.DurationMs,
			Tier:       "unknown",
		}
		if fetched < maxDetailFetches {
			fetched++
			if d, derr := client.GetAction(info.ID); derr == nil && d != nil {
				if hash, ok := d.Commit["hash"].(string); ok {
					entry.CommitSHA = hash
				}
				entry.Tier = profileToTier(d.Profile)
				entry.VerifierID = d.PluginName
				entry.VerifierVersion = d.PluginVersion
				entry.Intent = d.Intent
				entry.Intent = d.Intent
			}
		}
		if commit != "" && entry.CommitSHA == "" {
			// Fallback only: prefer the full sha from job details so
			// coverage matching against git log (40-char hashes) works even
			// when the caller passed a short sha.
			entry.CommitSHA = commit
		}
		entry.PromotionAllowed = entry.Status == "pass" && entry.Tier == "FULL"
		verdicts = append(verdicts, entry)
	}
	return verdicts
}

// profileToTier maps the execution profile onto the ASSURANCE verdict tiers.
// TARGETED (diff-aware) is not implemented yet, so nothing produces it.
func profileToTier(profile string) string {
	switch strings.ToLower(profile) {
	case "fast":
		return "FAST"
	case "full", "release", "nightly":
		return "FULL"
	default:
		return "unknown"
	}
}

// statusToVerdict maps ActionD job statuses onto the tri-state verdict
// vocabulary. Only "done" is pass and only "failed" is fail; everything else
// (pending/queued/running/cancelled/blocked/needs_approval/retrying/retryable)
// produced no verdict and must be reported as unknown.
func statusToVerdict(status string) string {
	switch status {
	case "done":
		return "pass"
	case "failed":
		return "fail"
	default:
		return "unknown"
	}
}

// overallVerdict computes the aggregate tri-state: any failure is fail; a
// non-empty set with every job passed is pass; otherwise unknown (including
// "no jobs at all" — silence is not a pass).
func overallVerdict(verdicts []VerdictEntry) string {
	if len(verdicts) == 0 {
		return "unknown"
	}
	passed, failed := 0, 0
	for _, v := range verdicts {
		switch v.Status {
		case "pass":
			passed++
		case "fail":
			failed++
		}
	}
	if failed > 0 {
		return "fail"
	}
	if passed == len(verdicts) {
		return "pass"
	}
	return "unknown"
}

// --- Six-question sections ---------------------------------------------

// buildIntent answers "为什么做" with R1 evidence levels.
func buildIntent(taskID string, callerProvided bool) IntentInfo {
	if taskID == "" {
		return IntentInfo{
			Status:        "unknown",
			EvidenceLevel: 0,
			Detail:        "L0：未发现任务归属（调用方未提供 task_id，commit message 也无 task:/goal: 标注）",
		}
	}
	src := "commit message 标注"
	if callerProvided {
		src = "调用方提供"
	}
	return IntentInfo{
		Status:        "known",
		TaskID:        taskID,
		EvidenceLevel: 2,
		Detail:        fmt.Sprintf("L2：有具体 task id（%s），但未做 run↔task 双向校验（L3 需双方记录互证）", src),
	}
}

// buildVerification answers "做对了吗".
func buildVerification(verdicts []VerdictEntry, detail string) VerificationInfo {
	v := VerificationInfo{
		Status:   overallVerdict(verdicts),
		Detail:   detail,
		Verdicts: verdicts,
	}
	for _, entry := range verdicts {
		v.JobsTotal++
		switch entry.Status {
		case "pass":
			v.JobsPassed++
		case "fail":
			v.JobsFailed++
		default:
			v.JobsUnknown++
		}
	}
	return v
}

// buildTrust answers "结果可信么" with the Phase B tier machinery.
// Tier mapping is coarse (profile-based); TARGETED is not implemented;
// verifier provenance comes from the plugin manifest version.
func buildTrust(verdicts []VerdictEntry) TrustInfo {
	t := TrustInfo{
		TierImplemented: true,
		MaxTier:         "unknown",
		Confidence:      "unknown",
	}
	for _, v := range verdicts {
		if rankTier(v.Tier) > rankTier(t.MaxTier) {
			t.MaxTier = v.Tier
		}
		if v.VerifierVersion != "" {
			t.VerifierProvenance = true
		}
		if v.Status == "pass" && v.Tier == "FULL" {
			t.HasFullPass = true
		}
	}
	t.Detail = "tier 按 profile 粗粒度映射（fast→FAST，full/release→FULL）；TARGETED（diff-aware）未实现；verifier 出处来自插件 manifest 版本"
	if !t.VerifierProvenance {
		t.Detail += "；当前任务无 verifier 版本记录（插件版本缺失）"
	}
	if t.HasFullPass {
		t.Detail += "；存在 FULL pass，可视为充分验证"
	} else {
		t.Detail += "；无 FULL pass，所有 pass 不得视为充分验证"
	}
	t.Detail += "；confidence 需另行跑 actiond_diagnose"
	return t
}

// rankTier orders tiers for max-tier aggregation (FULL > FAST > unknown).
func rankTier(tier string) int {
	switch tier {
	case "FULL":
		return 3
	case "FAST":
		return 2
	case "TARGETED":
		return 1
	default:
		return 0
	}
}

// buildHandoff answers "下一位能继续吗" via the RMS task report.
// RMS 未配置/不可达时显式 unknown，绝不伪装成"无交接"。
func buildHandoff(projectID, taskID string) HandoffInfo {
	if taskID == "" {
		return HandoffInfo{
			Status: "unknown",
			Detail: "L0：无 task_id，未查询交接（提供 task_id 参数后可查 RMS task report）",
		}
	}
	report, err := queryTaskReport(projectID, taskID)
	if err != nil {
		return HandoffInfo{
			Status: "unknown",
			TaskID: taskID,
			Detail: "RMS 查询失败，handoff 显式 unknown: " + err.Error(),
		}
	}
	status := firstString(report, "status")
	if status == "" {
		return HandoffInfo{Status: "not_found", TaskID: taskID, Detail: "RMS 无此 task 的报告"}
	}
	return HandoffInfo{
		Status:       "found",
		TaskID:       taskID,
		ReportStatus: status,
		NextActions:  stringSlice(report, "next_actions"),
	}
}

// buildRecovery answers "退得回吗" with R1 levels. Phase A has commit shas
// but no rollback drill records anywhere, so the honest level is L1.
func buildRecovery(changes []ChangeEntry) RecoveryInfo {
	var points []string
	for _, c := range changes {
		if c.CommitSHA != "" && len(points) < 3 {
			points = append(points, c.CommitSHA)
		}
	}
	if len(points) == 0 {
		return RecoveryInfo{
			EvidenceLevel: 0,
			LevelDetail:   "L0：无任何可回滚点记录（git 历史不可读？）",
		}
	}
	return RecoveryInfo{
		EvidenceLevel:  1,
		LevelDetail:    "L1：有具体 revision（git commit），但未执行过 rollback 演练（L3 需要演练记录，L4 需要演练结果被独立验证）",
		RecoveryPoints: points,
	}
}

// buildCoverage applies the R2 anti-gaming rule: native mutations only count
// when recorded through the mutation contract with a known intent; every git
// change here is reconstructed, so native_coverage is honestly 0 in Phase A.
func buildCoverage(changes []ChangeEntry, verdicts []VerdictEntry) CoverageInfo {
	total := len(changes)
	verifiedShas := make(map[string]bool)
	nativeShas := make(map[string]bool)
	for _, v := range verdicts {
		if v.CommitSHA != "" && (v.Status == "pass" || v.Status == "fail") {
			verifiedShas[v.CommitSHA] = true
		}
		if v.CommitSHA != "" && v.Intent != "" {
			nativeShas[v.CommitSHA] = true // dispatch-recorded intent (native v1)
		}
	}
	verified, native := 0, 0
	for _, c := range changes {
		if verifiedShas[c.CommitSHA] {
			verified++
		}
		if nativeShas[c.CommitSHA] {
			native++
		}
	}
	c := CoverageInfo{
		MutationsTotal:         total,
		NativeMutations:        native,
		ReconstructedMutations: total - native,
		UnverifiedMutations:    total - verified,
	}
	if total > 0 {
		c.NativeCoverage = float64(native) / float64(total)
		c.VerificationCoverage = float64(verified) / float64(total)
	}
	return c
}

// buildLimitations lists what the assurance layer does NOT yet guarantee.
func buildLimitations() []string {
	var limits = []string{
		"native 契约 v1：意图在派发时从 commit message（task:/goal: 标注）记录；未标注的 mutation 仍为 reconstructed",
		"TARGETED 档未实现（diff-aware 验证未落地）；tier 仅按 profile 粗粒度映射",
		"canary 元验证的自动降级（fail-closed → 仅 FULL）尚未接入 profile 执行层：canary 失败目前只体现在 CI 红色与报告 verdict",
		"历史 verdict 按 verifier 出处批量失效的通道未落地（RMS invalidates 对接待做）",
		"lgh 事件未持久化（WAL 未落地）：reconstructed 底账可能不完整",
		"rollback 无演练记录：recovery_evidence_level=L1（L3 需演练）",
	}
	if os.Getenv("RMS_API_KEY") == "" {
		limits = append(limits, "RMS_API_KEY 未设置：handoff 查询降级为显式 unknown")
	}
	return limits
}

// --- RMS task report (optional integration) -------------------------------

// defaultRMSURL is the RMS gray HTTP endpoint (overridable via RMS_URL).
const defaultRMSURL = "http://127.0.0.1:9100"

// queryTaskReport fetches the RMS task report for the given task. Requires
// RMS_API_KEY (X-API-Key header); a missing key is a hard error so callers
// degrade to explicit unknown instead of a fabricated "no handoff".
func queryTaskReport(projectID, taskID string) (map[string]any, error) {
	key := os.Getenv("RMS_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("RMS_API_KEY 未设置")
	}
	base := os.Getenv("RMS_URL")
	if base == "" {
		base = defaultRMSURL
	}

	payload := map[string]any{
		"project_id": projectID,
		"actor":      "app:actiond",
		"task_id":    taskID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, base+"/memory/task/report/query", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RMS 不可达: %w", err)
	}
	defer closeBody(resp.Body, "rms task report")

	if resp.StatusCode == http.StatusNotFound {
		return map[string]any{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RMS 返回 %d", resp.StatusCode)
	}

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode RMS response: %w", err)
	}
	return decoded, nil
}

// firstString digs a string field out of a task report response, tolerating
// the common {report: {...}} / {task_report: {...}} wrapper shapes.
func firstString(m map[string]any, key string) string {
	for _, wrapper := range []string{"", "report", "task_report", "data"} {
		var node map[string]any
		if wrapper == "" {
			node = m
		} else if inner, ok := m[wrapper].(map[string]any); ok {
			node = inner
		} else {
			continue
		}
		if v, ok := node[key].(string); ok {
			return v
		}
	}
	return ""
}

// stringSlice digs a []string field out of a task report response.
func stringSlice(m map[string]any, key string) []string {
	for _, wrapper := range []string{"", "report", "task_report", "data"} {
		var node map[string]any
		if wrapper == "" {
			node = m
		} else if inner, ok := m[wrapper].(map[string]any); ok {
			node = inner
		} else {
			continue
		}
		if raw, ok := node[key].([]any); ok {
			out := make([]string, 0, len(raw))
			for _, item := range raw {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// normalizeRepoName strips the .git suffix and lowercases for loose matching
// between git paths ("ActionD") and ActionD job records ("ActionD.git").
func normalizeRepoName(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ".git"))
}
