package job

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestActionResultV1Fields(t *testing.T) {
	// Test that ActionResult has all V1.1 required fields
	now := time.Now()

	result := &ActionResult{
		ActionID:    "action-123",
		RunID:       "run-456",
		Profile:     "fast",
		Trigger:     "git.push",
		Repo:        "test-repo",
		Module:      "core",
		StartedAt:   &now,
		FinishedAt:  &now,
		Status:      "success",
		Decision:    "passed",
		Summary:     "All checks passed",
		FailedStep:  "",
		Hints:       []string{"Proceed to next stage"},
		Signals:     []string{"Low complexity", "All tests green"},
		NextActions: []string{"Approve PR"},
		Artifacts:   []string{"build/bin", "coverage.json"},
		Metrics:     map[string]interface{}{"coverage": 85.5},
		Details:     map[string]string{"lint": "passed"},
	}

	// Marshal to JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal ActionResult: %v", err)
	}

	// Unmarshal back
	var parsed ActionResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ActionResult: %v", err)
	}

	// Verify all fields
	if parsed.ActionID != "action-123" {
		t.Errorf("ActionID mismatch: got %s", parsed.ActionID)
	}
	if parsed.RunID != "run-456" {
		t.Errorf("RunID mismatch: got %s", parsed.RunID)
	}
	if parsed.Profile != "fast" {
		t.Errorf("Profile mismatch: got %s", parsed.Profile)
	}
	if parsed.Decision != "passed" {
		t.Errorf("Decision mismatch: got %s", parsed.Decision)
	}
	if len(parsed.Signals) != 2 {
		t.Errorf("Signals count mismatch: got %d", len(parsed.Signals))
	}
	if len(parsed.NextActions) != 1 {
		t.Errorf("NextActions count mismatch: got %d", len(parsed.NextActions))
	}
}

func TestActionResultWithRawOutputs(t *testing.T) {
	result := &ActionResult{
		Status:  "success",
		Summary: "Lint passed",
		RawOutputs: map[string]interface{}{
			"files_checked": 42,
			"issues_found":  0,
			"exit_code":     0,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed ActionResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed.RawOutputs == nil {
		t.Fatal("RawOutputs should not be nil")
	}
}

func TestActionJobWithResult(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	result := &ActionResult{
		ActionID: job.ID,
		RunID:    "run-789",
		Status:   "success",
		Summary:  "Lint passed with 0 issues",
	}

	job.CompleteWithResult(result)

	if job.Status != StatusDone {
		t.Errorf("expected StatusDone, got %s", job.Status)
	}
	if job.Result == nil {
		t.Fatal("Result should not be nil")
	}
	if job.Result.Status != "success" {
		t.Errorf("expected result status success, got %s", job.Result.Status)
	}
}

func TestNewJobWithAllFields(t *testing.T) {
	job := NewJob("event-1", "my-repo", "security-scan")
	job.RunID = "run-123"
	job.TriggerReason = TriggerGitPush

	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}
	if job.RunID != "run-123" {
		t.Errorf("RunID mismatch: got %s", job.RunID)
	}
	if job.TriggerReason != TriggerGitPush {
		t.Errorf("TriggerReason mismatch: got %s", job.TriggerReason)
	}
}

func TestRetryPolicy(t *testing.T) {
	policy := DefaultRetryPolicy()

	// Test retryable job
	retryableJob := NewJob("event-1", "test-repo", "go-lint")
	retryableJob.Fail(fmt.Errorf("connection refused"))

	if !retryableJob.CanRetry(policy) {
		t.Error("Job with connection refused error should be retryable")
	}

	// Test non-retryable job
	nonRetryableJob := NewJob("event-1", "test-repo", "deploy")
	nonRetryableJob.Fail(fmt.Errorf("permission denied"))

	if nonRetryableJob.CanRetry(policy) {
		t.Error("Job with permission denied error should not be retryable")
	}

	// Test max retries exceeded
	maxRetryJob := NewJob("event-1", "test-repo", "go-lint")
	maxRetryJob.Fail(fmt.Errorf("connection refused"))
	maxRetryJob.RetryCount = 5 // exceeds default MaxRetries of 3

	if maxRetryJob.CanRetry(policy) {
		t.Error("Job exceeding max retries should not be retryable")
	}
}

func TestStatusTransitions(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	// Initial state
	if job.Status != StatusPending {
		t.Errorf("expected StatusPending, got %s", job.Status)
	}

	// Start
	job.Start()
	if job.Status != StatusRunning {
		t.Errorf("expected StatusRunning, got %s", job.Status)
	}

	// Complete
	job.Complete()
	if job.Status != StatusDone {
		t.Errorf("expected StatusDone, got %s", job.Status)
	}
}

func TestIsTerminal(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	job.Complete()
	if !job.IsTerminal() {
		t.Error("done job should be terminal")
	}

	job.Fail(fmt.Errorf("test"))
	if !job.IsTerminal() {
		t.Error("failed job should be terminal")
	}

	job2 := NewJob("event-2", "test-repo", "go-lint")
	if job2.IsTerminal() {
		t.Error("pending job should not be terminal")
	}
}

func TestNewJobWithTrigger(t *testing.T) {
	job := NewJobWithTrigger("event-1", "test-repo", "go-test", TriggerGitPush)

	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}
	if job.EventID != "event-1" {
		t.Errorf("EventID mismatch: got %s", job.EventID)
	}
	if job.Repo != "test-repo" {
		t.Errorf("Repo mismatch: got %s", job.Repo)
	}
	if job.Action != "go-test" {
		t.Errorf("Action mismatch: got %s", job.Action)
	}
	if job.TriggerReason != TriggerGitPush {
		t.Errorf("TriggerReason mismatch: expected %s, got %s", TriggerGitPush, job.TriggerReason)
	}
}

