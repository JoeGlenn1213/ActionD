package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/store"
)

// REST API unit tests for internal/server. Handlers are methods on *Server
// with the stdlib (http.ResponseWriter, *http.Request) signature, so each is
// driven directly via httptest.NewRecorder — no daemon, no network. The store
// is the in-memory implementation (store.NewMemoryStore) and config writes are
// redirected to a temp file via ACTIOND_CONFIG_PATH so tests never touch
// ~/.localgithub. House style: stdlib only, same package, one-func-per-case.

// newTestServer returns a Server backed by an in-memory store and an isolated
// config path. Call before seeding the store.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("ACTIOND_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	return New("", store.NewMemoryStore(), nil, "")
}

func assertStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Errorf("status = %d, want %d (body: %s)", rr.Code, want, rr.Body.String())
	}
}

func assertBodyContains(t *testing.T, rr *httptest.ResponseRecorder, sub string) {
	t.Helper()
	if !strings.Contains(rr.Body.String(), sub) {
		t.Errorf("body %q does not contain %q", rr.Body.String(), sub)
	}
}

// seedJob creates a job, optionally overrides its status, adds it to the
// server's store, and returns it.
func seedJob(t *testing.T, s *Server, status job.Status) *job.ActionJob {
	t.Helper()
	j := job.NewJob("evt-1", "demo-repo", "go-lint")
	if status != "" {
		j.Status = status
	}
	if err := s.store.AddJob(j); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	return j
}

// ---------------------------------------------------------------------------
// GET /api/actions
// ---------------------------------------------------------------------------

func TestHandleActionsList(t *testing.T) {
	s := newTestServer(t)
	seedJob(t, s, job.StatusRunning)
	j2 := seedJob(t, s, job.StatusDone)

	rr := httptest.NewRecorder()
	s.handleActions(rr, httptest.NewRequest(http.MethodGet, "/api/actions", nil))
	assertStatus(t, rr, http.StatusOK)

	var got []*job.ActionJob
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, rr.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("len(actions) = %d, want 2", len(got))
	}
	if got[1].ID != j2.ID && got[0].ID != j2.ID {
		t.Errorf("job %s not in response", j2.ID)
	}
}

func TestHandleActionsMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleActions(rr, httptest.NewRequest(http.MethodPut, "/api/actions", nil))
	assertStatus(t, rr, http.StatusMethodNotAllowed)
}

// ---------------------------------------------------------------------------
// GET /api/actions/{id}  (exercises handleActionRoute path dispatch)
// ---------------------------------------------------------------------------

func TestHandleJobDetails(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusRunning)

	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodGet, "/api/actions/"+j.ID, nil))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, j.ID)
}

func TestHandleJobDetailsNotFound(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodGet, "/api/actions/does-not-exist", nil))
	assertStatus(t, rr, http.StatusNotFound)
}

func TestHandleActionRouteUnknownSubresource(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusRunning)
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodGet, "/api/actions/"+j.ID+"/bogus", nil))
	assertStatus(t, rr, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// POST /api/actions/{id}/cancel
// ---------------------------------------------------------------------------

func TestHandleActionCancelNilCallback(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusRunning) // running -> cancellable, but no callback wired
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/cancel", nil))
	assertStatus(t, rr, http.StatusServiceUnavailable)
}

func TestHandleActionCancelSuccess(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusRunning)
	var cancelled string
	s.SetCancelFunc(func(id string) bool { cancelled = id; return true })

	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/cancel", nil))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, "Job cancelled")
	if cancelled != j.ID {
		t.Errorf("cancelFunc called with %q, want %q", cancelled, j.ID)
	}
}

func TestHandleActionCancelConflict(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusRunning)
	s.SetCancelFunc(func(string) bool { return false }) // worker refuses
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/cancel", nil))
	assertStatus(t, rr, http.StatusConflict)
}

func TestHandleActionCancelTerminalStatus(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusDone) // terminal -> rejected with structured error
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/cancel", nil))
	assertStatus(t, rr, http.StatusOK) // 200 with status:error in body
	assertBodyContains(t, rr, "Cannot cancel job with status: done")
}

func TestHandleActionCancelNotFound(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/missing/cancel", nil))
	assertStatus(t, rr, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// POST /api/actions/{id}/retry
// ---------------------------------------------------------------------------

func TestHandleActionRetryNilCallback(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusFailed)
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/retry", nil))
	assertStatus(t, rr, http.StatusServiceUnavailable)
}

func TestHandleActionRetrySuccess(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusFailed)
	s.SetRetryFunc(func(id string) (string, error) { return "new-job-id", nil })

	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/retry", nil))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, "new-job-id")
	assertBodyContains(t, rr, "queued for retry")
}

