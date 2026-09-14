// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Dev Cycle Workflow

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// LGHClient interface for LGH operations
type LGHClient interface {
	Up(ctx context.Context, path, message string) (string, error)
	Save(ctx context.Context, path, message string) (string, error)
	Rollback(ctx context.Context, path, toCommit string) (string, error)
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

// Rollback resets the repo to the explicitly given commit. It never derives
// HEAD~1 on its own: the caller records the pre-run commit and passes it in,
// and handleDevCycleRun guards with rollbackGuard first, so reset --hard
// cannot destroy commits or edits that appeared while CI was running.
func (c *LocalLGHClient) Rollback(ctx context.Context, path, toCommit string) (string, error) {
	if toCommit == "" {
		return "", fmt.Errorf("rollback target commit is empty")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "reset", "--hard", toCommit)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// rollbackGuard verifies the repo is still exactly where this run left it
// before auto-rollback may touch anything: HEAD must equal the commit this
// run created, the working tree must be clean, and exactly ONE commit
// (this run's own) may sit on top of baseCommit — so a commit that landed
// while lgh up was waiting for CI, or while jobs were running, is never
// removed by reset --hard.
func rollbackGuard(ctx context.Context, path, expectHead, baseCommit string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("无法读取当前 HEAD: %w", err)
	}
	if head := strings.TrimSpace(string(out)); head != expectHead {
		return fmt.Errorf("HEAD 已从 %s 移动到 %s（等待期间产生了新提交）", expectHead, head)
	}
	out, err = exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("无法检查工作区状态: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return fmt.Errorf("工作区存在未提交修改（等待期间的新改动）")
	}
	if baseCommit != "" {
		if n := commitCountSince(ctx, path, baseCommit); n != 1 {
			return fmt.Errorf("基准提交 %s 之上有 %d 个新提交（应为 1 个，仅本次运行的提交）", baseCommit, n)
		}
	}
	return nil
}

// commitCountSince counts commits reachable from HEAD but not from base
// (git rev-list --count base..HEAD). Returns -1 on git failure; callers
// treat that as "cannot verify".
func commitCountSince(ctx context.Context, path, base string) int {
	if base == "" {
		return -1
	}
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-list", "--count", base+"..HEAD").Output()
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return n
}

// DevCycleResult represents the result of dev_cycle_run
type DevCycleResult struct {
	Success bool `json:"success"`
	// Verification is the tri-state verdict behind Success (same vocabulary
	// and aggregation rule as actiond_run_report): "pass" only when at least
	// one job ran, all passed and nothing was left unassociated; "fail" on
	// any observed failure; "unknown" for zero jobs, query errors, timeouts,
	// attribution uncertainty and unassociated candidates — never a pass.
	// Every return path (lgh up failure included) carries a declared value.
	Verification string `json:"verification"`
	Commit       string `json:"commit"`
	Jobs         []JobSummary `json:"jobs"`
	// UnassociatedJobs lists repo-matched jobs whose commit ownership could
	// not be confirmed (missing sha, detail query failing). They are never
	// silently discarded, and their presence forces verification=unknown.
	UnassociatedJobs []string      `json:"unassociated_jobs,omitempty"`
	Summary          string        `json:"summary"`
	Artifacts        []string      `json:"artifacts,omitempty"`
	Rollback         *RollbackInfo `json:"rollback,omitempty"`
	Duration         string        `json:"duration"`
	Error            string        `json:"error,omitempty"`
	ResolvedPath     string        `json:"resolved_path,omitempty"`
	PathSource       string        `json:"path_source,omitempty"`
}

// JobSummary represents a job result summary
type JobSummary struct {
	ID       string `json:"id"`
	Plugin   string `json:"plugin"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
}

// RollbackInfo represents rollback information. Status is tri-state:
// "done" (repo reset), "skipped" (guard rejected — new commits or edits
// appeared while CI ran, nothing was touched), "failed" (reset attempted
// but errored).
type RollbackInfo struct {
	FromCommit string `json:"from_commit"`
	ToCommit   string `json:"to_commit"`
	Status     string `json:"status"`
	Message    string `json:"message"`
}

// registerWorkflowTools registers workflow-related MCP tools
func registerWorkflowTools(s *server.MCPServer, client ActionDClient, lghClient LGHClient) {
	// dev_cycle_run - End-to-end development cycle
	s.AddTool(
		mcp.NewTool("dev_cycle_run",
			mcp.WithDescription(`Run the end-to-end development loop — commit, trigger CI, wait for results, return a summary — in a single aggregated call. Internally it: (1) commits and pushes the working tree via "lgh up" (requires the LGH daemon running), (2) waits for the ActionD CI/CD jobs triggered by that push — the run's own commit is pinned from the LGH push event (the branch tip it replaced), so concurrent pushes while lgh up waits for CI are never adopted; jobs are then associated by event id, or by exact repo + commit-sha match, and jobs whose ownership cannot be confirmed are reported as "unassociated_jobs" and block a pass verdict — and (3) collects every observed job result (findings accumulate across polls; an observed failure is never dropped) into one structured output. Requires a message (the commit text); optional path (repo directory, defaults to the MCP client's working directory), timeout in seconds (default 300), profile (fast/full/release — switched temporarily for this run and restored afterwards; omit to keep the current profile), and auto_rollback (default false; on a confirmed failed verdict, resets the repo to the recorded pre-push commit — never on unverified runs, and skipped unless exactly that one commit sits on top of the pre-push base with a clean tree). The result reports verification ("pass"/"fail"/"unknown" — zero jobs, query errors, timeouts, uncertain attribution and unassociated candidates are all "unknown", never a pass; every return path carries a declared value), success (true only on "pass"), the commit sha, per-job statuses with durations, artifacts, rollback info (status done/skipped/failed) when applicable, and a human-readable summary. Use it after editing code to commit, test, and verify in one step.`),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:           "Run end-to-end dev cycle",
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
			mcp.WithString("path",
				mcp.Description("Repository path (defaults to the MCP client's working directory)"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("Git commit message"),
			),
			mcp.WithNumber("timeout",
				mcp.Description("Wait timeout in seconds (default 300 = 5 minutes)"),
			),
			withInteger("timeout"),
			mcp.WithBoolean("auto_rollback",
				mcp.Description("On failure, automatically roll back to the previous commit (default false)"),
			),
			mcp.WithString("profile",
				mcp.Enum("fast", "full", "release"),
				mcp.Description(`Execution profile for this run: fast/full/release (default: keep the current setting)
- fast: minimal CI, core lint and test only
- full: complete CI, adds security scan, coverage, etc.
- release: full CI/CD, adds build and deploy`),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleDevCycleRun(client, lghClient, ctx, request)
		},
	)

	// actiond_job_wait - Wait for job completion
	s.AddTool(
		mcp.NewTool("actiond_job_wait",
			mcp.WithDescription("Block until the given CI/CD job reaches a terminal status (done/failed/error/cancelled), then return the full job detail. Call it right after a push surfaces job IDs — for example, immediately after \"lgh up\" reports triggered_job_ids. The optional timeout in seconds (default 300) aborts the wait with an error if the job is still unfinished; it never cancels the job itself. Prefer this over polling actiond_action_get; use actiond_job_cancel to abort a stuck job instead."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:           "Wait for CI/CD job completion",
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("Job ID to wait for"),
			),
			mcp.WithNumber("timeout",
				mcp.Description("Timeout in seconds (default 300)"),
			),
			withInteger("timeout"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleJobWait(client, ctx, request)
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
	// Verification starts as "unknown": every early return below (lgh up
	// failure included) must carry a declared tri-state, never "".
	result := &DevCycleResult{
		Jobs:         []JobSummary{},
		Verification: "unknown",
	}

	absPath, pathSource := resolveDevCyclePath(path, request.Params.Meta)
	result.ResolvedPath = absPath
	result.PathSource = pathSource

	// Get commit before push
	cmd := exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "HEAD")
	out, _ := cmd.Output()
	beforeCommit := strings.TrimSpace(string(out))

	// Snapshot the LGH event-log boundary and the current ref BEFORE lgh up:
	// attribution below only trusts events appended after this boundary, and
	// only change entries for this ref — otherwise a historical push of
	// another branch from the same base tip could be mistaken for ours.
	eventLogBoundary := eventLogLineCount()
	refOut, _ := exec.CommandContext(ctx, "git", "-C", absPath, "symbolic-ref", "-q", "HEAD").Output()
	pushRef := strings.TrimSpace(string(refOut))

	// Step 1: Run lgh up
	upOut, err := lghClient.Up(ctx, absPath, message)
	if err != nil {
		result.Error = fmt.Sprintf("lgh up failed: %v\nOutput: %s", err, upOut)
		result.Summary = "提交失败（未验证）"
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}

	// Get repo name for event log lookup
	cmd = exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "--show-toplevel")
	out, _ = cmd.Output()
	repoName := filepath.Base(strings.TrimSpace(string(out)))

	// Determine the commit this run pushed. lgh up waits for CI after pushing
	// (up to ~60s), so a concurrent push during that wait can move HEAD —
	// reading HEAD now would adopt someone else's commit as ours. The LGH
	// event log identifies our push by an event APPENDED AFTER our pre-Up
	// boundary whose change entry for our ref moved the branch FROM
	// beforeCommit TO a new tip.
	var eventID string
	attributionUncertain := false
	pushEvt, pushNew, perr := findPushEventSince(repoName, beforeCommit, pushRef, eventLogBoundary)
	switch {
	case errors.Is(perr, errAmbiguousPush):
		// More than one in-window push could be ours — adopt none.
		attributionUncertain = true
	case perr != nil:
		fmt.Fprintf(os.Stderr, "[dev_cycle_run] findPushEventSince: %v — falling back to HEAD-based attribution\n", perr)
	case pushEvt != "" && pushNew != "":
		eventID = pushEvt
		result.Commit = pushNew
	}

	if eventID == "" {
		// Degrade: attribute to the post-Up HEAD and match an in-window
		// event by the new hash. If more than one commit landed on top of
		// beforeCommit we cannot tell whose jobs these are — keep them
		// visible but flag the run as attribution-uncertain (never a pass).
		cmd = exec.CommandContext(ctx, "git", "-C", absPath, "rev-parse", "HEAD")
		out, _ = cmd.Output()
		result.Commit = strings.TrimSpace(string(out))
		if n := commitCountSince(ctx, absPath, beforeCommit); n > 1 {
			attributionUncertain = true
		}
		if result.Commit != beforeCommit {
			// This run's push actually moved the branch.
			if evt, ferr := findEventIDFromLogSince(result.Commit, repoName, eventLogBoundary); ferr != nil {
				fmt.Fprintf(os.Stderr, "[dev_cycle_run] findEventIDFromLog: %v — falling back to repo+commit match\n", ferr)
			} else {
				eventID = evt
			}
		}
		// result.Commit == beforeCommit: lgh up pushed nothing new. Any job
		// we could match ran for a HISTORICAL push of the same commit —
		// reporting it as this run's verification would be a false pass, so
		// the run stays at zero jobs / unknown.
	}

	// Step 3: Poll for job completion — precise via event_id, otherwise exact
	// repo + commit-sha matching. Association invariants (review rounds 2–3):
	//   - a confirmed job is never dropped again: findings accumulate by ID
	//     across polls, so an observed failure survives the sliding 50-job
	//     list window;
	//   - a job whose repo matches splits into three outcomes: confirmed
	//     (this commit), foreign (a DIFFERENT commit — never blocks a pass
	//     once established), or unassociated (ownership still unknown after
	//     bounded detail retries — reported and pass-blocking);
	//   - known non-terminal jobs that leave the list window are tracked via
	//     the detail endpoint.
	const maxResolveAttempts = 3
	shaByID := map[string]string{}   // job ID -> successfully resolved sha (successes only)
	resolveAttempts := map[string]int{}
	known := map[string]ActionInfo{} // confirmed jobs for this run, by ID

	// associate splits one poll's actions into confirmed jobs, unassociated
	// candidates and definitively-foreign job IDs.
	associate := func(actions []ActionInfo) (confirmed, unassociated []ActionInfo, foreignIDs []string) {
		if eventID != "" {
			// Precise: the event query returns exactly this push's jobs.
			return actions, nil, nil
		}
		for _, a := range actions {
			if normalizeRepoName(a.Repo) != normalizeRepoName(repoName) {
				continue
			}
			sha := a.CommitSHA
			if sha == "" {
				var cached bool
				if sha, cached = shaByID[a.ID]; !cached {
					if resolveAttempts[a.ID] >= maxResolveAttempts {
						unassociated = append(unassociated, a) // give up, but report
						continue
					}
					resolveAttempts[a.ID]++
					if d, derr := client.GetAction(a.ID); derr == nil && d != nil {
						if h, _ := d.Commit["hash"].(string); h != "" {
							sha = h
							shaByID[a.ID] = h // cache successes only; failures retry
						}
					}
					if sha == "" {
						unassociated = append(unassociated, a)
						continue
					}
				}
			}
			if !sameCommit(sha, result.Commit) {
				// Definitively another commit's job: neither ours nor
				// pass-blocking — remove from any earlier unassociated set.
				foreignIDs = append(foreignIDs, a.ID)
				continue
			}
			a.CommitSHA = sha
			confirmed = append(confirmed, a)
		}
		return confirmed, unassociated, foreignIDs
	}

	// Nothing was pushed and no push event exists for this run: every job in
	// the system ran for a historical push, so polling can only produce a
	// false pass. Skip straight to the honest zero-jobs/unknown outcome.
	noPush := eventID == "" && result.Commit == beforeCommit

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	var queryErr error
	unresolved := map[string]ActionInfo{} // unassociated candidates, by ID

	for !noPush && time.Now().Before(deadline) {
		var actions []ActionInfo
		var err error

		if eventID != "" {
			actions, err = client.GetActionsByEventID(eventID)
		} else {
			actions, err = client.GetActions(50)
		}
		if err != nil {
			queryErr = err
			break
		}

		confirmed, unassociated, foreignIDs := associate(actions)
		inWindow := map[string]bool{}
		for _, a := range confirmed {
			known[a.ID] = a // accumulate: an observed failure is never dropped
			inWindow[a.ID] = true
			delete(unresolved, a.ID)
		}
		for _, u := range unassociated {
			unresolved[u.ID] = u
		}
		for _, id := range foreignIDs {
			// Ownership decided: this candidate is another commit's job and
			// must stop blocking (it may have been unassociated earlier when
			// its detail query was still failing).
			delete(unresolved, id)
		}
		// Track confirmed non-terminal jobs that dropped out of the list
		// window via the detail endpoint.
		for id, job := range known {
			if inWindow[id] || isTerminalStatus(job.Status) {
				continue
			}
			if d, derr := client.GetAction(id); derr == nil && d != nil && d.Status != "" {
				updated := known[id]
				updated.Status = d.Status
				known[id] = updated
			}
		}

		knownList := actionInfos(known)
		if len(knownList) > 0 && allTerminal(knownList) &&
			(len(unresolved) == 0 || resolveAttemptsExhausted(unresolved, resolveAttempts, maxResolveAttempts)) {
			break
		}

		time.Sleep(1 * time.Second)
	}
	newJobs := actionInfos(known)
	timedOut := !noPush && queryErr == nil && (len(newJobs) == 0 || !allTerminal(newJobs))

	if queryErr != nil {
		result.Error = fmt.Sprintf("Failed to get actions: %v", queryErr)
	}

	// Step 4: Collect results — tri-state verdict, same rule as the run
	// report: zero jobs, query errors, timeouts, attribution uncertainty and
	// unassociated candidates are "unknown", never a pass; only an observed
	// failure on this run's commit makes "fail"; all-terminal-passed (and at
	// least one job, nothing unassociated) makes "pass".
	sort.Slice(newJobs, func(i, j int) bool { return newJobs[i].ID < newJobs[j].ID })
	passed, failed := 0, 0
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

		switch statusToVerdict(job.Status) {
		case "pass":
			passed++
		case "fail":
			failed++
		}
	}
	for id := range unresolved {
		result.UnassociatedJobs = append(result.UnassociatedJobs, id)
	}
	sort.Strings(result.UnassociatedJobs)

	verification := aggregateVerdict(len(newJobs), passed, failed)
	if attributionUncertain {
		// Multiple commits landed on top of beforeCommit and no event info
		// separates them: even an observed failure cannot be attributed to
		// this run's change.
		verification = "unknown"
	} else if failed == 0 {
		// Interrupted or incomplete coverage — observed passes alone are not
		// a pass. (A confirmed failure with unassociated candidates stays
		// "fail": the failure evidence itself is sound.)
		if queryErr != nil || timedOut || len(unresolved) > 0 {
			verification = "unknown"
		}
	}
	result.Verification = verification
	result.Success = verification == "pass"
	result.Duration = time.Since(startTime).Round(100 * time.Millisecond).String()

	// Generate summary
	switch {
	case len(result.Jobs) == 0 && noPush:
		result.Summary = "⚠️ 本次运行没有产生新提交（推送无变更），未触发新检查，未验证（unknown）"
	case len(result.Jobs) == 0 && queryErr != nil:
		result.Summary = fmt.Sprintf("⚠️ 查询 CI 任务失败，未验证（unknown）: %v", queryErr)
	case len(result.Jobs) == 0 && attributionUncertain:
		result.Summary = "⚠️ 等待期间分支出现多个新提交，无法归属本次修改，未验证（unknown）"
	case len(result.Jobs) == 0:
		result.Summary = "⚠️ 未检测到关联到本次提交的 CI 任务，未验证（unknown）"
	case verification == "pass":
		result.Summary = fmt.Sprintf("✅ 全部通过 (%d 个插件)", len(result.Jobs))
	case verification == "fail":
		result.Summary = fmt.Sprintf("❌ %d/%d 个任务失败", failed, len(result.Jobs))
	case attributionUncertain:
		result.Summary = "⚠️ 等待期间分支出现多个新提交，任务无法精确归属，未验证（unknown）"
	case timedOut:
		result.Summary = fmt.Sprintf("⚠️ 等待超时，%d 个任务未完成，未验证（unknown）", len(result.Jobs)-passed-failed)
	default:
		result.Summary = "⚠️ 验证未完成（unknown）"
	}
	if len(unresolved) > 0 {
		result.Summary += fmt.Sprintf("；另有 %d 个候选任务归属未确认: %s", len(unresolved), strings.Join(result.UnassociatedJobs, ", "))
	}
	if queryErr != nil && len(result.Jobs) > 0 {
		result.Summary += fmt.Sprintf("；另外查询中断: %v", queryErr)
	}

	// Step 5: Auto rollback if requested and confirmed failed. Unknown runs
	// (zero jobs / query error / timeout / uncertain attribution) are never
	// auto-rolled back — no failure evidence, so reset --hard would only
	// destroy possibly-good work.
	if verification == "fail" && autoRollback && beforeCommit != "" && result.Commit != "" && beforeCommit != result.Commit {
		rbInfo := &RollbackInfo{FromCommit: result.Commit, ToCommit: beforeCommit}
		if reason := rollbackGuard(ctx, absPath, result.Commit, beforeCommit); reason != nil {
			rbInfo.Status = "skipped"
			rbInfo.Message = fmt.Sprintf("回滚已跳过（仓库在等待期间有新变动，未做任何改动）: %v", reason)
		} else if out, err := lghClient.Rollback(ctx, absPath, beforeCommit); err != nil {
			rbInfo.Status = "failed"
			rbInfo.Message = fmt.Sprintf("回滚失败: %v", err)
		} else {
			rbInfo.Status = "done"
			rbInfo.Message = out
			result.Summary += " (已回滚)"
		}
		result.Rollback = rbInfo
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

// allTerminal reports whether every job reached a terminal status. A
// pending/queued/running job anywhere keeps polling (and the final verdict
// at "unknown" if the deadline hits first).
func allTerminal(jobs []ActionInfo) bool {
	for _, j := range jobs {
		if !isTerminalStatus(j.Status) {
			return false
		}
	}
	return true
}

// isTerminalStatus reports whether a job status can no longer change.
func isTerminalStatus(status string) bool {
	switch status {
	case "done", "failed", "error", "cancelled":
		return true
	}
	return false
}

// actionInfos flattens a job map into a slice.
func actionInfos(m map[string]ActionInfo) []ActionInfo {
	out := make([]ActionInfo, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// resolveAttemptsExhausted reports whether every unassociated candidate has
// burned its detail-query retries (further polling cannot resolve them).
func resolveAttemptsExhausted(unresolved map[string]ActionInfo, attempts map[string]int, max int) bool {
	for id := range unresolved {
		if attempts[id] < max {
			return false
		}
	}
	return true
}

// sameCommit reports whether two non-empty shas refer to the same commit,
// tolerating short-vs-full forms (git-style prefix match). Empty never
// matches: no sha means no association.
func sameCommit(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
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

// eventRecord pairs a parsed event with its 1-based absolute line number in
// events.jsonl, so callers can restrict searches to records appended after a
// boundary observed earlier (e.g. before this run's lgh up).
type eventRecord struct {
	line int
	rec  lghEventRecord
}

// readRecentEventRecords loads the most recent event records from the LGH
// events.jsonl (newest first, capped at the last 50 lines). Returns no error
// when the file does not exist yet (LGH has not emitted events); malformed
// lines surface as an error so callers can tell "no match" from "couldn't
// search reliably".
func readRecentEventRecords() ([]eventRecord, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home dir: %w", err)
	}
	eventLogPath := filepath.Join(home, ".localgithub", "events", "events.jsonl")
	f, err := os.Open(eventLogPath)
	if err != nil {
		// File not existing is normal when LGH hasn't emitted any events yet.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open events.jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []eventRecord
	var parseErrors []string
	lineNo := 0
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadString('\n')
		lineNo++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			evt, err := parseEventRecord(trimmed)
			if err != nil {
				parseErrors = append(parseErrors, err.Error())
			} else {
				records = append(records, eventRecord{line: lineNo, rec: evt})
			}
		}
		if readErr != nil {
			break
		}
	}

	// Keep the most recent 50 records, newest first.
	start := len(records) - 50
	if start < 0 {
		start = 0
	}
	kept := make([]eventRecord, 0, len(records)-start)
	for i := len(records) - 1; i >= start; i-- {
		kept = append(kept, records[i])
	}
	if len(parseErrors) > 0 {
		// Return an error so callers know the search was degraded, not empty.
		return nil, fmt.Errorf("%d line(s) in events.jsonl could not be parsed: %s",
			len(parseErrors), strings.Join(parseErrors, "; "))
	}
	return kept, nil
}

// eventLogLineCount returns the current number of lines in the LGH event log
// (0 when absent). dev_cycle_run snapshots it BEFORE lgh up and only trusts
// events appended after that boundary, so a historical record can never be
// mistaken for this run's push.
func eventLogLineCount() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(home, ".localgithub", "events", "events.jsonl"))
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// errAmbiguousPush marks the case where more than one in-window push matches
// the attribution criteria: the run's own commit cannot be pinned, so callers
// must report unknown instead of adopting any candidate.
var errAmbiguousPush = errors.New("ambiguous push attribution")

// findEventIDFromLog reads the LGH event JSONL log to find the event_id for a
// commit+repo. Returns (id, nil) on match, ("", nil) when no matching record is
// found (normal case: ActionD not running, push not yet logged), and
// ("", err) when the log exists but contains unreadable/malformed lines that
// prevent a reliable search — callers should log the error and degrade gracefully.
func findEventIDFromLog(commitHash, repoName string) (string, error) {
	return findEventIDFromLogSince(commitHash, repoName, -1)
}

// findEventIDFromLogSince is findEventIDFromLog restricted to records
// appended after the given 1-based line boundary (< 0 = no boundary).
func findEventIDFromLogSince(commitHash, repoName string, sinceLines int) (string, error) {
	records, err := readRecentEventRecords()
	if err != nil {
		return "", err
	}
	for _, r := range records { // newest first
		if sinceLines >= 0 && r.line <= sinceLines {
			continue
		}
		if r.rec.Type != "git.push" || !repoMatchesEvent(r.rec.Repo, repoName) {
			continue
		}
		if newHash, ok := eventChangeHashes(r.rec, "new"); ok && sameCommit(newHash, commitHash) {
			return r.rec.ID, nil
		}
	}
	return "", nil
}

// findPushEventSince locates the git.push event for the push THIS run made:
// it only considers records appended after sinceLines (the pre-Up line
// boundary) and requires the change entry for ref (the branch this run was
// on) to carry BOTH the old tip matching oldCommit and a new tip — old and
// new from the same ref entry, never mixed across refs. Returns
// (eventID, newSHA, nil) on a unique match, empty values when nothing
// matches, errAmbiguousPush when more than one in-window push matches (the
// run's commit cannot be pinned), and other errors when the log is unreadable.
func findPushEventSince(repoName, oldCommit, ref string, sinceLines int) (string, string, error) {
	if oldCommit == "" {
		return "", "", nil
	}
	records, err := readRecentEventRecords()
	if err != nil {
		return "", "", err
	}
	var id, newSHA string
	matches := 0
	for _, r := range records { // newest first
		if sinceLines >= 0 && r.line <= sinceLines {
			continue
		}
		if r.rec.Type != "git.push" || !repoMatchesEvent(r.rec.Repo, repoName) {
			continue
		}
		old, new, ok := eventChangeRefHashes(r.rec, ref)
		if !ok || !sameCommit(old, oldCommit) || new == old {
			continue
		}
		matches++
		id, newSHA = r.rec.ID, new
	}
	if matches > 1 {
		return "", "", fmt.Errorf("multiple in-window pushes moved %s from %s: %w", ref, shortSHA(oldCommit), errAmbiguousPush)
	}
	return id, newSHA, nil
}

// repoMatchesEvent compares an event record's repo field against the given
// repo name (with or without the .git suffix).
func repoMatchesEvent(eventRepo, repoName string) bool {
	return eventRepo == repoName+".git" || eventRepo == repoName
}

// eventChangeHashes digs the "old" or "new" hash out of a git.push event's
// payload.changes map (any ref). ok=false when the payload has no such hash.
func eventChangeHashes(evt lghEventRecord, key string) (string, bool) {
	payload, ok := evt.Payload["changes"].(map[string]interface{})
	if !ok {
		return "", false
	}
	for _, change := range payload {
		changeMap, ok := change.(map[string]interface{})
		if !ok {
			continue
		}
		if hash, _ := changeMap[key].(string); hash != "" {
			return hash, true
		}
	}
	return "", false
}

// eventChangeRefHashes reads the old AND new tip from the same change entry —
// the one keyed by ref. An empty ref (detached HEAD) falls back to the first
// entry carrying both values. Mixing hashes across different refs' entries
// would fabricate a push that never happened.
func eventChangeRefHashes(evt lghEventRecord, ref string) (old, new string, ok bool) {
	payload, ok := evt.Payload["changes"].(map[string]interface{})
	if !ok {
		return "", "", false
	}
	entryOf := func(key string) (map[string]interface{}, bool) {
		if ref != "" {
			m, yes := payload[ref].(map[string]interface{})
			return m, yes
		}
		for _, v := range payload {
			if m, yes := v.(map[string]interface{}); yes {
				if _, hasOld := m["old"]; hasOld {
					return m, true
				}
			}
		}
		return nil, false
	}
	m, ok := entryOf(ref)
	if !ok {
		return "", "", false
	}
	old, _ = m["old"].(string)
	new, _ = m["new"].(string)
	return old, new, old != "" && new != ""
}