func TestFailWithResult(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")
	job.Start()

	result := &ActionResult{
		Status:  "failure",
		Summary: "Lint failed with 5 issues",
	}
	err := fmt.Errorf("exit status 1")

	job.FailWithResult(err, result)

	if job.Status != StatusFailed {
		t.Errorf("expected StatusFailed, got %s", job.Status)
	}
	if job.Error != "exit status 1" {
		t.Errorf("Error mismatch: got %s", job.Error)
	}
	if job.Result == nil {
		t.Fatal("Result should not be nil")
	}
	if job.Result.Summary != "Lint failed with 5 issues" {
		t.Errorf("ErrorSummary mismatch: got %s", job.ErrorSummary)
	}
	if job.EndedAt == nil {
		t.Error("EndedAt should be set")
	}
}

func TestCancel(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")
	job.Start()

	job.Cancel("Cancelled by user")

	if job.Status != StatusCanceled {
		t.Errorf("expected StatusCanceled, got %s", job.Status)
	}
	if job.Error != "Cancelled by user" {
		t.Errorf("Error mismatch: got %s", job.Error)
	}
	if job.EndedAt == nil {
		t.Error("EndedAt should be set")
	}
}

func TestMarkRetry(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	job.MarkRetry("original-job-id", "run-original-123", 2)

	if job.RetryOf != "original-job-id" {
		t.Errorf("RetryOf mismatch: got %s", job.RetryOf)
	}
	if job.OriginalRun != "run-original-123" {
		t.Errorf("OriginalRun mismatch: got %s", job.OriginalRun)
	}
	if job.RetryCount != 2 {
		t.Errorf("RetryCount mismatch: got %d", job.RetryCount)
	}
	if job.TriggerReason != TriggerRetry {
		t.Errorf("TriggerReason mismatch: expected %s, got %s", TriggerRetry, job.TriggerReason)
	}
}

func TestIsRetry(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	if job.IsRetry() {
		t.Error("new job should not be a retry")
	}

	job.MarkRetry("original-id", "run-orig", 1)

	if !job.IsRetry() {
		t.Error("job with RetryOf should be a retry")
	}
}

func TestSetGitContext(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	job.SetGitContext("abc123", "main", "", "Fix bug", "Joe")

	if job.CommitSHA != "abc123" {
		t.Errorf("CommitSHA mismatch: got %s", job.CommitSHA)
	}
	if job.Branch != "main" {
		t.Errorf("Branch mismatch: got %s", job.Branch)
	}
	if job.CommitMsg != "Fix bug" {
		t.Errorf("CommitMsg mismatch: got %s", job.CommitMsg)
	}
	if job.CommitAuthor != "Joe" {
		t.Errorf("CommitAuthor mismatch: got %s", job.CommitAuthor)
	}
	if job.Commit.Hash != "abc123" {
		t.Errorf("Commit.Hash mismatch: got %s", job.Commit.Hash)
	}
}

func TestSetGitContextWithTag(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	job.SetGitContext("def456", "", "v1.0.0", "Release v1.0.0", "Joe")

	if job.Tag != "v1.0.0" {
		t.Errorf("Tag mismatch: got %s", job.Tag)
	}
	if job.Branch != "" {
		t.Errorf("Branch should be empty for tag, got %s", job.Branch)
	}
}

