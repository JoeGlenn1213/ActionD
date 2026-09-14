package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// Workflow-tool tests (actiond_job_wait, dev_cycle_run, parseEventRecord, findEventIDFromLog).
// Shares fakeClient / errBoom / callReq / wantOK / wantErr / bodyText with
// server_test.go (same package).

// fakeLGH implements LGHClient. Calls are recorded for assertion.
type lghCall struct {
	path    string
	message string
}

type rollbackCall struct {
	path     string
	toCommit string
}

type fakeLGH struct {
	upOut, saveOut, rbOut string
	upErr, saveErr, rbErr error
	upCalls               []lghCall
	saveCalls             []lghCall
	rollbackCalls         []rollbackCall
	// upSideEffect, when set, runs inside Up (e.g. create a real git commit),
	// simulating what "lgh up" does to the repo.
	upSideEffect func(path, message string)
}

func (l *fakeLGH) Up(_ context.Context, path, message string) (string, error) {
	l.upCalls = append(l.upCalls, lghCall{path, message})
	if l.upSideEffect != nil {
		l.upSideEffect(path, message)
	}
	return l.upOut, l.upErr
}

func (l *fakeLGH) Save(_ context.Context, path, message string) (string, error) {
	l.saveCalls = append(l.saveCalls, lghCall{path, message})
	return l.saveOut, l.saveErr
}

func (l *fakeLGH) Rollback(_ context.Context, path, toCommit string) (string, error) {
	l.rollbackCalls = append(l.rollbackCalls, rollbackCall{path, toCommit})
	return l.rbOut, l.rbErr
}

// ---------------------------------------------------------------------------
// actiond_job_wait
// ---------------------------------------------------------------------------

func TestHandleJobWaitDone(t *testing.T) {
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{
		"j-1": {ID: "j-1", Status: "done"},
	}}
	r, err := handleJobWait(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "j-1") {
		t.Errorf("job_wait body missing id: %s", body)
	}
}

func TestHandleJobWaitMissingID(t *testing.T) {
	r, err := handleJobWait(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleJobWaitGetActionError(t *testing.T) {
	r, err := handleJobWait(&fakeClient{getActionErr: errBoom}, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Failed to get action")
}

func TestHandleJobWaitTimeout(t *testing.T) {
	// Job never reaches a terminal status -> handler times out after ~1s.
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{
		"j-1": {ID: "j-1", Status: "running"},
	}}
	r, err := handleJobWait(fc, context.Background(), callReq(map[string]any{
		"id":      "j-1",
		"timeout": float64(1), // getInt reads float64 (as JSON does)
	}))
	wantErr(t, r, err, "Timeout waiting for job j-1")
}

// ---------------------------------------------------------------------------
// dev_cycle_run
// ---------------------------------------------------------------------------

func TestHandleDevCycleRunMissingMessage(t *testing.T) {
	r, err := handleDevCycleRun(&fakeClient{}, &fakeLGH{}, context.Background(), callReq(map[string]any{"path": "."}))
	wantErr(t, r, err, "Missing required parameter: message")
}

func TestHandleDevCycleRunInvalidProfile(t *testing.T) {
	r, err := handleDevCycleRun(&fakeClient{}, &fakeLGH{}, context.Background(), callReq(map[string]any{
		"path":    ".",
		"message": "feat: x",
		"profile": "bogus",
	}))
	wantErr(t, r, err, "Invalid profile")
}

func TestHandleDevCycleRunUpFails(t *testing.T) {
	// lgh up failure short-circuits before the job-poll loop. Path points at a
	// plain temp dir (the bare `git rev-parse` calls ignore their errors), so no
	// git repo fixture is required.
	flgh := &fakeLGH{upErr: errBoom}
	r, err := handleDevCycleRun(&fakeClient{}, flgh, context.Background(), callReq(map[string]any{
		"path":    t.TempDir(),
		"message": "feat: x",
	}))
	wantOK(t, r, err) // reported in body, not as IsError
	res := decodeDevCycle(t, r)
	if !strings.Contains(res.Error, "lgh up failed") {
		t.Errorf("dev_cycle result missing failure note: %s", res.Error)
	}
	if res.Verification != "unknown" {
		t.Errorf("verification = %q on up error, want declared \"unknown\" (never a fourth state)", res.Verification)
	}
	if res.Success {
		t.Error("success=true on up error")
	}
}

// initGitRepo creates a minimal git repo with one commit in dir.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
}

// gitHead returns the current HEAD sha of the git repo at dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gitRun runs a git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestHandleDevCycleRunHappy(t *testing.T) {
	// Isolate the ~/.localgithub event-log read done by findEventIDFromLog.
	t.Setenv("HOME", t.TempDir())

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)

	// A real lgh up creates a commit; without one the run has nothing of its
	// own to verify (and nothing to associate jobs with).
	flgh := &fakeLGH{upOut: "pushed"}
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
	}
	fc := &liveJobsClient{t: t, repoDir: repoDir, jobs: []ActionInfo{
		{ID: "job-1", Repo: repoName, PluginName: "go-lint", Status: "done"},
	}}
	r, err := handleDevCycleRun(fc, flgh, context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: test",
		"timeout": float64(5),
	}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	if !strings.Contains(body, "job-1") {
		t.Errorf("dev_cycle body missing job-1: %s", body)
	}
	if !strings.Contains(body, "全部通过") {
		t.Errorf("dev_cycle body missing success summary: %s", body)
	}
	if !strings.Contains(body, `"verification": "pass"`) {
		t.Errorf("dev_cycle body missing verification pass: %s", body)
	}
	if len(flgh.upCalls) != 1 || flgh.upCalls[0].message != "feat: test" {
		t.Errorf("Up calls = %+v, want message 'feat: test'", flgh.upCalls)
	}
}

