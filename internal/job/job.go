// Copyright (c) 2025 JoeGlenn1213
// ActionD Job Model - The core "auditable work unit"

package job

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status represents the state of an ActionJob
type Status string

const (
	StatusPending       Status = "pending"        // Job created, not started
	StatusQueued        Status = "queued"         // Waiting in worker queue
	StatusRunning       Status = "running"        // Currently executing
	StatusDone          Status = "done"           // Completed successfully
	StatusFailed        Status = "failed"         // Completed with failure
	StatusCanceled      Status = "cancelled"      // Manually cancelled
	StatusRetrying      Status = "retrying"       // Retrying after failure
	StatusBlocked       Status = "blocked"        // Blocked by dependency
	StatusNeedsApproval Status = "needs_approval" // Waiting for human approval
	StatusRetryable     Status = "retryable"      // Can be retried
)

// TriggerReason represents why an action was triggered
type TriggerReason string

const (
	TriggerGitPush TriggerReason = "git.push"
	TriggerGitTag  TriggerReason = "git.tag"
	TriggerManual  TriggerReason = "manual"
	TriggerRetry   TriggerReason = "retry"
	TriggerWebhook TriggerReason = "webhook"
)

// ActionJob represents a unit of work triggered by an event
// This is the "auditable ticket" - core abstraction for slow intelligence
type ActionJob struct {
	// Core Identity
	ID      string `json:"id"`
	RunID   string `json:"run_id,omitempty"` // Visual ID: run-{date}-{hash}
	EventID string `json:"event_id,omitempty"`

	// Repository & Event Context
	Repo          string        `json:"repo"`
	EventType     string        `json:"event_type,omitempty"`     // git.push, git.tag, etc.
	TriggerReason TriggerReason `json:"trigger_reason,omitempty"` // push, tag, manual, retry
	EventJSON     string        `json:"-"`                        // Raw event data (not serialized)

	// Action Identification
	Action     string `json:"action"`      // Plugin name (e.g., go-lint, deepwiki)
	PluginName string `json:"plugin_name"` // Same as Action, for compatibility
	// PluginVersion is the plugin manifest version at dispatch time —
	// verifier provenance (ASSURANCE Phase B). Empty when unknown.
	PluginVersion string `json:"plugin_version,omitempty"`
	// Profile is the execution profile active at dispatch time —
	// verdict tier provenance (fast -> FAST, full/release -> FULL).
	Profile string `json:"profile,omitempty"`
	// Intent is the task/goal id recorded at dispatch time (native
	// mutation contract v1, ASSURANCE §1). Empty = unknown (R2: 不计 native).
	Intent string `json:"intent,omitempty"`

	// Status
	Status   Status `json:"status"`
	Progress string `json:"progress,omitempty"`

	// Git Context
	Commit       CommitInfo `json:"commit,omitempty"`
	Branch       string     `json:"branch,omitempty"`
	Tag          string     `json:"tag,omitempty"`
	CommitSHA    string     `json:"commit_sha,omitempty"`
	CommitMsg    string     `json:"commit_message,omitempty"`
	CommitAuthor string     `json:"commit_author,omitempty"`

	// Timing
	CreatedAt   time.Time     `json:"created_at"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	EndedAt     *time.Time    `json:"ended_at,omitempty"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
	DurationMs  int64         `json:"duration_ms,omitempty"`
	Duration    time.Duration `json:"-"` // Internal duration

	// Execution Results
	Error        string   `json:"error,omitempty"`
	ErrorSummary string   `json:"error_summary,omitempty"` // AI-friendly short error
	ExitCode     int      `json:"exit_code,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"`
	RawLogPath   string   `json:"raw_log_path,omitempty"`

	// Structured Result (v0.3 feature)
	Result *ActionResult `json:"result,omitempty"`

	// Execution metadata
	Model  string `json:"model,omitempty"`
	Tokens int    `json:"tokens,omitempty"`

	// Retry tracking
	RetryCount  int    `json:"retry_count,omitempty"`
	RetryOf     string `json:"retry_of,omitempty"`     // Original job ID if retry
	OriginalRun string `json:"original_run,omitempty"` // Original run ID if retry
}