func TestGetRef(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	// No ref
	if job.GetRef() != "" {
		t.Error("empty job should return empty ref")
	}

	// Branch ref
	job.Branch = "main"
	if job.GetRef() != "heads/main" {
		t.Errorf("Branch ref mismatch: got %s", job.GetRef())
	}

	// Tag ref takes precedence
	job.Tag = "v2.0.0"
	if job.GetRef() != "tags/v2.0.0" {
		t.Errorf("Tag ref mismatch: got %s", job.GetRef())
	}
}

func TestCanResume(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	if job.CanResume() {
		t.Error("pending job should not be resumable")
	}

	job.Status = StatusRunning
	if !job.CanResume() {
		t.Error("running job should be resumable")
	}

	job.Status = StatusBlocked
	if !job.CanResume() {
		t.Error("blocked job should be resumable")
	}

	job.Status = StatusDone
	if job.CanResume() {
		t.Error("done job should not be resumable")
	}
}

func TestMarkRetryable(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")
	job.Fail(fmt.Errorf("connection refused"))

	job.MarkRetryable()

	if job.Status != StatusRetryable {
		t.Errorf("expected StatusRetryable, got %s", job.Status)
	}
}

func TestMarkNeedsApproval(t *testing.T) {
	job := NewJob("event-1", "test-repo", "approval-gate")

	job.MarkNeedsApproval()

	if job.Status != StatusNeedsApproval {
		t.Errorf("expected StatusNeedsApproval, got %s", job.Status)
	}
}

func TestMarkBlocked(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	job.MarkBlocked("Waiting for dependency job")

	if job.Status != StatusBlocked {
		t.Errorf("expected StatusBlocked, got %s", job.Status)
	}
	if job.ErrorSummary != "Waiting for dependency job" {
		t.Errorf("ErrorSummary mismatch: got %s", job.ErrorSummary)
	}
}

func TestShouldHardStop(t *testing.T) {
	// approval-gate should hard stop on failure
	approvalJob := NewJob("event-1", "test-repo", "approval-gate")
	approvalJob.Fail(fmt.Errorf("rejected"))

	if !approvalJob.ShouldHardStop() {
		t.Error("approval-gate failure should hard stop")
	}

	// deploy should hard stop on failure
	deployJob := NewJob("event-2", "test-repo", "deploy")
	deployJob.Fail(fmt.Errorf("deployment failed"))

	if !deployJob.ShouldHardStop() {
		t.Error("deploy failure should hard stop")
	}

	// normal job should not hard stop
	normalJob := NewJob("event-3", "test-repo", "go-lint")
	normalJob.Fail(fmt.Errorf("lint error"))

	if normalJob.ShouldHardStop() {
		t.Error("go-lint failure should not hard stop")
	}

	// non-failed job should not hard stop
	successJob := NewJob("event-4", "test-repo", "deploy")
	successJob.Complete()

	if successJob.ShouldHardStop() {
		t.Error("completed deploy should not hard stop")
	}
}

func TestCanRetryEdgeCases(t *testing.T) {
	policy := DefaultRetryPolicy()

	// Done job cannot retry
	doneJob := NewJob("event-1", "test-repo", "go-lint")
	doneJob.Complete()
	if doneJob.CanRetry(policy) {
		t.Error("done job cannot retry")
	}

	// Cancelled job cannot retry
	cancelledJob := NewJob("event-2", "test-repo", "go-lint")
	cancelledJob.Cancel("user cancelled")
	if cancelledJob.CanRetry(policy) {
		t.Error("cancelled job cannot retry")
	}

	// Non-retryable error patterns
	notFoundJob := NewJob("event-3", "test-repo", "go-lint")
	notFoundJob.Fail(fmt.Errorf("file not found"))
	if notFoundJob.CanRetry(policy) {
		t.Error("'not found' error should not be retryable")
	}

	invalidJob := NewJob("event-4", "test-repo", "go-lint")
	invalidJob.Fail(fmt.Errorf("invalid configuration"))
	if invalidJob.CanRetry(policy) {
		t.Error("'invalid' error should not be retryable")
	}

	// Retryable error patterns
	timeoutJob := NewJob("event-5", "test-repo", "go-lint")
	timeoutJob.Fail(fmt.Errorf("connection timeout"))
	if !timeoutJob.CanRetry(policy) {
		t.Error("'timeout' error should be retryable")
	}

	tempJob := NewJob("event-6", "test-repo", "go-lint")
	tempJob.Fail(fmt.Errorf("temporary failure"))
	if !tempJob.CanRetry(policy) {
		t.Error("'temporary' error should be retryable")
	}
}

