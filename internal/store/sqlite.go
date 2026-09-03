// Copyright (c) 2025 JoeGlenn1213
// ActionD SQLite Store
// Uses CGO go-sqlite3 driver

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/job"
	_ "modernc.org/sqlite"
)

// ErrDuplicateJob is returned by AddJob when the dispatch dedup index
// rejects a second non-retry job for the same (event_id, plugin_name).
// Callers treat it as "skip quietly", not as a failure.
var ErrDuplicateJob = errors.New("duplicate job: same event+plugin already dispatched")

// SQLiteStore implements Store using SQLite
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new store backed by a file
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	// busy_timeout makes concurrent writes wait instead of failing with
	// SQLITE_BUSY (observed in production: AddJob lost 5 jobs to transient
	// locks, see ASSURANCE Phase B incident 2026-08-21).
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		event_id TEXT,
		repo TEXT,
		action TEXT,
		plugin_name TEXT,
		plugin_version TEXT,
		profile TEXT,
		intent TEXT,
		event_json TEXT,
		status TEXT,
		progress TEXT,
		error TEXT,
		created_at DATETIME,
		started_at DATETIME,
		completed_at DATETIME,
		duration_ms INTEGER,
		model TEXT,
		tokens INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_jobs_repo ON jobs(repo);
	CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	`
	_, err := s.db.Exec(query)
	if err != nil {
		return err
	}

	// Ensure new columns exist (for migrations)
	newColumns := []struct {
		name string
		typ  string
	}{
		{"event_json", "TEXT"},
		{"plugin_version", "TEXT"},
		{"profile", "TEXT"},
		{"intent", "TEXT"},
		{"event_type", "TEXT"},
		{"trigger_reason", "TEXT"},
		{"branch", "TEXT"},
		{"tag", "TEXT"},
		{"commit_sha", "TEXT"},
		{"commit_message", "TEXT"},
		{"commit_author", "TEXT"},
		{"error_summary", "TEXT"},
		{"exit_code", "INTEGER"},
		{"artifacts", "TEXT"},
		{"raw_log_path", "TEXT"},
		{"result_json", "TEXT"},
		{"retry_count", "INTEGER"},
		{"retry_of", "TEXT"},
		{"original_run", "TEXT"},
	}

	for _, col := range newColumns {
		if err := s.ensureColumn("jobs", col.name, col.typ); err != nil {
			return err
		}
	}

	// Dispatch idempotency (ASSURANCE §7 前置债): dedupe pre-existing
	// duplicate (event_id, plugin_name) rows — left over from the
	// double-daemon incident — keeping the newest, then enforce uniqueness
	// for future non-retry dispatches. Retried jobs carry retry_of and are
	// excluded from the index, so retries are unaffected.
	if err := s.dedupeExistingDispatchDuplicates(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dispatch_dedup
		ON jobs(event_id, plugin_name)
		WHERE event_id <> '' AND (retry_of IS NULL OR retry_of = '')
	`); err != nil {
		return fmt.Errorf("create dispatch dedup index: %w", err)
	}

	return nil
}