// ---------------------------------------------------------------------------
// dev_cycle_run verdict honesty (review 2026-09-13 P1): zero jobs, query
// errors and timeouts must never surface as success=true.
// ---------------------------------------------------------------------------

// decodeDevCycle parses a dev_cycle_run result body into DevCycleResult.
func decodeDevCycle(t *testing.T, r *mcp.CallToolResult) DevCycleResult {
	t.Helper()
	var res DevCycleResult
	if err := json.Unmarshal([]byte(bodyText(t, r)), &res); err != nil {
		t.Fatalf("decode dev_cycle result: %v", err)
	}
	return res
}

// committingLGH returns a fake lgh whose Up really commits (as real lgh up
// does), so the run owns a fresh commit for attribution and association.
func committingLGH(t *testing.T) *fakeLGH {
	t.Helper()
	flgh := &fakeLGH{upOut: "pushed"}
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
	}
	return flgh
}

func TestHandleDevCycleRunNoJobsMustNotPass(t *testing.T) {
	// Reviewer repro #1: no CI jobs triggered → must be unknown, not pass.
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	r, err := handleDevCycleRun(&fakeClient{}, committingLGH(t), context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success {
		t.Errorf("success=true with 0 jobs — unverified CI must not report pass (summary=%q)", res.Summary)
	}
	if res.Verification != "unknown" {
		t.Errorf("verification = %q, want unknown", res.Verification)
	}
	if !strings.Contains(res.Summary, "未验证") {
		t.Errorf("summary %q must state the run is unverified", res.Summary)
	}
}

func TestHandleDevCycleRunQueryErrorMustNotPass(t *testing.T) {
	// Reviewer repro #2: first GetActions errors → must be unknown, not pass.
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	r, err := handleDevCycleRun(&fakeClient{getActionsErr: errBoom}, committingLGH(t), context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success {
		t.Errorf("success=true despite query error — must not report pass (error=%q)", res.Error)
	}
	if res.Verification != "unknown" {
		t.Errorf("verification = %q, want unknown", res.Verification)
	}
	if !strings.Contains(res.Error, "Failed to get actions") {
		t.Errorf("error %q missing query failure", res.Error)
	}
}

func TestHandleDevCycleRunEventQueryErrorMustNotPass(t *testing.T) {
	// Same contract on the precise (event_id) path: the LGH daemon logs OUR
	// push during lgh up (after the pre-Up boundary, on our ref), then
	// GetActionsByEventID errors.
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)
	ref := gitSymbolicRef(t, repoDir)
	before := gitHead(t, repoDir)

	flgh := &fakeLGH{upOut: "pushed"}
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
		writePushEvent(t, home, repoName, ref, before, gitHead(t, path), "evt-1")
	}
	r, err := handleDevCycleRun(&fakeClient{actionsByEventErr: errBoom}, flgh, context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success || res.Verification != "unknown" {
		t.Errorf("success=%v verification=%q, want false/unknown on event query error", res.Success, res.Verification)
	}
	if !strings.Contains(res.Error, "Failed to get actions") {
		t.Errorf("error %q missing query failure", res.Error)
	}
}

func TestHandleDevCycleRunTimeoutMustNotPass(t *testing.T) {
	// Jobs still running when the deadline hits → unknown, not pass.
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)

	fc := &liveJobsClient{t: t, repoDir: repoDir, jobs: []ActionInfo{
		{ID: "job-slow", Repo: repoName, PluginName: "go-lint", Status: "running"},
	}}
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success {
		t.Errorf("success=true on timeout — running jobs are not a pass")
	}
	if res.Verification != "unknown" {
		t.Errorf("verification = %q, want unknown on timeout", res.Verification)
	}
	if !strings.Contains(res.Summary, "超时") {
		t.Errorf("summary %q must mention timeout", res.Summary)
	}
}