// ActionResult represents a structured execution result for AI consumption
// V1.1 standard: All fields for full traceability
type ActionResult struct {
	// Identity (for traceability)
	ActionID string `json:"action_id,omitempty"` // Unique action instance ID
	RunID    string `json:"run_id,omitempty"`    // Parent run ID
	Profile  string `json:"profile,omitempty"`   // Profile used (fast/full/release)
	Trigger  string `json:"trigger,omitempty"`   // git.push, git.tag, etc.
	Repo     string `json:"repo,omitempty"`      // Repository name
	Module   string `json:"module,omitempty"`    // Affected module (for affected-scope)

	// Timing
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// Status & Decision
	Status   string `json:"status"`             // success, failure, error
	Decision string `json:"decision,omitempty"` // approved, rejected, blocked, passed

	// Summary & Hints
	Summary     string   `json:"summary"`                // Human-readable summary
	FailedStep  string   `json:"failed_step,omitempty"`  // Which step failed
	Hints       []string `json:"hints,omitempty"`        // Suggested next steps
	Signals     []string `json:"signals,omitempty"`      // Key findings for AI
	NextActions []string `json:"next_actions,omitempty"` // Suggested follow-up actions

	// Outputs
	Artifacts  []string               `json:"artifacts,omitempty"`
	RawOutputs map[string]interface{} `json:"raw_outputs,omitempty"` // Original plugin output
	Metrics    map[string]interface{} `json:"metrics,omitempty"`
	Details    map[string]string      `json:"details,omitempty"` // Additional structured data
}

// CommitInfo contains git commit information
type CommitInfo struct {
	Hash    string `json:"hash,omitempty"`
	Message string `json:"message,omitempty"`
	Author  string `json:"author,omitempty"`
}

// NewJob creates a new pending job
func NewJob(eventID, repo, action string) *ActionJob {
	return &ActionJob{
		ID:         generateID(),
		EventID:    eventID,
		Repo:       repo,
		Action:     action,
		PluginName: action,
		Status:     StatusPending,
		CreatedAt:  time.Now(),
		Artifacts:  []string{},
	}
}

// NewJobWithTrigger creates a job with trigger information
func NewJobWithTrigger(eventID, repo, action string, trigger TriggerReason) *ActionJob {
	j := NewJob(eventID, repo, action)
	j.TriggerReason = trigger
	return j
}

// Start marks the job as running
func (j *ActionJob) Start() {
	now := time.Now()
	j.StartedAt = &now
	j.Status = StatusRunning
}

// Clone returns a deep copy of the job so that callers (and in-memory store
// readers) never share mutable state with the worker goroutine that owns the
// original. Shared pointers caused data races detected by go test -race.
func (j *ActionJob) Clone() *ActionJob {
	if j == nil {
		return nil
	}
	cp := *j
	if j.StartedAt != nil {
		t := *j.StartedAt
		cp.StartedAt = &t
	}
	if j.EndedAt != nil {
		t := *j.EndedAt
		cp.EndedAt = &t
	}
	if j.Artifacts != nil {
		cp.Artifacts = append([]string(nil), j.Artifacts...)
	}
	if j.Result != nil {
		cp.Result = j.Result.CloneResult()
	}
	return &cp
}

// CloneResult returns a deep copy of the ActionResult (nil-safe), so callers
// never share mutable slices/maps with the writer side.
func (r *ActionResult) CloneResult() *ActionResult {
	if r == nil {
		return nil
	}
	cp := *r
	if r.StartedAt != nil {
		t := *r.StartedAt
		cp.StartedAt = &t
	}
	if r.FinishedAt != nil {
		t := *r.FinishedAt
		cp.FinishedAt = &t
	}
	cp.Hints = append([]string(nil), r.Hints...)
	cp.Signals = append([]string(nil), r.Signals...)
	cp.NextActions = append([]string(nil), r.NextActions...)
	cp.Artifacts = append([]string(nil), r.Artifacts...)
	cp.RawOutputs = cloneAnyMap(r.RawOutputs)
	cp.Metrics = cloneAnyMap(r.Metrics)
	cp.Details = cloneStringMap(r.Details)
	return &cp
}

// cloneAnyMap returns a shallow copy of a map[string]interface{} (nil-safe).
func cloneAnyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// cloneStringMap returns a shallow copy of a map[string]string (nil-safe).
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Complete marks the job as done
func (j *ActionJob) Complete() {
	now := time.Now()
	j.EndedAt = &now
	j.CompletedAt = now
	j.Status = StatusDone
	if j.StartedAt != nil {
		j.DurationMs = now.Sub(*j.StartedAt).Milliseconds()
	}
}

// CompleteWithResult marks the job as done with structured result
func (j *ActionJob) CompleteWithResult(result *ActionResult) {
	j.Complete()
	j.Result = result
	if result != nil {
		if len(result.Artifacts) > 0 {
			j.Artifacts = result.Artifacts
		}
	}
}

// Fail marks the job as failed
func (j *ActionJob) Fail(err error) {
	now := time.Now()
	j.EndedAt = &now
	j.CompletedAt = now
	j.Status = StatusFailed
	j.Error = err.Error()
	// Generate error summary (first 200 chars)
	if len(err.Error()) > 200 {
		j.ErrorSummary = err.Error()[:200] + "..."
	} else {
		j.ErrorSummary = err.Error()
	}
	if j.StartedAt != nil {
		j.DurationMs = now.Sub(*j.StartedAt).Milliseconds()
	}
}

// FailWithResult marks the job as failed with structured result
func (j *ActionJob) FailWithResult(err error, result *ActionResult) {
	j.Fail(err)
	j.Result = result
	if result != nil {
		if result.Summary != "" {
			j.ErrorSummary = result.Summary
		}
	}
}

