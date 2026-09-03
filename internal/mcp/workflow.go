// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Dev Cycle Workflow

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// LGHClient interface for LGH operations
type LGHClient interface {
	Up(ctx context.Context, path, message string) (string, error)
	Save(ctx context.Context, path, message string) (string, error)
	Rollback(ctx context.Context, path string) (string, error)
}

// LocalLGHClient implements LGHClient using local binary
type LocalLGHClient struct {
	binaryPath string
}

// NewLocalLGHClient creates a new LGH client.
// Binary resolution order: $LGH_BINARY → "lgh" on PATH (used when ActionD MCP
// runs standalone). When LGH is unavailable on PATH, callers should set
// LGH_BINARY explicitly to avoid silent dev_cycle_run failures.
func NewLocalLGHClient() *LocalLGHClient {
	binaryPath := "lgh"
	if env := os.Getenv("LGH_BINARY"); env != "" {
		binaryPath = env
	}
	return &LocalLGHClient{
		binaryPath: binaryPath,
	}
}

// Up runs lgh up command
func (c *LocalLGHClient) Up(ctx context.Context, path, message string) (string, error) {
	args := []string{"up", message}
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	if path != "" {
		cmd.Dir = path
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Save runs lgh save command
func (c *LocalLGHClient) Save(ctx context.Context, path, message string) (string, error) {
	args := []string{"save", message}
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	if path != "" {
		cmd.Dir = path
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Rollback runs git reset to previous commit
func (c *LocalLGHClient) Rollback(ctx context.Context, path string) (string, error) {
	// Get previous commit
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD~1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get previous commit: %w", err)
	}
	prevCommit := strings.TrimSpace(string(out))

	// Reset to previous commit
	cmd = exec.CommandContext(ctx, "git", "-C", path, "reset", "--hard", prevCommit)
	out, err = cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// DevCycleResult represents the result of dev_cycle_run
type DevCycleResult struct {
	Success      bool          `json:"success"`
	Commit       string        `json:"commit"`
	Jobs         []JobSummary  `json:"jobs"`
	Summary      string        `json:"summary"`
	Artifacts    []string      `json:"artifacts,omitempty"`
	Rollback     *RollbackInfo `json:"rollback,omitempty"`
	Duration     string        `json:"duration"`
	Error        string        `json:"error,omitempty"`
	ResolvedPath string        `json:"resolved_path,omitempty"`
	PathSource   string        `json:"path_source,omitempty"`
}

// JobSummary represents a job result summary
type JobSummary struct {
	ID       string `json:"id"`
	Plugin   string `json:"plugin"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
}

// RollbackInfo represents rollback information
type RollbackInfo struct {
	FromCommit string `json:"from_commit"`
	ToCommit   string `json:"to_commit"`
	Message    string `json:"message"`
}

// registerWorkflowTools registers workflow-related MCP tools
func registerWorkflowTools(s *server.MCPServer, client ActionDClient, lghClient LGHClient) {
	// dev_cycle_run - End-to-end development cycle
	s.AddTool(
		mcp.NewTool("dev_cycle_run",
			mcp.WithDescription(`端到端开发循环：提交代码 → 触发CI → 等待结果 → 返回汇总

这是一个聚合工具，内部自动完成：
1. lgh up: 提交并推送代码到 LGH
2. 等待 ActionD 触发的 CI/CD 任务完成
3. 收集所有任务结果并返回结构化输出

适用场景：AI 修改代码后，一键完成提交、测试、验证的完整流程。`),
			mcp.WithString("path",
				mcp.Description("仓库路径（默认当前目录）"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("提交信息"),
			),
			mcp.WithNumber("timeout",
				mcp.Description("等待超时秒数（默认 300 = 5分钟）"),
			),
			withInteger("timeout"),
			mcp.WithBoolean("auto_rollback",
				mcp.Description("失败时自动回滚到上一个 commit（默认 false）"),
			),
			mcp.WithString("profile",
				mcp.Description(`执行 profile：fast/full/release（默认不切换，保持当前设置）
- fast: 最小 CI，只跑核心 lint 和 test
- full: 完整 CI，加上安全扫描、覆盖率等
- release: 完整 CI/CD，加上 build 和 deploy`),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleDevCycleRun(client, lghClient, ctx, request)
		},
	)

	// actiond_job_wait - Wait for job completion
	s.AddTool(
		mcp.NewTool("actiond_job_wait",
			mcp.WithDescription("等待指定的 CI/CD 任务完成，返回最终状态。建议在 lgh_up 返回 triggered_job_ids 后立即调用。"),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("任务 ID"),
			),
			mcp.WithNumber("timeout",
				mcp.Description("超时秒数（默认 300）"),
			),
			withInteger("timeout"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleJobWait(client, ctx, request)
		},
	)

	// actiond_cancel - Cancel a running job
	s.AddTool(
		mcp.NewTool("actiond_cancel",
			mcp.WithDescription("取消正在运行的 CI/CD 任务（Deprecated：推荐使用 actiond_job_cancel，它会先校验 job 状态并拒绝终态任务）"),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("任务 ID"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleCancel(client, ctx, request)
		},
	)
}

func handleDevCycleRun(client ActionDClient, lghClient LGHClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)

	path := getString(args, "path")
	message := getString(args, "message")
	if message == "" {
		return mcp.NewToolResultError("Missing required parameter: message"), nil
	}

	timeout := getInt(args, "timeout")
	if timeout == 0 {
		timeout = 300 // 5 minutes
	}
	autoRollback := getBool(args, "auto_rollback")
	profile := getString(args, "profile")

	// Handle profile switching
	var originalProfile string
	if profile != "" {
		// Validate profile
		validProfiles := map[string]bool{"fast": true, "full": true, "release": true}
		if !validProfiles[profile] {
			return mcp.NewToolResultError("Invalid profile. Must be one of: fast, full, release"), nil
		}
		// Save current profile
		originalProfile, _ = client.GetProfile()
		// Switch to requested profile
		if err := client.SetProfile(profile); err != nil {
			return mcp.NewToolResultError("Failed to set profile: " + err.Error()), nil
		}
		// Ensure we restore the profile when done
		defer func() {
			if originalProfile != "" {
				_ = client.SetProfile(originalProfile)
			}
		}()
	}

	startTime := time.Now()
	result := &DevCycleResult{
		Jobs: []JobSummary{},
	}

	absPath, pathSource := resolveDevCyclePath(path, request.Params.Meta)
	result.ResolvedPath = absPath
	result.PathSource = pathSource

	// Get commit before push
	cmd := exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "HEAD")
	out, _ := cmd.Output()
	beforeCommit := strings.TrimSpace(string(out))

	// Step 1: Run lgh up
	upOut, err := lghClient.Up(ctx, absPath, message)
	if err != nil {
		result.Error = fmt.Sprintf("lgh up failed: %v\nOutput: %s", err, upOut)
		result.Summary = "提交失败"
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}

	// Get commit after push
	cmd = exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "HEAD")
	out, _ = cmd.Output()
	result.Commit = strings.TrimSpace(string(out))

	// Get repo name for event log lookup
	cmd = exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "--show-toplevel")
	out, _ = cmd.Output()
	repoName := filepath.Base(strings.TrimSpace(string(out)))

	// Step 2: Find event_id from LGH event log
	eventID, eventIDErr := findEventIDFromLog(result.Commit, repoName)
	if eventIDErr != nil {
		// Non-fatal: degrade to fuzzy repo-match, but make it visible.
		fmt.Fprintf(os.Stderr, "[dev_cycle_run] findEventIDFromLog: %v — falling back to repo-name match\n", eventIDErr)
	}

	// Step 3: Poll for job completion — use event_id if available, fallback to repo matching
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	var newJobs []ActionInfo

	for time.Now().Before(deadline) {
		var actions []ActionInfo
		var err error

		if eventID != "" {
			// Precise: query by event_id
			actions, err = client.GetActionsByEventID(eventID)
		} else {
			// Fallback: query recent actions and filter by repo
			actions, err = client.GetActions(50)
		}

		if err != nil {
			result.Error = fmt.Sprintf("Failed to get actions: %v", err)
			break
		}

		if eventID != "" {
			// event_id query returns exact matches — just use them directly
			newJobs = actions
		} else {
			// Fallback: filter by repo name (old behavior)
			for _, a := range actions {
				if strings.Contains(a.Repo, repoName) || strings.Contains(repoName, a.Repo) {
					alreadyExists := false
					for _, j := range newJobs {
						if j.ID == a.ID {
							alreadyExists = true
							break
						}
					}
					if !alreadyExists {
						newJobs = append(newJobs, a)
					}
				}
			}
			// Update job statuses
			for i, nj := range newJobs {
				for _, a := range actions {
					if nj.ID == a.ID {
						newJobs[i] = a
					}
				}
			}
		}

		// Check if all jobs are done
		if len(newJobs) > 0 {
			allDone := true
			for _, j := range newJobs {
				if j.Status != "done" && j.Status != "failed" && j.Status != "error" && j.Status != "cancelled" {
					allDone = false
					break
				}
			}
			if allDone {
				break
			}
		}

		time.Sleep(1 * time.Second)
	}

	// Step 4: Collect results
	allSuccess := true
	for _, job := range newJobs {
		duration := fmt.Sprintf("%dms", job.DurationMs)
		if job.DurationMs > 1000 {
			duration = fmt.Sprintf("%.1fs", float64(job.DurationMs)/1000)
		}

		result.Jobs = append(result.Jobs, JobSummary{
			ID:       job.ID,
			Plugin:   job.PluginName,
			Status:   job.Status,
			Duration: duration,
		})

		if job.Status != "done" {
			allSuccess = false
		}
	}

	result.Success = allSuccess
	result.Duration = time.Since(startTime).Round(100 * time.Millisecond).String()

	// Generate summary
	if len(result.Jobs) == 0 {
		result.Summary = "未检测到触发的 CI 任务"
	} else if allSuccess {
		result.Summary = fmt.Sprintf("✅ 全部通过 (%d 个插件)", len(result.Jobs))
	} else {
		failed := 0
		for _, j := range result.Jobs {
			if j.Status != "done" {
				failed++
			}
		}
		result.Summary = fmt.Sprintf("❌ %d/%d 个任务失败", failed, len(result.Jobs))
	}

	// Step 5: Auto rollback if requested and failed
	if !allSuccess && autoRollback && beforeCommit != result.Commit {
		rollbackOut, err := lghClient.Rollback(ctx, absPath)
		if err != nil {
			result.Rollback = &RollbackInfo{
				FromCommit: result.Commit,
				ToCommit:   beforeCommit,
				Message:    fmt.Sprintf("回滚失败: %v", err),
			}
		} else {
			result.Rollback = &RollbackInfo{
				FromCommit: result.Commit,
				ToCommit:   beforeCommit,
				Message:    rollbackOut,
			}
			result.Summary += " (已回滚)"
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func resolveDevCyclePath(path string, meta *mcp.Meta) (string, string) {
	if path != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return path, "args.path"
		}
		return absPath, "args.path"
	}

	if meta != nil {
		if candidate, ok := getMetaString(meta, "cwd"); ok && candidate != "" {
			if absPath, err := filepath.Abs(candidate); err == nil {
				return absPath, "meta.cwd"
			}
			return candidate, "meta.cwd"
		}
		if candidate, ok := getMetaString(meta, "path"); ok && candidate != "" {
			if absPath, err := filepath.Abs(candidate); err == nil {
				return absPath, "meta.path"
			}
			return candidate, "meta.path"
		}
		if candidate, ok := getMetaString(meta, "workspace"); ok && candidate != "" {
			if absPath, err := filepath.Abs(candidate); err == nil {
				return absPath, "meta.workspace"
			}
			return candidate, "meta.workspace"
		}
	}

	absPath, err := filepath.Abs(".")
	if err != nil {
		return ".", "server.cwd"
	}
	return absPath, "server.cwd"
}

func getMetaString(meta *mcp.Meta, key string) (string, bool) {
	if meta == nil {
		return "", false
	}
	if meta.AdditionalFields == nil {
		return "", false
	}
	value, ok := meta.AdditionalFields[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func handleJobWait(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)

	id := getString(args, "id")
	if id == "" {
		return mcp.NewToolResultError("Missing required parameter: id"), nil
	}

	timeout := getInt(args, "timeout")
	if timeout == 0 {
		timeout = 300
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	for time.Now().Before(deadline) {
		action, err := client.GetAction(id)
		if err != nil {
			return mcp.NewToolResultError("Failed to get action: " + err.Error()), nil
		}

		if action.Status == "done" || action.Status == "failed" || action.Status == "error" || action.Status == "cancelled" {
			data, _ := json.MarshalIndent(action, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for job %s after %d seconds", id, timeout)), nil
}

func handleCancel(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)

	id := getString(args, "id")
	if id == "" {
		return mcp.NewToolResultError("Missing required parameter: id"), nil
	}

	err := client.CancelAction(id)
	if err != nil {
		return mcp.NewToolResultError("Failed to cancel action: " + err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully cancelled action %s", id)), nil
}

// getString gets a string argument with default
func getString(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// lghEventRecord is the canonical shape of a single line in events.jsonl.
// All JSON field access must go through parseEventRecord — this is the single
// point of format-coupling. If the schema changes, only this struct needs updating.
type lghEventRecord struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Repo    string                 `json:"repo"`
	Payload map[string]interface{} `json:"payload"`
}

// parseEventRecord parses one line from events.jsonl into an lghEventRecord.
// Returns an error if the line is not valid JSON or is missing required fields,
// so callers can distinguish "bad format" from "not the record we want".
func parseEventRecord(line string) (lghEventRecord, error) {
	var evt lghEventRecord
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return lghEventRecord{}, fmt.Errorf("events.jsonl parse error: %w", err)
	}
	if evt.ID == "" || evt.Type == "" {
		return lghEventRecord{}, fmt.Errorf("events.jsonl record missing required fields id/type (got: %q)", line)
	}
	return evt, nil
}

// findEventIDFromLog reads the LGH event JSONL log to find the event_id for a
// commit+repo. Returns (id, nil) on match, ("", nil) when no matching record is
// found (normal case: ActionD not running, push not yet logged), and
// ("", err) when the log exists but contains unreadable/malformed lines that
// prevent a reliable search — callers should log the error and degrade gracefully.
func findEventIDFromLog(commitHash, repoName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home dir: %w", err)
	}
	eventLogPath := filepath.Join(home, ".localgithub", "events", "events.jsonl")
	f, err := os.Open(eventLogPath)
	if err != nil {
		// File not existing is normal when LGH hasn't emitted any events yet.
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open events.jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Read all lines using bufio.Reader (reliable for large files)
	var lines []string
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
		if readErr != nil {
			break
		}
	}

	// Search from end (most recent first), cap at last 50 lines.
	start := len(lines) - 50
	if start < 0 {
		start = 0
	}
	var parseErrors []string
	for i := len(lines) - 1; i >= start; i-- {
		evt, err := parseEventRecord(lines[i])
		if err != nil {
			parseErrors = append(parseErrors, err.Error())
			continue
		}
		if evt.Type != "git.push" {
			continue
		}
		// Match by repo name (with or without .git suffix)
		if evt.Repo != repoName+".git" && evt.Repo != repoName {
			continue
		}
		if payload, ok := evt.Payload["changes"].(map[string]interface{}); ok {
			for _, change := range payload {
				if changeMap, ok := change.(map[string]interface{}); ok {
					newHash, _ := changeMap["new"].(string)
					if strings.HasPrefix(newHash, commitHash) || strings.HasPrefix(commitHash, newHash) {
						return evt.ID, nil
					}
				}
			}
		}
	}
	if len(parseErrors) > 0 {
		// Return an error so callers know the search was degraded, not just empty.
		return "", fmt.Errorf("%d line(s) in events.jsonl could not be parsed: %s",
			len(parseErrors), strings.Join(parseErrors, "; "))
	}
	return "", nil
}
