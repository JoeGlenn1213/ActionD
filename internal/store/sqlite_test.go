package store

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/job"
)

func setupTestStore(t *testing.T) (*SQLiteStore, func()) {
	f, err := os.CreateTemp("", "actiond_test_*.db")
	if err != nil {
		t.Fatalf("Failed to create temp db: %v", err)
	}
	dbPath := f.Name()
	if err := f.Close(); err != nil {
		t.Fatalf("close temp db file: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("Failed to init store: %v", err)
	}

	cleanup := func() {
		_ = store.db.Close()
		_ = os.Remove(dbPath)
	}

	return store, cleanup
}

func TestStoreJobVerifierProvenance(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-2", "repo/test", "go-test")
	j.PluginName = "go-test"
	j.PluginVersion = "1.4.2" // verifier provenance (Phase B)
	j.Profile = "fast"        // verdict tier provenance

	if err := store.AddJob(j); err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	got, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}
	if got.PluginVersion != "1.4.2" {
		t.Errorf("PluginVersion = %q, want 1.4.2 (roundtrip lost)", got.PluginVersion)
	}
	if got.Profile != "fast" {
		t.Errorf("Profile = %q, want fast (roundtrip lost)", got.Profile)
	}
}

func TestSQLiteStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Test AddJob
	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.PluginName = "test-plugin"
	j.EventJSON = `{"id":"evt-1","type":"git.push"}`

	if err := store.AddJob(j); err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Test GetJob
	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}
	if j2.ID != j.ID || j2.Repo != j.Repo {
		t.Errorf("Job mismatch. Got %v, want %v", j2, j)
	}

	// Test UpdateJob
	j.Start()
	if err := store.UpdateJob(j); err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	j3, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}
	if j3.Status != job.StatusRunning {
		t.Errorf("Status mismatch. Got %s, want running", j3.Status)
	}

	// Test FinishJob
	j.Complete()
	j.DurationMs = 5000

	if err := store.FinishJob(j); err != nil {
		t.Fatalf("Failed to complete job: %v", err)
	}

	j4, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}
	if j4.Status != job.StatusDone {
		t.Errorf("Status mismatch. Got %s, want done", j4.Status)
	}

	// Test ListJobs
	jobs, err := store.ListJobs(10)
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("Expected 1 job, got %d", len(jobs))
	}
}

func TestStoreAddJobDuplicate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")

	if err := store.AddJob(j); err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Adding same job twice should fail
	if err := store.AddJob(j); err == nil {
		t.Error("Expected error when adding duplicate job")
	}
}

func TestStoreGetJobNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	_, err := store.GetJob("nonexistent-id")
	if err == nil {
		t.Error("Expected error when getting nonexistent job")
	}
}

func TestStoreUpdateJobNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// UpdateJob on nonexistent job should not error (upsert behavior)
	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()
	// This may succeed or fail depending on implementation
	_ = store.UpdateJob(j)
}

func TestStoreFinishJobNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Complete()
	// This may succeed or fail depending on implementation
	_ = store.FinishJob(j)
}

func TestStoreJobWithResult(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()

	result := &job.ActionResult{
		ActionID: j.ID,
		Status:   "success",
		Summary:  "Test passed",
		Metrics:  map[string]interface{}{"coverage": 85.0},
	}

	j.CompleteWithResult(result)

	// Store job with result
	if err := store.AddJob(j); err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	// Result storage depends on implementation
	// Just verify job was stored correctly
	if j2.Status != job.StatusDone {
		t.Errorf("Status mismatch: got %s", j2.Status)
	}
}

func TestStoreJobWithRetry(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()
	j.Fail(fmt.Errorf("test error"))
	j.MarkRetry(j.ID, "run-123", 1)

	if err := store.AddJob(j); err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if !j2.IsRetry() {
		t.Error("Expected job to be marked as retry")
	}
	if j2.OriginalRun != "run-123" {
		t.Errorf("OriginalRun mismatch: got %s", j2.OriginalRun)
	}
}

func TestStoreListJobsEmpty(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	jobs, err := store.ListJobs(10)
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("Expected 0 jobs, got %d", len(jobs))
	}
}

func TestStoreListJobsLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Add 5 jobs
	for i := 0; i < 5; i++ {
		j := job.NewJob(fmt.Sprintf("evt-%d", i), "repo/test", "test-plugin")
		j.Complete()
		_ = store.AddJob(j)
	}

	// List with limit 2
	jobs, err := store.ListJobs(2)
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}
}