func TestHandleDevCycleRunNoPushMustNotAdoptHistory(t *testing.T) {
	// Review round-3 repro #1: lgh up pushes nothing new ("Everything
	// up-to-date") and a HISTORICAL push of another branch moved the same
	// base tip — its green checks must never verify this run.
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)
	head := gitHead(t, repoDir)
	gitRun(t, repoDir, "branch", "-M", "main")

	// Another branch previously advanced from the same base; main stays put.
	gitRun(t, repoDir, "checkout", "-b", "feature")
	gitRun(t, repoDir, "commit", "--allow-empty", "-m", "historical feature commit")
	other := gitHead(t, repoDir)
	gitRun(t, repoDir, "checkout", "main")
	writePushEvent(t, home, repoName, "refs/heads/feature", head, other, "old-feature-event")

	fc := &fakeClient{actionsByEvent: map[string][]ActionInfo{
		"old-feature-event": {{ID: "old-green", Repo: repoName, CommitSHA: other, Status: "done"}},
	}}
	r, err := handleDevCycleRun(fc, &fakeLGH{upOut: "Everything up-to-date"}, context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "verify",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Commit == other || res.Success {
		t.Fatalf("historical event from another branch falsely attributed: commit=%s verification=%q", res.Commit, res.Verification)
	}
	if res.Verification != "unknown" || len(res.Jobs) != 0 {
		t.Errorf("verification=%q jobs=%+v; a no-op push must report zero jobs / unknown", res.Verification, res.Jobs)
	}
	if !strings.Contains(res.Summary, "没有产生新提交") {
		t.Errorf("summary %q must explain the no-op push", res.Summary)
	}
}

// ---------------------------------------------------------------------------
// dev_cycle_run fallback association (review 2026-09-13 P1): repo-name
// substring match without a commit filter could adopt green jobs from an old
// commit or a similarly named repo.
// ---------------------------------------------------------------------------

// assocWindowClient serves a mixed window: an old-commit job of this repo, a
// current-sha job of a similarly named repo, and a job of this repo whose sha
// is only resolvable via detail (computed against the live HEAD, since the
// run's commit only exists after Up).
type assocWindowClient struct {
	fakeClient
	t        *testing.T
	repoDir  string
	repoName string
}

func (c *assocWindowClient) GetActions(int) ([]ActionInfo, error) {
	head := gitHead(c.t, c.repoDir)
	return []ActionInfo{
		{ID: "job-old", Repo: c.repoName, PluginName: "go-lint", Status: "done", CommitSHA: "0000000000000000000000000000000000000001"},
		{ID: "job-twin", Repo: c.repoName + "-utils", PluginName: "go-test", Status: "done", CommitSHA: head},
		{ID: "job-now", Repo: c.repoName + ".git", PluginName: "go-vet", Status: "done"},
	}, nil
}

func (c *assocWindowClient) GetAction(id string) (*ActionDetail, error) {
	if id == "job-now" {
		return &ActionDetail{ID: id, Status: "done", Commit: map[string]interface{}{"hash": gitHead(c.t, c.repoDir)}}, nil
	}
	return c.fakeClient.GetAction(id)
}

func TestHandleDevCycleRunFallbackExcludesForeignJobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	fc := &assocWindowClient{t: t, repoDir: repoDir, repoName: filepath.Base(repoDir)}
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(5),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if len(res.Jobs) != 1 || res.Jobs[0].ID != "job-now" {
		t.Errorf("jobs = %+v, want exactly [job-now] (old-commit and twin-repo jobs must not be adopted)", res.Jobs)
	}
	if res.Verification != "pass" || !res.Success {
		t.Errorf("verification=%q success=%v, want pass/true for the one associated done job", res.Verification, res.Success)
	}
	if len(res.UnassociatedJobs) != 0 {
		t.Errorf("unassociated = %v, want none (every candidate was decisively resolved)", res.UnassociatedJobs)
	}
}

func TestHandleDevCycleRunUnassociatedJobsAreUnknown(t *testing.T) {
	// No job can be pinned to this commit (all old/different shas, no
	// resolvable details) → verification unknown, success false — even
	// though every visible job is green.
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)

	fc := &fakeClient{actions: []ActionInfo{
		{ID: "job-old", Repo: repoName, PluginName: "go-lint", Status: "done", CommitSHA: "0000000000000000000000000000000000000001"},
	}}
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path":    repoDir,
		"message": "feat: x",
		"timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success || res.Verification != "unknown" {
		t.Errorf("success=%v verification=%q — an old commit's green job must not verify this push", res.Success, res.Verification)
	}
}

// ---------------------------------------------------------------------------
// dev_cycle_run auto rollback (review 2026-09-13 P1): reset --hard must
// target the recorded beforeCommit (never HEAD~1) and must be skipped when
// new commits or edits appeared while CI was running.
// ---------------------------------------------------------------------------

// liveFailClient returns one failed job pinned to the repo's live HEAD (read
// before pollHook so the job stays associated with the run's commit), so the
// handler sees a confirmed failure. pollHook runs on each poll, simulating
// wait-period activity (new commits, dirty files).
type liveFailClient struct {
	fakeClient
	t        *testing.T
	repoDir  string
	pollHook func()
}

func (c *liveFailClient) GetActions(int) ([]ActionInfo, error) {
	out, err := exec.Command("git", "-C", c.repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		c.t.Fatalf("git rev-parse HEAD: %v", err)
	}
	head := strings.TrimSpace(string(out))
	if c.pollHook != nil {
		c.pollHook()
	}
	return []ActionInfo{{
		ID:         "job-fail",
		Repo:       filepath.Base(c.repoDir),
		PluginName: "go-lint",
		Status:     "failed",
		CommitSHA:  head,
	}}, nil
}