// dedupeExistingDispatchDuplicates removes older duplicate dispatch rows,
// keeping the newest per (event_id, plugin_name). Without this cleanup the
// partial unique index cannot be created on databases that already contain
// duplicates (observed 2026-08-22: one push dispatched twice by two daemon
// instances).
func (s *SQLiteStore) dedupeExistingDispatchDuplicates() error {
	_, err := s.db.Exec(`
		DELETE FROM jobs WHERE id IN (
			SELECT j1.id FROM jobs j1
			WHERE (j1.retry_of IS NULL OR j1.retry_of = '') AND j1.event_id <> ''
			AND EXISTS (
				SELECT 1 FROM jobs j2
				WHERE j2.event_id = j1.event_id
				  AND j2.plugin_name = j1.plugin_name
				  AND (j2.retry_of IS NULL OR j2.retry_of = '')
				  AND j2.created_at > j1.created_at
			)
		)
	`)
	if err != nil {
		return fmt.Errorf("dedupe existing dispatch duplicates: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ensureColumn(table, column, columnType string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer closeRows(rows)

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	return err
}

func (s *SQLiteStore) AddJob(j *job.ActionJob) error {
	query := `
	INSERT INTO jobs (
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status,
		progress, created_at, started_at, event_type, trigger_reason,
		branch, tag, commit_sha, commit_message, commit_author,
		artifacts, retry_count, retry_of, original_run
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var artifactsJSON string
	if len(j.Artifacts) > 0 {
		data, _ := json.Marshal(j.Artifacts)
		artifactsJSON = string(data)
	}

	_, err := s.db.Exec(query,
		j.ID, j.EventID, j.Repo, j.Action, j.PluginName, j.PluginVersion, j.Profile, j.Intent, j.EventJSON, j.Status,
		j.Progress, j.CreatedAt, j.StartedAt, j.EventType, j.TriggerReason,
		j.Branch, j.Tag, j.CommitSHA, j.CommitMsg, j.CommitAuthor,
		artifactsJSON, j.RetryCount, j.RetryOf, j.OriginalRun,
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateJob
	}
	return err
}

func (s *SQLiteStore) UpdateJob(j *job.ActionJob) error {
	query := `UPDATE jobs SET status = ?, progress = ?, started_at = ?, event_json = COALESCE(NULLIF(?, ''), event_json) WHERE id = ?`
	_, err := s.db.Exec(query, j.Status, j.Progress, j.StartedAt, j.EventJSON, j.ID)
	return err
}

func (s *SQLiteStore) FinishJob(j *job.ActionJob) error {
	query := `
	UPDATE jobs
	SET status = ?,
		progress = ?,
		error = ?,
		error_summary = ?,
		exit_code = ?,
		completed_at = ?,
		duration_ms = ?,
		tokens = ?,
		artifacts = ?,
		raw_log_path = ?,
		result_json = ?
	WHERE id = ?
	`
	progress := "Done"
	switch j.Status {
	case job.StatusFailed:
		progress = "Failed"
	case job.StatusCanceled:
		progress = "Cancelled"
	}
	if j.Progress != "" {
		progress = j.Progress
	}

	var artifactsJSON string
	if len(j.Artifacts) > 0 {
		data, _ := json.Marshal(j.Artifacts)
		artifactsJSON = string(data)
	}

	var resultJSON string
	if j.Result != nil {
		data, _ := json.Marshal(j.Result)
		resultJSON = string(data)
	}

	_, err := s.db.Exec(query,
		string(j.Status),
		progress,
		j.Error,
		j.ErrorSummary,
		j.ExitCode,
		j.CompletedAt,
		j.DurationMs,
		j.Tokens,
		artifactsJSON,
		j.RawLogPath,
		resultJSON,
		j.ID,
	)
	return err
}

func (s *SQLiteStore) GetJob(id string) (*job.ActionJob, error) {
	query := `SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs WHERE id = ?`
	row := s.db.QueryRow(query, id)
	return scanJob(row)
}

func (s *SQLiteStore) ListJobs(limit int) ([]*job.ActionJob, error) {
	query := `
	SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	jobs := []*job.ActionJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListJobsByRepo lists jobs for a specific repository
func (s *SQLiteStore) ListJobsByRepo(repo string, limit int) ([]*job.ActionJob, error) {
	query := `
	SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs
		WHERE repo = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, repo, limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	jobs := []*job.ActionJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListJobsByEventID lists all jobs triggered by a specific event
func (s *SQLiteStore) ListJobsByEventID(eventID string) ([]*job.ActionJob, error) {
	query := `
	SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs
		WHERE event_id = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.Query(query, eventID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	jobs := []*job.ActionJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListJobsByStatus lists jobs with a specific status
func (s *SQLiteStore) ListJobsByStatus(status job.Status, limit int) ([]*job.ActionJob, error) {
	query := `
	SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs
		WHERE status = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, status, limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	jobs := []*job.ActionJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Scannable allows scanning from both Row and Rows
type Scannable interface {
	Scan(dest ...interface{}) error
}

func scanJob(s Scannable) (*job.ActionJob, error) {
	j := &job.ActionJob{}
	var created, started, completed sql.NullTime
	var durMs sql.NullInt64
	var model, pluginName, pluginVersion, profile, intent, errMsg, progress, eventJSON sql.NullString
	var tokens sql.NullInt64
	// New fields
	var eventType, triggerReason, branch, tag, commitSHA, commitMsg, commitAuthor sql.NullString
	var errorSummary, rawLogPath, artifactsJSON, resultJSON sql.NullString
	var exitCode sql.NullInt64
	var retryCount sql.NullInt64
	var retryOf, originalRun sql.NullString

	err := s.Scan(
		&j.ID, &j.EventID, &j.Repo, &j.Action, &pluginName, &pluginVersion, &profile, &intent, &eventJSON, &j.Status, &progress, &errMsg,
		&created, &started, &completed, &durMs, &model, &tokens,
		&eventType, &triggerReason, &branch, &tag, &commitSHA, &commitMsg, &commitAuthor,
		&errorSummary, &exitCode, &artifactsJSON, &rawLogPath, &resultJSON,
		&retryCount, &retryOf, &originalRun,
	)
	if err != nil {
		return nil, err
	}

	j.PluginName = pluginName.String
	j.PluginVersion = pluginVersion.String
	j.Profile = profile.String
	j.Intent = intent.String
	j.EventJSON = eventJSON.String
	j.Progress = progress.String
	j.Error = errMsg.String
	j.Model = model.String
	j.Tokens = int(tokens.Int64)

	// New fields
	j.EventType = eventType.String
	j.TriggerReason = job.TriggerReason(triggerReason.String)
	j.Branch = branch.String
	j.Tag = tag.String
	j.CommitSHA = commitSHA.String
	j.CommitMsg = commitMsg.String
	j.CommitAuthor = commitAuthor.String
	j.Commit = job.CommitInfo{
		Hash:    commitSHA.String,
		Message: commitMsg.String,
		Author:  commitAuthor.String,
	}
	j.ErrorSummary = errorSummary.String
	j.ExitCode = int(exitCode.Int64)
	j.RawLogPath = rawLogPath.String
	j.RetryCount = int(retryCount.Int64)
	j.RetryOf = retryOf.String
	j.OriginalRun = originalRun.String

	// Parse artifacts JSON
	if artifactsJSON.Valid && artifactsJSON.String != "" {
		_ = json.Unmarshal([]byte(artifactsJSON.String), &j.Artifacts)
	}
	if j.Artifacts == nil {
		j.Artifacts = []string{}
	}

	// Parse result JSON
	if resultJSON.Valid && resultJSON.String != "" {
		j.Result = &job.ActionResult{}
		_ = json.Unmarshal([]byte(resultJSON.String), j.Result)
	}

	if created.Valid {
		j.CreatedAt = created.Time
	}
	if started.Valid {
		t := started.Time
		j.StartedAt = &t
	}
	if completed.Valid {
		j.CompletedAt = completed.Time
		// Also update EndedAt for compatibility
		t := completed.Time
		j.EndedAt = &t
	}
	if durMs.Valid {
		j.DurationMs = durMs.Int64
		// Derived duration struct
		j.Duration = time.Duration(j.DurationMs) * time.Millisecond
	}

	return j, nil
}

// ClearAll deletes all jobs from the database
func (s *SQLiteStore) ClearAll() (int64, error) {
	result, err := s.db.Exec("DELETE FROM jobs")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteJob removes a single job from the database.
func (s *SQLiteStore) DeleteJob(id string) error {
	_, err := s.db.Exec("DELETE FROM jobs WHERE id = ?", id)
	return err
}

// DeleteJobsBefore removes terminal jobs (done/failed/cancelled) created
// before the cutoff and returns how many were deleted. Non-terminal jobs
// are left alone so pending/running work is never silently dropped.
func (s *SQLiteStore) DeleteJobsBefore(cutoff time.Time) (int, error) {
	res, err := s.db.Exec(`
		DELETE FROM jobs
		WHERE status IN ('done', 'failed', 'cancelled')
		  AND created_at < ?
	`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetDB returns the underlying database connection for advanced queries
func (s *SQLiteStore) GetDB() *sql.DB {
	return s.db
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		fmt.Printf("failed to close rows: %v\n", err)
	}
}

// ListRecoverableJobs returns non-terminal jobs created at/after `since`,
// for re-queueing after a restart (ASSURANCE §7 前置债: pending 恢复).
func (s *SQLiteStore) ListRecoverableJobs(since time.Time) ([]*job.ActionJob, error) {
	query := `
	SELECT
		id, event_id, repo, action, plugin_name, plugin_version, profile, intent, event_json, status, progress, error,
		created_at, started_at, completed_at, duration_ms, model, tokens,
		event_type, trigger_reason, branch, tag, commit_sha, commit_message, commit_author,
		error_summary, exit_code, artifacts, raw_log_path, result_json,
		retry_count, retry_of, original_run
		FROM jobs
		WHERE status NOT IN ('done', 'failed', 'cancelled')
		  AND created_at >= ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.Query(query, since)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var res []*job.ActionJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, j)
	}
	return res, rows.Err()
}

// isTerminalStatus reports whether a job status is final.
func isTerminalStatus(status job.Status) bool {
	switch status {
	case job.StatusDone, job.StatusFailed, job.StatusCanceled:
		return true
	default:
		return false
	}
}

// AbandonStaleJobs marks non-terminal jobs older than the cutoff as
// cancelled with an honest summary — they predate the recovery window and
// would otherwise sit pending forever in the UI (ASSURANCE §7: 僵尸任务).
func (s *SQLiteStore) AbandonStaleJobs(cutoff time.Time) (int, error) {
	res, err := s.db.Exec(`
		UPDATE jobs
		SET status = 'cancelled',
		    error_summary = 'abandoned: stale before recovery window',
		    completed_at = ?
		WHERE status NOT IN ('done', 'failed', 'cancelled')
		  AND created_at < ?
	`, time.Now(), cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