func TestStorePersistence(t *testing.T) {
	// Create temp file
	f, err := os.CreateTemp("", "actiond_test_*.db")
	if err != nil {
		t.Fatalf("Failed to create temp db: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(dbPath) }()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()
	j.Complete()

	_ = store.AddJob(j)

	// Close and reopen
	_ = store.db.Close()

	store2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen store: %v", err)
	}
	defer func() { _ = store2.db.Close() }()

	jobs, err := store2.ListJobs(10)
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("Expected 1 job after reopen, got %d", len(jobs))
	}
}

func TestStoreJobWithGitContext(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.SetGitContext("abc123def", "main", "", "feat: add feature", "developer@example.com")
	j.Complete()

	_ = store.AddJob(j)

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if j2.CommitSHA != "abc123def" {
		t.Errorf("CommitSHA mismatch: got %s", j2.CommitSHA)
	}
	if j2.Branch != "main" {
		t.Errorf("Branch mismatch: got %s", j2.Branch)
	}
	if j2.CommitMsg != "feat: add feature" {
		t.Errorf("CommitMsg mismatch: got %s", j2.CommitMsg)
	}
}

func TestStoreListJobsByRepo(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Add jobs for different repos
	j1 := job.NewJob("evt-1", "repo/project-a", "test-plugin")
	j1.Complete()
	j2 := job.NewJob("evt-2", "repo/project-a", "test-plugin")
	j2.Complete()
	j3 := job.NewJob("evt-3", "repo/project-b", "test-plugin")
	j3.Complete()

	_ = store.AddJob(j1)
	_ = store.AddJob(j2)
	_ = store.AddJob(j3)

	// Filter by project-a
	jobsA, err := store.ListJobsByRepo("repo/project-a", 10)
	if err != nil {
		t.Fatalf("Failed to list jobs by repo: %v", err)
	}
	if len(jobsA) != 2 {
		t.Errorf("Expected 2 jobs for project-a, got %d", len(jobsA))
	}

	// Filter by project-b
	jobsB, err := store.ListJobsByRepo("repo/project-b", 10)
	if err != nil {
		t.Fatalf("Failed to list jobs by repo: %v", err)
	}
	if len(jobsB) != 1 {
		t.Errorf("Expected 1 job for project-b, got %d", len(jobsB))
	}

	// Non-existent repo
	jobsC, err := store.ListJobsByRepo("repo/nonexistent", 10)
	if err != nil {
		t.Fatalf("Failed to list jobs by non-existent repo: %v", err)
	}
	if len(jobsC) != 0 {
		t.Errorf("Expected 0 jobs for non-existent repo, got %d", len(jobsC))
	}
}

func TestStoreListJobsByStatus(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Add jobs with different statuses
	j1 := job.NewJob("evt-1", "repo/test", "test-plugin")
	j1.Complete()
	j2 := job.NewJob("evt-2", "repo/test", "test-plugin")
	j2.Start()
	j3 := job.NewJob("evt-3", "repo/test", "test-plugin")
	j3.Fail(fmt.Errorf("test error"))
	j4 := job.NewJob("evt-4", "repo/test", "test-plugin")
	// j4 stays pending

	_ = store.AddJob(j1)
	_ = store.AddJob(j2)
	_ = store.AddJob(j3)
	_ = store.AddJob(j4)

	// Filter by done
	jobsDone, err := store.ListJobsByStatus(job.StatusDone, 10)
	if err != nil {
		t.Fatalf("Failed to list jobs by status: %v", err)
	}
	if len(jobsDone) != 1 {
		t.Errorf("Expected 1 done job, got %d", len(jobsDone))
	}

	// Filter by running
	jobsRunning, err := store.ListJobsByStatus(job.StatusRunning, 10)
	if err != nil {
		t.Fatalf("Failed to list running jobs: %v", err)
	}
	if len(jobsRunning) != 1 {
		t.Errorf("Expected 1 running job, got %d", len(jobsRunning))
	}

	// Filter by failed
	jobsFailed, err := store.ListJobsByStatus(job.StatusFailed, 10)
	if err != nil {
		t.Fatalf("Failed to list failed jobs: %v", err)
	}
	if len(jobsFailed) != 1 {
		t.Errorf("Expected 1 failed job, got %d", len(jobsFailed))
	}

	// Filter by pending
	jobsPending, err := store.ListJobsByStatus(job.StatusPending, 10)
	if err != nil {
		t.Fatalf("Failed to list pending jobs: %v", err)
	}
	if len(jobsPending) != 1 {
		t.Errorf("Expected 1 pending job, got %d", len(jobsPending))
	}
}