func TestDurationCalculation(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	if job.DurationMs != 0 {
		t.Errorf("new job should have 0 duration, got %d", job.DurationMs)
	}

	job.Start()
	time.Sleep(10 * time.Millisecond)
	job.Complete()

	if job.DurationMs < 1 {
		t.Errorf("completed job should have positive duration, got %d", job.DurationMs)
	}
	if job.StartedAt == nil {
		t.Error("StartedAt should be set after Start()")
	}
	if job.EndedAt == nil {
		t.Error("EndedAt should be set after Complete()")
	}
}

func TestErrorSummaryTruncation(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	// Short error - no truncation
	shortErr := fmt.Errorf("short error")
	job.Fail(shortErr)
	if job.ErrorSummary != "short error" {
		t.Errorf("short ErrorSummary mismatch: got %s", job.ErrorSummary)
	}

	// Long error - truncation
	longMsg := ""
	for i := 0; i < 250; i++ {
		longMsg += "x"
	}
	longErr := fmt.Errorf("%s", longMsg)
	job.Fail(longErr)
	if len(job.ErrorSummary) != 203 { // 200 + "..."
		t.Errorf("ErrorSummary should be 203 chars, got %d", len(job.ErrorSummary))
	}
	if job.ErrorSummary[len(job.ErrorSummary)-3:] != "..." {
		t.Error("ErrorSummary should end with ...")
	}
}

func TestArtifactsInitialized(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	if job.Artifacts == nil {
		t.Error("Artifacts should be initialized to empty slice, not nil")
	}
	if len(job.Artifacts) != 0 {
		t.Errorf("Artifacts should be empty, got %d", len(job.Artifacts))
	}
}

func TestTriggerReasonConstants(t *testing.T) {
	if TriggerGitPush != "git.push" {
		t.Errorf("TriggerGitPush mismatch")
	}
	if TriggerGitTag != "git.tag" {
		t.Errorf("TriggerGitTag mismatch")
	}
	if TriggerManual != "manual" {
		t.Errorf("TriggerManual mismatch")
	}
	if TriggerRetry != "retry" {
		t.Errorf("TriggerRetry mismatch")
	}
	if TriggerWebhook != "webhook" {
		t.Errorf("TriggerWebhook mismatch")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusPending != "pending" {
		t.Errorf("StatusPending mismatch")
	}
	if StatusQueued != "queued" {
		t.Errorf("StatusQueued mismatch")
	}
	if StatusRunning != "running" {
		t.Errorf("StatusRunning mismatch")
	}
	if StatusDone != "done" {
		t.Errorf("StatusDone mismatch")
	}
	if StatusFailed != "failed" {
		t.Errorf("StatusFailed mismatch")
	}
	if StatusCanceled != "cancelled" {
		t.Errorf("StatusCanceled mismatch")
	}
	if StatusRetrying != "retrying" {
		t.Errorf("StatusRetrying mismatch")
	}
	if StatusBlocked != "blocked" {
		t.Errorf("StatusBlocked mismatch")
	}
	if StatusNeedsApproval != "needs_approval" {
		t.Errorf("StatusNeedsApproval mismatch")
	}
	if StatusRetryable != "retryable" {
		t.Errorf("StatusRetryable mismatch")
	}
}

func TestPluginNameCompatibility(t *testing.T) {
	job := NewJob("event-1", "test-repo", "go-lint")

	// Action and PluginName should be the same
	if job.Action != job.PluginName {
		t.Error("Action and PluginName should match")
	}

	// JSON serialization should include both
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to marshal job: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed["action"] != "go-lint" {
		t.Errorf("action field mismatch")
	}
	if parsed["plugin_name"] != "go-lint" {
		t.Errorf("plugin_name field mismatch")
	}
}

// TestIntentFromMessage: the commit-message intent convention.
func TestIntentFromMessage(t *testing.T) {
	cases := map[string]string{
		"feat: add thing task:abc-123":    "abc-123",
		"fix: bug [task: xyz_9]":          "xyz_9",
		"goal:ship-1: implement":          "ship-1",
		"no intent here":                  "",
		"task":                            "",
		"TASK:upper-1 and more":           "upper-1",
		"feat(task-1): scoped convention": "", // scoped style not supported (documented convention is task:<id>)
	}
	for msg, want := range cases {
		if got := IntentFromMessage(msg); got != want {
			t.Errorf("IntentFromMessage(%q) = %q, want %q", msg, got, want)
		}
	}
}