// newRollbackFixture builds the common scenario: repo with an init commit, a
// fake lgh whose Up really commits (so beforeCommit != result.Commit), and a
// client reporting a failed job for the new HEAD.
func newRollbackFixture(t *testing.T) (repoDir, beforeCommit string, flgh *fakeLGH, lc *liveFailClient) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repoDir = t.TempDir()
	initGitRepo(t, repoDir)
	beforeCommit = gitHead(t, repoDir)

	flgh = &fakeLGH{upOut: "pushed"}
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
	}
	lc = &liveFailClient{t: t, repoDir: repoDir}
	return
}

// mixedLGH gives tests a fake Up (with real side effects, e.g. creating the
// run commit) while delegating Rollback to the real LocalLGHClient, so
// auto-rollback tests observe actual git state.
type mixedLGH struct {
	fakeLGH
	real *LocalLGHClient
}

func (m *mixedLGH) Rollback(ctx context.Context, path, toCommit string) (string, error) {
	m.rollbackCalls = append(m.rollbackCalls, rollbackCall{path, toCommit})
	return m.real.Rollback(ctx, path, toCommit)
}

func TestDevCycleRunAutoRollbackDone(t *testing.T) {
	repoDir, beforeCommit, flgh, lc := newRollbackFixture(t)
	lc.pollHook = func() {} // nothing changes during the wait
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}

	r, err := handleDevCycleRun(lc, mlgh, context.Background(), callReq(map[string]any{
		"path":         repoDir,
		"message":      "feat: x",
		"timeout":      float64(5),
		"auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Verification != "fail" {
		t.Fatalf("verification = %q, want fail", res.Verification)
	}
	if res.Rollback == nil || res.Rollback.Status != "done" {
		t.Fatalf("rollback = %+v, want status done", res.Rollback)
	}
	if after := gitHead(t, repoDir); after != beforeCommit {
		t.Errorf("repo HEAD = %s after rollback, want beforeCommit %s", after, beforeCommit)
	}
	if len(mlgh.rollbackCalls) != 1 || mlgh.rollbackCalls[0].toCommit != beforeCommit {
		t.Errorf("rollback calls = %+v, want explicit reset to beforeCommit %s", mlgh.rollbackCalls, beforeCommit)
	}
}

func TestDevCycleRunAutoRollbackSkippedOnNewCommit(t *testing.T) {
	repoDir, beforeCommit, flgh, lc := newRollbackFixture(t)
	var concurrentCommit string
	lc.pollHook = func() {
		if concurrentCommit == "" {
			// User commits again while CI is running.
			gitRun(t, repoDir, "commit", "--allow-empty", "-m", "user: concurrent work")
			concurrentCommit = gitHead(t, repoDir)
		}
	}
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}

	r, err := handleDevCycleRun(lc, mlgh, context.Background(), callReq(map[string]any{
		"path":         repoDir,
		"message":      "feat: x",
		"timeout":      float64(5),
		"auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Rollback == nil || res.Rollback.Status != "skipped" {
		t.Fatalf("rollback = %+v, want status skipped", res.Rollback)
	}
	if after := gitHead(t, repoDir); after != concurrentCommit || after == beforeCommit {
		t.Errorf("repo HEAD = %s, want the wait-period commit %s preserved (beforeCommit=%s)", after, concurrentCommit, beforeCommit)
	}
	if len(mlgh.rollbackCalls) != 0 {
		t.Errorf("rollback was executed despite HEAD moving: %+v", mlgh.rollbackCalls)
	}
}

func TestDevCycleRunAutoRollbackSkippedOnDirtyTree(t *testing.T) {
	repoDir, _, flgh, lc := newRollbackFixture(t)
	written := false
	lc.pollHook = func() {
		// User edits a file while CI is running (run once).
		if !written {
			written = true
			if err := os.WriteFile(filepath.Join(repoDir, "user-edit.txt"), []byte("don't lose me"), 0644); err != nil {
				t.Errorf("write user edit: %v", err)
			}
		}
	}
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}

	r, err := handleDevCycleRun(lc, mlgh, context.Background(), callReq(map[string]any{
		"path":         repoDir,
		"message":      "feat: x",
		"timeout":      float64(5),
		"auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Rollback == nil || res.Rollback.Status != "skipped" {
		t.Fatalf("rollback = %+v, want status skipped", res.Rollback)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "user-edit.txt")); err != nil {
		t.Errorf("user edit made during the wait was destroyed: %v", err)
	}
	if len(mlgh.rollbackCalls) != 0 {
		t.Errorf("rollback was executed despite dirty tree: %+v", mlgh.rollbackCalls)
	}
}

func TestDevCycleRunAutoRollbackNotTriggeredOnUnknown(t *testing.T) {
	// Zero jobs → unknown → auto_rollback must NOT fire (no failure evidence,
	// reset --hard would only destroy possibly-good work).
	repoDir, _, flgh, _ := newRollbackFixture(t)
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}
	runCommit := gitHead(t, repoDir) // upSideEffect will move HEAD past this below

	r, err := handleDevCycleRun(&fakeClient{}, mlgh, context.Background(), callReq(map[string]any{
		"path":         repoDir,
		"message":      "feat: x",
		"timeout":      float64(1),
		"auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Verification != "unknown" {
		t.Fatalf("verification = %q, want unknown", res.Verification)
	}
	if res.Rollback != nil || len(mlgh.rollbackCalls) != 0 {
		t.Errorf("rollback fired on unknown verdict: %+v / %+v", res.Rollback, mlgh.rollbackCalls)
	}
	if after := gitHead(t, repoDir); after == runCommit || after == "" {
		t.Errorf("fixture sanity: expected Up to have created a commit beyond %s, got %s", runCommit, after)
	}
}

// ---------------------------------------------------------------------------
// LocalLGHClient.Rollback / rollbackGuard unit behavior
// ---------------------------------------------------------------------------

func TestLocalLGHClientRollbackResetsToExplicitCommit(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	first := gitHead(t, repoDir)
	gitRun(t, repoDir, "commit", "--allow-empty", "-m", "second")
	second := gitHead(t, repoDir)

	c := NewLocalLGHClient()
	out, err := c.Rollback(context.Background(), repoDir, first)
	if err != nil {
		t.Fatalf("Rollback: %v (%s)", err, out)
	}
	if after := gitHead(t, repoDir); after != first {
		t.Errorf("HEAD = %s after rollback, want %s (explicit target, not HEAD~1)", after, first)
	}
	if second == first {
		t.Fatal("fixture setup error: commits identical")
	}

	if _, err := c.Rollback(context.Background(), repoDir, ""); err == nil {
		t.Error("Rollback with empty target must error, never derive HEAD~1")
	}
}

func TestRollbackGuard(t *testing.T) {
	t.Run("clean repo exactly one commit ahead passes", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		first := gitHead(t, repoDir)
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "second")
		second := gitHead(t, repoDir)
		if err := rollbackGuard(context.Background(), repoDir, second, first); err != nil {
			t.Errorf("unexpected guard rejection: %v", err)
		}
	})

	t.Run("moved HEAD is rejected", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		first := gitHead(t, repoDir)
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "run")
		second := gitHead(t, repoDir)
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "moved")
		if err := rollbackGuard(context.Background(), repoDir, second, first); err == nil {
			t.Error("guard must reject when HEAD moved")
		}
	})

	t.Run("dirty tree is rejected", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		first := gitHead(t, repoDir)
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "second")
		second := gitHead(t, repoDir)
		if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := rollbackGuard(context.Background(), repoDir, second, first); err == nil {
			t.Error("guard must reject uncommitted changes")
		}
	})

	t.Run("multiple commits on base are rejected", func(t *testing.T) {
		// HEAD matches expectHead, tree clean, but a concurrent commit sits
		// between base and HEAD — reset --hard would remove it too.
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		first := gitHead(t, repoDir)
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "run")
		gitRun(t, repoDir, "commit", "--allow-empty", "-m", "concurrent")
		head := gitHead(t, repoDir)
		if err := rollbackGuard(context.Background(), repoDir, head, first); err == nil {
			t.Error("guard must reject when >1 commit sits on top of the base")
		}
	})
}