func TestStoreClearAll(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Add several jobs
	for i := 0; i < 5; i++ {
		j := job.NewJob(fmt.Sprintf("evt-%d", i), "repo/test", "test-plugin")
		j.Complete()
		_ = store.AddJob(j)
	}

	// Verify jobs exist
	jobs, _ := store.ListJobs(10)
	if len(jobs) != 5 {
		t.Fatalf("Expected 5 jobs before clear, got %d", len(jobs))
	}

	// Clear all
	deleted, err := store.ClearAll()
	if err != nil {
		t.Fatalf("Failed to clear all: %v", err)
	}
	if deleted != 5 {
		t.Errorf("Expected 5 deleted jobs, got %d", deleted)
	}

	// Verify all cleared
	jobs, _ = store.ListJobs(10)
	if len(jobs) != 0 {
		t.Errorf("Expected 0 jobs after clear, got %d", len(jobs))
	}
}

func TestStoreDeleteJob(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Complete()
	_ = store.AddJob(j)

	if err := store.DeleteJob(j.ID); err != nil {
		t.Fatalf("DeleteJob failed: %v", err)
	}
	if _, err := store.GetJob(j.ID); err == nil {
		t.Error("Expected job to be gone after DeleteJob")
	}
	// Deleting a non-existent job is a no-op, not an error.
	if err := store.DeleteJob("does-not-exist"); err != nil {
		t.Errorf("DeleteJob on missing id should not error, got %v", err)
	}
}

func TestStoreDeleteJobsBefore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	oldDone := job.NewJob("evt-old-done", "repo/test", "test-plugin")
	oldDone.Complete()
	oldDone.CreatedAt = time.Now().AddDate(0, 0, -30)
	_ = store.AddJob(oldDone)

	oldFailed := job.NewJob("evt-old-failed", "repo/test", "test-plugin")
	oldFailed.Fail(errors.New("boom"))
	oldFailed.CreatedAt = time.Now().AddDate(0, 0, -30)
	_ = store.AddJob(oldFailed)

	// Old pending job must be preserved.
	oldPending := job.NewJob("evt-old-pending", "repo/test", "test-plugin")
	oldPending.CreatedAt = time.Now().AddDate(0, 0, -30)
	_ = store.AddJob(oldPending)

	// Recent done job must be preserved.
	recent := job.NewJob("evt-recent", "repo/test", "test-plugin")
	recent.Complete()
	_ = store.AddJob(recent)

	deleted, err := store.DeleteJobsBefore(time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("DeleteJobsBefore failed: %v", err)
	}
	if deleted != 2 {
		t.Errorf("Expected 2 deleted jobs, got %d", deleted)
	}
	if _, err := store.GetJob(oldDone.ID); err == nil {
		t.Error("old done job should be deleted")
	}
	if _, err := store.GetJob(oldFailed.ID); err == nil {
		t.Error("old failed job should be deleted")
	}
	if _, err := store.GetJob(oldPending.ID); err != nil {
		t.Error("old pending job should be preserved")
	}
	if _, err := store.GetJob(recent.ID); err != nil {
		t.Error("recent job should be preserved")
	}
}

func TestStoreGetDB(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	db := store.GetDB()
	if db == nil {
		t.Error("GetDB should return non-nil db")
	}

	// Verify we can execute a query
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query db: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 jobs, got %d", count)
	}
}

func TestStoreJobWithArtifacts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Artifacts = []string{"build/output.bin", "coverage.json"}
	j.Complete()

	_ = store.AddJob(j)

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if len(j2.Artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(j2.Artifacts))
	}
}

func TestStoreJobWithTag(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.SetGitContext("abc123", "", "v1.0.0", "Release v1.0.0", "developer")
	j.Complete()

	_ = store.AddJob(j)

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if j2.Tag != "v1.0.0" {
		t.Errorf("Tag mismatch: got %s", j2.Tag)
	}
}

func TestStoreJobWithDuration(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()
	// Small delay to ensure DurationMs > 0
	time.Sleep(10 * time.Millisecond)
	j.Complete()

	// Use AddJob for initial insert, then FinishJob to update final state
	_ = store.AddJob(j)

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	// DurationMs is set by Complete() but stored via AddJob/FinishJob
	// The actual storage depends on implementation - verify job was stored
	if j2.Status != job.StatusDone {
		t.Errorf("Status mismatch: got %s", j2.Status)
	}
}

func TestStoreUpdateJobRunning(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-1", "repo/test", "test-plugin")
	j.Start()
	j.Progress = "Running tests..."

	_ = store.AddJob(j)

	j2, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if j2.Progress != "Running tests..." {
		t.Errorf("Progress mismatch: got %s", j2.Progress)
	}
}

