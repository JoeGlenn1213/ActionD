package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Workflow-tool tests (actiond_cancel, actiond_job_wait, dev_cycle_run, parseEventRecord, findEventIDFromLog).
// Shares fakeClient / errBoom / callReq / wantOK / wantErr / bodyText with
// server_test.go (same package).

// fakeLGH implements LGHClient. Calls are recorded for assertion.
type lghCall struct {
	path    string
	message string
}

type fakeLGH struct {
	upOut, saveOut, rbOut string
	upErr, saveErr, rbErr error
	upCalls               []lghCall
	saveCalls             []lghCall
	rollbackCalls         []string
}

func (l *fakeLGH) Up(_ context.Context, path, message string) (string, error) {
	l.upCalls = append(l.upCalls, lghCall{path, message})
	return l.upOut, l.upErr
}

func (l *fakeLGH) Save(_ context.Context, path, message string) (string, error) {
	l.saveCalls = append(l.saveCalls, lghCall{path, message})
	return l.saveOut, l.saveErr
}

func (l *fakeLGH) Rollback(_ context.Context, path string) (string, error) {
	l.rollbackCalls = append(l.rollbackCalls, path)
	return l.rbOut, l.rbErr
}

// ---------------------------------------------------------------------------
// actiond_cancel
// ---------------------------------------------------------------------------

func TestHandleCancel(t *testing.T) {
	fc := &fakeClient{}
	r, err := handleCancel(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "Successfully cancelled action j-1") {
		t.Errorf("cancel body: %s", body)
	}
	if len(fc.cancelledIDs) != 1 || fc.cancelledIDs[0] != "j-1" {
		t.Errorf("CancelAction = %v, want [j-1]", fc.cancelledIDs)
	}
}

func TestHandleCancelMissingID(t *testing.T) {
	r, err := handleCancel(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleCancelError(t *testing.T) {
	fc := &fakeClient{cancelErr: errBoom}
	r, err := handleCancel(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Failed to cancel action")
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
	if body := bodyText(t, r); !strings.Contains(body, "lgh up failed") {
		t.Errorf("dev_cycle body missing failure note: %s", body)
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

func TestHandleDevCycleRunHappy(t *testing.T) {
	// Isolate the ~/.localgithub event-log read done by findEventIDFromLog.
	t.Setenv("HOME", t.TempDir())

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	repoName := filepath.Base(repoDir)

	flgh := &fakeLGH{upOut: "pushed"}
	fc := &fakeClient{actions: []ActionInfo{
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
	if len(flgh.upCalls) != 1 || flgh.upCalls[0].message != "feat: test" {
		t.Errorf("Up calls = %+v, want message 'feat: test'", flgh.upCalls)
	}
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