// gitSymbolicRef returns the full ref of HEAD (e.g. refs/heads/main).
func gitSymbolicRef(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "-q", "HEAD").Output()
	if err != nil {
		t.Fatalf("git symbolic-ref HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// writePushEvent appends one git.push record to the event log under home,
// keyed by ref, with explicit old/new branch tips. Used to simulate the LGH
// daemon logging a push (possibly during lgh up's CI wait).
func writePushEvent(t *testing.T, home, repoName, ref, oldTip, newTip, eventID string) {
	t.Helper()
	logDir := filepath.Join(home, ".localgithub", "events")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"id":%q,"type":"git.push","repo":%q,"payload":{"changes":{"%s":{"old":%q,"new":%q}}}}`, eventID, repoName+".git", ref, oldTip, newTip)
	path := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// liveJobsClient reports a fixed job template pinned to the repo's live HEAD,
// simulating jobs dispatched for the push lgh up just made (the sha only
// exists after Up commits, so it cannot be captured at client construction).
type liveJobsClient struct {
	fakeClient
	t       *testing.T
	repoDir string
	jobs    []ActionInfo // Repo/Status template; CommitSHA filled from live HEAD
}

func (c *liveJobsClient) GetActions(int) ([]ActionInfo, error) {
	head := gitHead(c.t, c.repoDir)
	out := make([]ActionInfo, 0, len(c.jobs))
	for _, j := range c.jobs {
		j.CommitSHA = head
		out = append(out, j)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Review round 2 (FIX_SUMMARY_REVIEW_2026-09-13) — four boundary regressions,
// ported from the reviewer's repro suite into the official test set.
// ---------------------------------------------------------------------------

// Concurrent commit during lgh up's post-push CI wait must never be rolled
// back. No event log → HEAD-based attribution; the rev-list guard rejects the
// rollback because >1 commit sits on top of beforeCommit.
func TestDevCycleRunUpWaitConcurrentCommitPreserved(t *testing.T) {
	repoDir, beforeCommit, flgh, lc := newRollbackFixture(t)
	var userCommit string
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
		// lgh up waits for CI before returning; another agent commits here.
		gitRun(t, path, "commit", "--allow-empty", "-m", "user: work during lgh up CI wait")
		userCommit = gitHead(t, path)
	}
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}

	r, err := handleDevCycleRun(lc, mlgh, context.Background(), callReq(map[string]any{
		"path": repoDir, "message": "run", "timeout": float64(2), "auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if after := gitHead(t, repoDir); after != userCommit {
		t.Fatalf("rollback removed the user commit made during lgh up wait: HEAD=%s, want %s (before=%s)", after, userCommit, beforeCommit)
	}
	if res.Rollback != nil && res.Rollback.Status == "done" {
		t.Errorf("rollback executed despite concurrent commit: %+v", res.Rollback)
	}
}

// Same scenario, but the LGH event log recorded our push (old=beforeCommit,
// on our ref, after the pre-Up boundary): attribution must pin OUR commit,
// not the post-wait HEAD; the concurrent commit is preserved and the run
// reports an honest unknown.
func TestDevCycleRunUpWaitConcurrentCommitEventLogAttribution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)
	ref := gitSymbolicRef(t, repoDir)
	before := gitHead(t, repoDir)

	var runCommit string
	flgh := &fakeLGH{upOut: "pushed"}
	flgh.upSideEffect = func(path, message string) {
		gitRun(t, path, "commit", "--allow-empty", "-m", message)
		runCommit = gitHead(t, path)
		// LGH logs our push (branch tip moved before → runCommit)...
		writePushEvent(t, home, repoName, ref, before, runCommit, "evt-ours")
		// ...then, while lgh up waits for CI, someone else commits.
		gitRun(t, path, "commit", "--allow-empty", "-m", "user: concurrent work")
	}
	// A failing job pinned to the concurrent HEAD must NOT be associated
	// with our commit.
	lc := &liveFailClient{t: t, repoDir: repoDir}
	mlgh := &mixedLGH{fakeLGH: *flgh, real: NewLocalLGHClient()}

	r, err := handleDevCycleRun(lc, mlgh, context.Background(), callReq(map[string]any{
		"path": repoDir, "message": "run", "timeout": float64(2), "auto_rollback": true,
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Commit != runCommit {
		t.Errorf("result.Commit = %s, want the push this run made (%s), not the post-wait HEAD", res.Commit, runCommit)
	}
	if res.Verification != "unknown" || res.Success {
		t.Errorf("verification=%q success=%v; a foreign-head job must not verify (or fail) this run", res.Verification, res.Success)
	}
	if after := gitHead(t, repoDir); after == before || after == runCommit {
		t.Errorf("concurrent commit lost: HEAD=%s (before=%s, run=%s)", after, before, runCommit)
	}
	if res.Rollback != nil {
		t.Errorf("rollback fired on an unattributed run: %+v", res.Rollback)
	}
}

// blockedPassClient serves one confirmed done job (live HEAD sha) plus one
// candidate whose sha resolution keeps failing (global detail error).
type blockedPassClient struct {
	fakeClient
	t       *testing.T
	repoDir string
}

func (c *blockedPassClient) GetActions(int) ([]ActionInfo, error) {
	return []ActionInfo{
		{ID: "known", Repo: filepath.Base(c.repoDir), CommitSHA: gitHead(c.t, c.repoDir), Status: "done"},
		{ID: "unresolved", Repo: filepath.Base(c.repoDir), Status: "failed"},
	}, nil
}

// A detail-query failure during sha resolution must not be swallowed: the
// unresolvable candidate blocks a pass verdict instead of being discarded
// while a lone done job reports success.
func TestDevCycleRunDetailQueryErrorBlocksPass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	fc := &blockedPassClient{t: t, repoDir: repoDir}
	fc.getActionErr = errBoom
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path": repoDir, "message": "run", "timeout": float64(1),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Success || res.Verification == "pass" {
		t.Fatalf("partial association with detail API failure reported pass: success=%v verification=%q", res.Success, res.Verification)
	}
	if !containsStr(res.UnassociatedJobs, "unresolved") {
		t.Errorf("unresolved candidate silently discarded: %+v", res.UnassociatedJobs)
	}
	if !containsStr(jobIDs(res.Jobs), "known") {
		t.Errorf("confirmed job missing from result: %+v", res.Jobs)
	}
	if !strings.Contains(res.Summary, "归属未确认") {
		t.Errorf("summary must explain the unassociated candidates: %q", res.Summary)
	}
}

// flakyDetailClient: the candidate's sha lookup fails once, then resolves to
// a DIFFERENT commit — ownership becomes decided-foreign and must stop
// blocking the otherwise valid pass (review round-3 repro #2).
type flakyDetailClient struct {
	fakeClient
	t           *testing.T
	repoDir     string
	detailCalls int
}

func (c *flakyDetailClient) GetActions(int) ([]ActionInfo, error) {
	return []ActionInfo{
		{ID: "good", Repo: filepath.Base(c.repoDir), CommitSHA: gitHead(c.t, c.repoDir), Status: "done"},
		{ID: "candidate", Repo: filepath.Base(c.repoDir), Status: "done"},
	}, nil
}

func (c *flakyDetailClient) GetAction(id string) (*ActionDetail, error) {
	if id == "candidate" {
		c.detailCalls++
		if c.detailCalls == 1 {
			return nil, errBoom
		}
		return &ActionDetail{ID: id, Status: "done", Commit: map[string]interface{}{"hash": "0000000000000000000000000000000000000001"}}, nil
	}
	return c.fakeClient.GetAction(id)
}

func TestDevCycleRunResolvedForeignCandidateStopsBlocking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	fc := &flakyDetailClient{t: t, repoDir: repoDir}
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path": repoDir, "message": "run", "timeout": float64(3),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if !res.Success || len(res.UnassociatedJobs) > 0 {
		t.Fatalf("resolved-foreign candidate permanently blocks a valid pass: success=%v unassociated=%v (detail calls=%d)", res.Success, res.UnassociatedJobs, fc.detailCalls)
	}
	if fc.detailCalls < 2 {
		t.Errorf("detail calls = %d; the flaky candidate must have been retried", fc.detailCalls)
	}
	if !containsStr(jobIDs(res.Jobs), "good") {
		t.Errorf("confirmed job missing: %+v", res.Jobs)
	}
}

// pageShiftClient simulates the sliding 50-job list window: the failed job
// is visible only in the first poll. Shas are pinned to the live HEAD.
type pageShiftClient struct {
	fakeClient
	t       *testing.T
	repoDir string
	calls   int
}

func (c *pageShiftClient) GetActions(int) ([]ActionInfo, error) {
	c.calls++
	head := gitHead(c.t, c.repoDir)
	repo := filepath.Base(c.repoDir)
	if c.calls == 1 {
		return []ActionInfo{
			{ID: "failed", Repo: repo, CommitSHA: head, Status: "failed"},
			{ID: "running", Repo: repo, CommitSHA: head, Status: "running"},
		}, nil
	}
	return []ActionInfo{{ID: "running", Repo: repo, CommitSHA: head, Status: "done"}}, nil
}

// An observed failure must survive the list window sliding past it.
func TestDevCycleRunFailureSurvivesListWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	fc := &pageShiftClient{t: t, repoDir: repoDir}
	r, err := handleDevCycleRun(fc, committingLGH(t), context.Background(), callReq(map[string]any{
		"path": repoDir, "message": "run", "timeout": float64(3),
	}))
	wantOK(t, r, err)
	res := decodeDevCycle(t, r)
	if res.Verification != "fail" {
		t.Fatalf("verification = %q; the previously observed failed job disappeared from the aggregate (jobs=%+v)", res.Verification, res.Jobs)
	}
	if !containsStr(jobIDs(res.Jobs), "failed") {
		t.Errorf("failed job missing from final jobs: %+v", res.Jobs)
	}
}

// jobIDs extracts the IDs of a JobSummary slice.
func jobIDs(jobs []JobSummary) []string {
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// parseEventRecord
// ---------------------------------------------------------------------------

func TestParseEventRecord(t *testing.T) {
	t.Run("valid record returns no error", func(t *testing.T) {
		line := `{"id":"evt-1","type":"git.push","repo":"myrepo.git","payload":{}}`
		evt, err := parseEventRecord(line)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.ID != "evt-1" || evt.Type != "git.push" {
			t.Errorf("got id=%q type=%q, want evt-1/git.push", evt.ID, evt.Type)
		}
	})

	t.Run("invalid JSON returns error not silent skip", func(t *testing.T) {
		_, err := parseEventRecord(`{not valid json}`)
		if err == nil {
			t.Fatal("expected error for malformed JSON, got nil — silent degradation risk!")
		}
	})

	t.Run("missing id field returns error not silent skip", func(t *testing.T) {
		// Simulate a schema change where 'id' is renamed to 'event_id'
		line := `{"event_id":"evt-1","type":"git.push","repo":"myrepo.git","payload":{}}`
		_, err := parseEventRecord(line)
		if err == nil {
			t.Fatal("expected error when 'id' field is absent — format change would silently degrade without this check!")
		}
	})

	t.Run("missing type field returns error not silent skip", func(t *testing.T) {
		line := `{"id":"evt-1","repo":"myrepo.git","payload":{}}`
		_, err := parseEventRecord(line)
		if err == nil {
			t.Fatal("expected error when 'type' field is absent")
		}
	})
}

// ---------------------------------------------------------------------------
// findEventIDFromLog
// ---------------------------------------------------------------------------

func TestFindEventIDFromLog(t *testing.T) {
	writeLog := func(t *testing.T, lines ...string) string {
		t.Helper()
		dir := t.TempDir()
		logDir := dir + "/.localgithub/events"
		if err := os.MkdirAll(logDir, 0700); err != nil {
			t.Fatal(err)
		}
		logPath := logDir + "/events.jsonl"
		content := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(logPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", dir)
		return logPath
	}

	t.Run("returns id on exact commit match", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-abc","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"new":"deadbeef1234","old":"00000000"}}}}`,
		)
		id, err := findEventIDFromLog("deadbeef1234", "myrepo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "evt-abc" {
			t.Errorf("got id=%q, want evt-abc", id)
		}
	})

	t.Run("returns empty string (no error) when no match found", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-xyz","type":"git.push","repo":"other-repo.git","payload":{"changes":{}}}`,
		)
		id, err := findEventIDFromLog("deadbeef", "myrepo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Errorf("got id=%q, want empty string", id)
		}
	})

	t.Run("returns empty string (no error) when log file does not exist", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir()) // empty home, no events.jsonl
		id, err := findEventIDFromLog("deadbeef", "myrepo")
		if err != nil {
			t.Fatalf("unexpected error for missing file: %v", err)
		}
		if id != "" {
			t.Errorf("got id=%q, want empty string", id)
		}
	})

	t.Run("returns error (not empty) when log contains malformed lines", func(t *testing.T) {
		// This is the key regression guard: format change → error, never silent skip.
		writeLog(t,
			`{"id":"evt-1","type":"git.push","repo":"myrepo.git","payload":{}}`,
			`{MALFORMED LINE SIMULATING SCHEMA CHANGE}`,
		)
		_, err := findEventIDFromLog("anycommit", "myrepo")
		if err == nil {
			t.Fatal("expected error when log contains malformed lines — silent degradation risk!")
		}
	})
}