func TestHandleActionRetryWrongStatus(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusDone) // only failed/cancelled can retry
	rr := httptest.NewRecorder()
	s.handleActionRoute(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/retry", nil))
	assertStatus(t, rr, http.StatusConflict)
	assertBodyContains(t, rr, "Only failed or cancelled")
}

// ---------------------------------------------------------------------------
// GET /api/plugins  +  POST /api/plugins/reload
// ---------------------------------------------------------------------------

func TestHandlePluginsListEmpty(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handlePlugins(rr, httptest.NewRequest(http.MethodGet, "/api/plugins", nil))
	assertStatus(t, rr, http.StatusOK)
	if trimmed := strings.TrimSpace(rr.Body.String()); trimmed != "[]" {
		t.Errorf("empty plugins body = %q, want `[]`", trimmed)
	}
}

func TestHandlePluginsReloadNilCallback(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handlePluginsReload(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/reload", nil))
	assertStatus(t, rr, http.StatusServiceUnavailable)
}

func TestHandlePluginsReloadSuccess(t *testing.T) {
	s := newTestServer(t)
	s.SetReloadFunc(func() []plugin.Plugin { return nil }) // no plugins, but callback wired
	rr := httptest.NewRecorder()
	s.handlePluginsReload(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/reload", nil))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, "Plugins reloaded successfully")
}

// ---------------------------------------------------------------------------
// GET/POST /api/profile
// ---------------------------------------------------------------------------

func TestHandleProfileGet(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleProfile(rr, httptest.NewRequest(http.MethodGet, "/api/profile", nil))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, "profile")
}

func TestHandleProfileSet(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleProfile(rr, httptest.NewRequest(http.MethodPost, "/api/profile",
		strings.NewReader(`{"profile":"fast"}`)))
	assertStatus(t, rr, http.StatusOK)
	assertBodyContains(t, rr, `"profile":"fast"`)
	assertBodyContains(t, rr, "updated")
	// GET reflects the persisted value
	rr2 := httptest.NewRecorder()
	s.handleProfile(rr2, httptest.NewRequest(http.MethodGet, "/api/profile", nil))
	assertBodyContains(t, rr2, `"profile":"fast"`)
}

func TestHandleProfileSetInvalid(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleProfile(rr, httptest.NewRequest(http.MethodPost, "/api/profile",
		strings.NewReader(`{"profile":"bogus"}`)))
	assertStatus(t, rr, http.StatusBadRequest)
	assertBodyContains(t, rr, "invalid profile")
}

func TestHandleProfileSetEmpty(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleProfile(rr, httptest.NewRequest(http.MethodPost, "/api/profile",
		strings.NewReader(`{"profile":""}`)))
	assertStatus(t, rr, http.StatusBadRequest)
}

func TestHandleProfileMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleProfile(rr, httptest.NewRequest(http.MethodDelete, "/api/profile", nil))
	assertStatus(t, rr, http.StatusMethodNotAllowed)
}

// ---------------------------------------------------------------------------
// GET /api/actions/{id}/diagnose
// ---------------------------------------------------------------------------

func TestHandleActionDiagnoseHappy(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusFailed)
	j.Error = "main.go:10:2: undefined: missingFunc\nbuild failed"
	j.ErrorSummary = "Go build failed"
	// FinishJob persists Error/ErrorSummary on both MemoryStore and
	// SQLiteStore (UpdateJob only persists status/progress/started_at).
	if err := s.store.FinishJob(j); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}

	rr := httptest.NewRecorder()
	s.handleActionDiagnose(rr, httptest.NewRequest(http.MethodGet, "/api/actions/"+j.ID+"/diagnose", nil), j.ID)
	assertStatus(t, rr, http.StatusOK)

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not JSON: %v\nbody: %s", err, rr.Body.String())
	}
	if resp["job_id"] != j.ID {
		t.Errorf("job_id = %v, want %s", resp["job_id"], j.ID)
	}
	// interpreter.Analyze should return a non-nil analysis for a Go build error
	if resp["analysis"] == nil {
		t.Error("analysis is nil, expected structured analysis for known Go build error")
	}
	// related_files should pick up main.go:10
	files, _ := resp["related_files"].([]interface{})
	found := false
	for _, f := range files {
		if strings.Contains(fmt.Sprint(f), "main.go") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("related_files missing main.go reference; got %v", files)
	}
}

