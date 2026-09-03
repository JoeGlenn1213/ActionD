// Copyright (c) 2025 JoeGlenn1213
// ActionD Worker - recovery requeue tests (ASSURANCE §7 前置债)

package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/artifact"
	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/store"
)

func TestRequeueResetsAndExecutes(t *testing.T) {
	// A deterministic exec plugin that exits 0.
	p := plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name:     "echo-test",
		Command:  "/bin/echo",
		Args:     []string{"ok"},
		Triggers: []string{event.TypeGitPush},
	})

	st := store.NewMemoryStore()
	w := NewWorker(4, artifact.NewStore(t.TempDir()), st, "")
	w.SetPluginResolver(func(name string) plugin.Plugin { return p })
	w.Start()
	defer w.Stop()

	evt := event.Event{ID: "evt-test-1", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: time.Now()}
	j := job.NewJob(evt.ID, "repo/x", "echo-test")
	j.EventJSON = mustJSON(t, evt)
	j.Status = job.StatusRunning // stale pre-restart state
	j.CreatedAt = time.Now()
	if err := st.AddJob(j); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	if !w.Requeue(j, Task{Plugin: p, Event: evt, Profile: "fast"}) {
		t.Fatal("Requeue returned false")
	}

	// Wait for the worker to process and finish the job.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.GetJob(j.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got.Status == job.StatusDone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach done within 5s", j.ID)
}

// TestRequeueDedupInQueue: the same event+plugin cannot be requeued twice in
// one instance (in-memory dedup set).
func TestRequeueDedupInQueue(t *testing.T) {
	p := plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name: "echo-test", Command: "/bin/echo", Args: []string{"ok"},
		Triggers: []string{event.TypeGitPush},
	})
	st := store.NewMemoryStore()
	w := NewWorker(4, artifact.NewStore(t.TempDir()), st, "")
	w.Start()
	defer w.Stop()

	evt := event.Event{ID: "evt-test-2", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: time.Now()}
	mk := func(id string) *job.ActionJob {
		j := job.NewJob(evt.ID, "repo/x", "echo-test")
		j.ID = id
		j.CreatedAt = time.Now()
		return j
	}
	j1, j2 := mk("rq-1"), mk("rq-2")
	_ = st.AddJob(j1)

	if !w.Requeue(j1, Task{Plugin: p, Event: evt, Profile: "fast"}) {
		t.Fatal("first requeue must succeed")
	}
	if w.Requeue(j2, Task{Plugin: p, Event: evt, Profile: "fast"}) {
		t.Fatal("second requeue for same event+plugin must be skipped")
	}
}

// TestRetryMarksRetryOf: retried jobs must carry retry_of so the dispatch
// dedup index (unique event_id+plugin_name for non-retry rows) does not
// swallow them — regression for actiond_job_retry silently not re-running.
func TestRetryMarksRetryOf(t *testing.T) {
	p := plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name: "echo-test", Command: "/bin/echo", Args: []string{"ok"},
		Triggers: []string{event.TypeGitPush},
	})
	st := store.NewMemoryStore()
	w := NewWorker(4, artifact.NewStore(t.TempDir()), st, "")
	w.SetPluginResolver(func(name string) plugin.Plugin { return p })
	w.Start()
	defer w.Stop()

	evt := event.Event{ID: "evt-retry-1", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: time.Now()}
	firstID := w.Submit(Task{Plugin: p, Event: evt, Profile: "fast"})
	if firstID == "" {
		t.Fatal("first submit returned empty id")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.GetJob(firstID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got != nil && got.Status == job.StatusDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	retryID, err := w.Retry(firstID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if retryID == "" {
		t.Fatal("retry returned empty id — duplicate swallowed")
	}
	if retryID == firstID {
		t.Fatal("retry should create a new job id")
	}
	got, err := st.GetJob(retryID)
	if err != nil || got == nil {
		t.Fatalf("retry job not persisted: %v", err)
	}
	if got.RetryOf != firstID {
		t.Fatalf("RetryOf = %q, want %q", got.RetryOf, firstID)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestIntentForEvent: dispatch-time intent from the pushed commit message.
func TestIntentForEvent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repoDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", repoDir)
	run("-C", repoDir, "config", "user.email", "t@e.c")
	run("-C", repoDir, "config", "user.name", "t")
	run("-C", repoDir, "commit", "--allow-empty", "-q", "-m", "feat: demo task:intent-7")
	sha := strings.TrimSpace(string(mustCmd(t, "git", "-C", repoDir, "rev-parse", "HEAD")))

	evt := event.Event{
		Type: event.TypeGitPush,
		Repo: "demo",
		Payload: map[string]interface{}{
			"changes": map[string]interface{}{
				"refs/heads/main": map[string]interface{}{"new": sha},
			},
		},
	}
	if got := IntentForEvent(root, evt); got != "intent-7" {
		t.Errorf("IntentForEvent = %q, want intent-7", got)
	}

	// Non-push events never carry intent.
	if got := IntentForEvent(root, event.Event{Type: event.TypeGitTag, Repo: "demo"}); got != "" {
		t.Errorf("tag event intent = %q, want empty", got)
	}
}

func mustCmd(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out
}