// Cancel marks the job as cancelled.
func (j *ActionJob) Cancel(reason string) {
	now := time.Now()
	j.EndedAt = &now
	j.CompletedAt = now
	j.Status = StatusCanceled
	j.Error = reason
	if j.StartedAt != nil {
		j.DurationMs = now.Sub(*j.StartedAt).Milliseconds()
	}
}

// MarkRetry marks this job as a retry of another job
func (j *ActionJob) MarkRetry(originalID, originalRunID string, retryCount int) {
	j.RetryOf = originalID
	j.OriginalRun = originalRunID
	j.RetryCount = retryCount
	j.TriggerReason = TriggerRetry
}

// IsRetry returns true if this job is a retry
func (j *ActionJob) IsRetry() bool {
	return j.RetryOf != ""
}

// SetGitContext sets git-related fields from event
func (j *ActionJob) SetGitContext(commitSHA, branch, tag, commitMsg, author string) {
	j.CommitSHA = commitSHA
	j.Branch = branch
	j.Tag = tag
	j.CommitMsg = commitMsg
	j.CommitAuthor = author
	j.Commit = CommitInfo{
		Hash:    commitSHA,
		Message: commitMsg,
		Author:  author,
	}
}

// GetRef returns the git ref (branch or tag)
func (j *ActionJob) GetRef() string {
	if j.Tag != "" {
		return "tags/" + j.Tag
	}
	if j.Branch != "" {
		return "heads/" + j.Branch
	}
	return ""
}

func generateID() string {
	return uuid.New().String()
}

// RetryPolicy defines retry behavior for a job
type RetryPolicy struct {
	MaxRetries         int           // Maximum number of retries allowed
	RetryDelay         time.Duration // Delay between retries
	BackoffMultiplier  float64       // Exponential backoff multiplier
	RetryableErrors    []string      // List of error patterns that trigger retry
	NonRetryableErrors []string      // List of error patterns that never trigger retry
}

// DefaultRetryPolicy returns sensible defaults
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:         3,
		RetryDelay:         30 * time.Second,
		BackoffMultiplier:  2.0,
		RetryableErrors:    []string{"connection refused", "timeout", "temporary"},
		NonRetryableErrors: []string{"permission denied", "not found", "invalid"},
	}
}

// CanRetry checks if this job can be retried based on policy
func (j *ActionJob) CanRetry(policy RetryPolicy) bool {
	// Can't retry if already succeeded
	if j.Status == StatusDone {
		return false
	}
	// Can't retry if manually cancelled
	if j.Status == StatusCanceled {
		return false
	}
	// Can't retry if already approved/deployed
	if j.Action == "approval-gate" || j.Action == "deploy" {
		return false
	}
	// Check retry count
	if j.RetryCount >= policy.MaxRetries {
		return false
	}
	// Check non-retryable errors
	for _, pattern := range policy.NonRetryableErrors {
		if strings.Contains(j.Error, pattern) {
			return false
		}
	}
	return true
}

// CanResume checks if this job can be resumed (was blocked/running)
func (j *ActionJob) CanResume() bool {
	return j.Status == StatusBlocked || j.Status == StatusRunning
}

// MarkRetryable marks job as retryable after a recoverable failure
func (j *ActionJob) MarkRetryable() {
	j.Status = StatusRetryable
}

// MarkNeedsApproval marks job as waiting for human approval
func (j *ActionJob) MarkNeedsApproval() {
	j.Status = StatusNeedsApproval
}

// MarkBlocked marks job as blocked by dependency
func (j *ActionJob) MarkBlocked(reason string) {
	j.Status = StatusBlocked
	j.ErrorSummary = reason
}

// IsTerminal returns true if status is terminal (no further action possible)
func (j *ActionJob) IsTerminal() bool {
	return j.Status == StatusDone || j.Status == StatusFailed || j.Status == StatusCanceled
}

// ShouldHardStop returns true if failure should stop entire pipeline
func (j *ActionJob) ShouldHardStop() bool {
	// Critical stages that hard-stop on failure
	hardStopActions := map[string]bool{
		"approval-gate": true,
		"deploy":        true,
	}
	return hardStopActions[j.Action] && j.Status == StatusFailed
}

// intentPattern is the commit-message convention for declaring task/goal
// intent: `task:<id>`, `goal:<id>`, or bracketed forms. Single source of
// truth — shared by the dispatch-time recording (worker) and the report
// reconstruction (mcp).
var intentPattern = regexp.MustCompile(`(?i)\b(?:task|goal):\s*([A-Za-z0-9_.-]{1,64})|\[(?:task|goal):\s*([A-Za-z0-9_.-]{1,64})\]`)

// IntentFromMessage extracts a task/goal id from a commit message, or "".
func IntentFromMessage(message string) string {
	m := intentPattern.FindStringSubmatch(message)
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}