func TestFindPushEventSince(t *testing.T) {
	writeLog := func(t *testing.T, lines ...string) {
		t.Helper()
		dir := t.TempDir()
		logDir := dir + "/.localgithub/events"
		if err := os.MkdirAll(logDir, 0700); err != nil {
			t.Fatal(err)
		}
		content := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(logDir+"/events.jsonl", []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", dir)
	}
	const mainRef = "refs/heads/main"

	t.Run("matches the in-window push that moved our ref from oldCommit", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-run","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"bbb2222"}}}}`,
			`{"id":"evt-other","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"bbb2222","new":"ccc3333"}}}}`,
		)
		id, newSHA, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "evt-run" || newSHA != "bbb2222" {
			t.Errorf("got (%q, %q), want (evt-run, bbb2222)", id, newSHA)
		}
	})

	t.Run("historical events before the boundary are ignored", func(t *testing.T) {
		// The only matching record predates the run's pre-Up boundary.
		writeLog(t,
			`{"id":"evt-historical","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"bbb2222"}}}}`,
		)
		id, newSHA, err := findPushEventSince("myrepo", "aaa1111", mainRef, 1)
		if err != nil || id != "" || newSHA != "" {
			t.Errorf("got (%q, %q, %v), want no match across the boundary", id, newSHA, err)
		}
	})

	t.Run("another branch's historical push from the same base is ignored", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-feature","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/feature":{"old":"aaa1111","new":"fff4444"}}}}`,
			`{"id":"evt-ours","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"bbb2222"}}}}`,
		)
		id, newSHA, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "evt-ours" || newSHA != "bbb2222" {
			t.Errorf("got (%q, %q), want the main-ref push (evt-ours, bbb2222)", id, newSHA)
		}
	})

	t.Run("old and new must come from the same ref entry", func(t *testing.T) {
		// main moved elsewhere; feature moved from our base — no single ref
		// entry carries old=ours → must not fabricate a push.
		writeLog(t,
			`{"id":"evt-mixed","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"zzz9999","new":"bbb2222"},"refs/heads/feature":{"old":"aaa1111","new":"yyy8888"}}}}`,
		)
		id, newSHA, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if err != nil || id != "" || newSHA != "" {
			t.Errorf("got (%q, %q, %v), want no cross-ref fabrication", id, newSHA, err)
		}
	})

	t.Run("multiple in-window matches are ambiguous", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-a","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"bbb2222"}}}}`,
			`{"id":"evt-b","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"ddd5555"}}}}`,
		)
		_, _, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if !errors.Is(err, errAmbiguousPush) {
			t.Fatalf("got %v, want errAmbiguousPush", err)
		}
	})

	t.Run("no-op push (old == new) never matches", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-noop","type":"git.push","repo":"myrepo.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"aaa1111"}}}}`,
		)
		id, newSHA, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if err != nil || id != "" || newSHA != "" {
			t.Errorf("got (%q, %q, %v), want no match for a no-op push", id, newSHA, err)
		}
	})

	t.Run("empty oldCommit is a no-op", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if id, newSHA, err := findPushEventSince("myrepo", "", mainRef, 0); err != nil || id != "" || newSHA != "" {
			t.Errorf("got (%q, %q, %v), want empty no-op", id, newSHA, err)
		}
	})

	t.Run("other repos are ignored", func(t *testing.T) {
		writeLog(t,
			`{"id":"evt-elsewhere","type":"git.push","repo":"other.git","payload":{"changes":{"refs/heads/main":{"old":"aaa1111","new":"fff0000"}}}}`,
		)
		id, _, err := findPushEventSince("myrepo", "aaa1111", mainRef, 0)
		if err != nil || id != "" {
			t.Errorf("got (%q, %v), want no match across repos", id, err)
		}
	})
}