func TestHandleActionDiagnoseNotFound(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleActionDiagnose(rr, httptest.NewRequest(http.MethodGet, "/api/actions/no-such-id/diagnose", nil), "no-such-id")
	assertStatus(t, rr, http.StatusNotFound)
}

func TestHandleActionDiagnoseMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	j := seedJob(t, s, job.StatusFailed)
	rr := httptest.NewRecorder()
	s.handleActionDiagnose(rr, httptest.NewRequest(http.MethodPost, "/api/actions/"+j.ID+"/diagnose", nil), j.ID)
	assertStatus(t, rr, http.StatusMethodNotAllowed)
}

func TestHandleActionsCleanupDeletesOldTerminalJobs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ACTIOND_ACTIONS_DIR", base)
	s := newTestServer(t)

	// Old done job with an artifact dir -> deleted.
	oldDone := seedAgedJob(t, s, job.StatusDone, -30)
	oldDir := filepath.Join(base, "2026-01-01T00-00-00_git.push_demo-repo_git_"+oldDone.ID)
	mustMkdir(t, oldDir)

	// Old failed job -> deleted even without an artifact dir.
	oldFailed := seedAgedJob(t, s, job.StatusFailed, -30)

	// Old pending job -> preserved (never silently drop live work).
	oldPending := seedAgedJob(t, s, job.StatusPending, -30)

	// Recent done job -> preserved.
	recent := seedAgedJob(t, s, job.StatusDone, 0)
	recentDir := filepath.Join(base, "2026-08-23T00-00-00_git.push_demo-repo_git_"+recent.ID)
	mustMkdir(t, recentDir)

	// Orphan artifact dir (uuid suffix not in store) -> swept. Age its mtime
	// beyond the cutoff so the orphan sweep picks it up.
	orphan := "2026-02-01T00-00-00_git.push_demo-repo_git_11111111-2222-3333-4444-555555555555"
	orphanDir := filepath.Join(base, orphan)
	mustMkdir(t, orphanDir)
	oldMtime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(orphanDir, oldMtime, oldMtime); err != nil {
		t.Fatalf("chtimes orphan: %v", err)
	}

	// Non-artifact runtime files must never be touched.
	dbFile := filepath.Join(base, "actiond.db")
	mustWrite(t, dbFile, "keep-me")

	rr := httptest.NewRecorder()
	s.handleActionsCleanup(rr, httptest.NewRequest(http.MethodPost, "/api/actions/cleanup?days=7", nil))
	assertStatus(t, rr, http.StatusOK)

	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if res["deleted_jobs"].(float64) != 2 {
		t.Errorf("deleted_jobs = %v, want 2", res["deleted_jobs"])
	}
	if res["deleted_dirs"].(float64) != 2 {
		t.Errorf("deleted_dirs = %v, want 2 (old job dir + orphan)", res["deleted_dirs"])
	}

	if _, err := s.store.GetJob(oldDone.ID); err == nil {
		t.Error("old done job should be deleted from store")
	}
	if _, err := s.store.GetJob(oldFailed.ID); err == nil {
		t.Error("old failed job should be deleted from store")
	}
	if _, err := s.store.GetJob(oldPending.ID); err != nil {
		t.Error("old pending job must be preserved")
	}
	if _, err := s.store.GetJob(recent.ID); err != nil {
		t.Error("recent job must be preserved")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old artifact dir should be removed")
	}
	if _, err := os.Stat(recentDir); err != nil {
		t.Error("recent artifact dir must be preserved")
	}
	if _, err := os.Stat(filepath.Join(base, orphan)); !os.IsNotExist(err) {
		t.Error("orphan artifact dir should be swept")
	}
	if b, err := os.ReadFile(dbFile); err != nil || string(b) != "keep-me" {
		t.Error("non-artifact files must not be touched")
	}
}

func TestHandleActionsCleanupRejectsGet(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleActionsCleanup(rr, httptest.NewRequest(http.MethodGet, "/api/actions/cleanup", nil))
	assertStatus(t, rr, http.StatusMethodNotAllowed)
}

// seedAgedJob adds a job whose CreatedAt is offset by ageDays from now
// (negative = in the past). The store snapshots on AddJob, so aging must
// happen before insertion.
func seedAgedJob(t *testing.T, s *Server, status job.Status, ageDays int) *job.ActionJob {
	t.Helper()
	j := job.NewJob("evt-aged", "demo-repo", "go-lint")
	j.Status = status
	j.CreatedAt = time.Now().AddDate(0, 0, ageDays)
	if err := s.store.AddJob(j); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	return j
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