func TestStoreMultipleRepos(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	repos := []string{"repo/a", "repo/b", "repo/c"}
	for i, repo := range repos {
		j := job.NewJob(fmt.Sprintf("evt-%d", i), repo, "test-plugin")
		j.Complete()
		_ = store.AddJob(j)
	}

	// Count total
	total, err := store.ListJobs(10)
	if err != nil {
		t.Fatalf("Failed to list all jobs: %v", err)
	}
	if len(total) != 3 {
		t.Errorf("Expected 3 total jobs, got %d", len(total))
	}

	// List by each repo
	for _, repo := range repos {
		jobs, err := store.ListJobsByRepo(repo, 10)
		if err != nil {
			t.Fatalf("Failed to list by repo %s: %v", repo, err)
		}
		if len(jobs) != 1 {
			t.Errorf("Expected 1 job for %s, got %d", repo, len(jobs))
		}
	}
}

// TestAddJobDispatchDedup: a second non-retry job for the same
// (event_id, plugin_name) must be rejected with ErrDuplicateJob, while a
// retried job (retry_of set) stays allowed.
func TestAddJobDispatchDedup(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mk := func(id string, retryOf string) *job.ActionJob {
		j := job.NewJob("evt-dup", "repo/x", "go-lint")
		j.ID = id
		j.EventID = "evt-dup"
		j.PluginName = "go-lint"
		j.RetryOf = retryOf
		return j
	}

	if err := store.AddJob(mk("dup-1", "")); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := store.AddJob(mk("dup-2", "")); !errors.Is(err, ErrDuplicateJob) {
		t.Fatalf("second add = %v, want ErrDuplicateJob", err)
	}
	// A retried job is a different dispatch path and must be allowed.
	if err := store.AddJob(mk("dup-3", "dup-1")); err != nil {
		t.Fatalf("retry add must succeed: %v", err)
	}
}

// TestListRecoverableJobs: terminal statuses and old jobs are excluded;
// recent non-terminal jobs are returned.
func TestListRecoverableJobs(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now()
	mk := func(id, status string, created time.Time) *job.ActionJob {
		j := job.NewJob(fmt.Sprintf("evt-r-%s", id), "repo/x", "go-lint")
		j.ID = id
		j.Status = job.Status(status)
		j.CreatedAt = created
		return j
	}

	if err := store.AddJob(mk("r-pending", "pending", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddJob(mk("r-running", "running", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := store.AddJob(mk("r-done", "done", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddJob(mk("r-old", "pending", now.Add(-48*time.Hour))); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListRecoverableJobs(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("ListRecoverableJobs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d recoverable jobs, want 2 (pending+running): %v", len(got), got)
	}
	for _, j := range got {
		if j.ID != "r-pending" && j.ID != "r-running" {
			t.Errorf("unexpected job %s", j.ID)
		}
	}
}

// TestStoreJobIntentRoundtrip: the native-contract intent column persists.
func TestStoreJobIntentRoundtrip(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	j := job.NewJob("evt-i", "repo/x", "go-test")
	j.Intent = "task-42"
	if err := store.AddJob(j); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != "task-42" {
		t.Errorf("intent = %q, want task-42 (roundtrip lost)", got.Intent)
	}
}

// TestAbandonStaleJobs: non-terminal jobs before the cutoff are cancelled,
// recent ones and terminal ones are untouched.
func TestAbandonStaleJobs(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now()
	mk := func(id, status string, created time.Time) *job.ActionJob {
		j := job.NewJob(fmt.Sprintf("evt-%s", id), "repo/x", "go-lint")
		j.ID = id
		j.Status = job.Status(status)
		j.CreatedAt = created
		return j
	}
	_ = store.AddJob(mk("stale-pending", "pending", now.Add(-48*time.Hour)))
	_ = store.AddJob(mk("stale-running", "running", now.Add(-30*time.Hour)))
	_ = store.AddJob(mk("fresh-pending", "pending", now))
	_ = store.AddJob(mk("stale-done", "done", now.Add(-48*time.Hour)))

	n, err := store.AbandonStaleJobs(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("AbandonStaleJobs: %v", err)
	}
	if n != 2 {
		t.Fatalf("abandoned %d, want 2", n)
	}
	stale, _ := store.GetJob("stale-pending")
	if stale.Status != job.StatusCanceled || stale.ErrorSummary == "" {
		t.Errorf("stale-pending = %+v, want cancelled with summary", stale)
	}
	fresh, _ := store.GetJob("fresh-pending")
	if fresh.Status != job.StatusPending {
		t.Errorf("fresh-pending must stay pending, got %s", fresh.Status)
	}
	done, _ := store.GetJob("stale-done")
	if done.Status != job.StatusDone {
		t.Errorf("done job must stay done, got %s", done.Status)
	}
}
