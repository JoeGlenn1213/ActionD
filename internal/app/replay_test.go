// Copyright (c) 2025 JoeGlenn1213
// ActionD App - event-gap replay tests (ASSURANCE §7 前置债)

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/artifact"
	"github.com/JoeGlenn1213/actiond/internal/dispatcher"
	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/store"
	"github.com/JoeGlenn1213/actiond/internal/worker"
)

func writeEventsLog(t *testing.T, dir string, events []event.Event) string {
	t.Helper()
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, evt := range events {
		b, _ := json.Marshal(evt)
		_, _ = f.Write(append(b, '\n'))
	}
	return path
}

// TestReadRecentEvents: parses the tail, skips malformed lines, keeps order.
func TestReadRecentEvents(t *testing.T) {
	dir := t.TempDir()
	mk := func(id string, ts time.Time) event.Event {
		return event.Event{ID: id, Type: event.TypeGitPush, Repo: "repo/x", Timestamp: ts}
	}
	now := time.Now()
	path := filepath.Join(dir, "events.jsonl")
	f, _ := os.Create(path)
	_, _ = f.WriteString("{not json}\n")
	for _, e := range []event.Event{mk("e1", now), mk("e2", now.Add(time.Minute)), mk("e3", now.Add(2*time.Minute))} {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	events, err := readRecentEvents(path, 10)
	if err != nil {
		t.Fatalf("readRecentEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (malformed line skipped)", len(events))
	}
	if events[0].ID != "e1" || events[2].ID != "e3" {
		t.Errorf("order wrong: %v", events)
	}
}

// TestNewestJobTime: empty store → zero time; seeded store → newest created.
func TestNewestJobTime(t *testing.T) {
	st := store.NewMemoryStore()
	if !newestJobTime(st).IsZero() {
		t.Error("empty store must yield zero watermark")
	}
	now := time.Now()
	old := job.NewJob("evt-old", "repo/x", "p")
	old.CreatedAt = now.Add(-time.Hour)
	newer := job.NewJob("evt-new", "repo/x", "p")
	newer.CreatedAt = now
	_ = st.AddJob(old)
	_ = st.AddJob(newer)

	if got := newestJobTime(st); !got.Equal(now) {
		t.Errorf("watermark = %v, want %v", got, now)
	}
}

// TestReplayMissedEvents: only events newer than the watermark are
// dispatched; older ones are skipped; a fresh install replays nothing.
func TestReplayMissedEvents(t *testing.T) {
	echoPlugin := plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name:      "echo-test",
		Command:   "/bin/echo",
		Args:      []string{"ok"},
		Triggers:  []string{event.TypeGitPush},
		Languages: []string{"*"},
	})

	newStore := func() *store.MemoryStore { return store.NewMemoryStore() }
	now := time.Now()

	// Case 1: fresh install (no jobs) → nothing replayed.
	st := newStore()
	dir := t.TempDir()
	path := writeEventsLog(t, dir, []event.Event{
		{ID: "evt-a", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: now},
	})
	disp := dispatcher.New([]plugin.Plugin{echoPlugin}, dir)
	work := worker.NewWorker(8, artifact.NewStore(dir), st, dir)
	if n := replayMissedEvents(st, disp, work, path, dir); n != 0 {
		t.Errorf("fresh install must replay 0, got %d", n)
	}

	// Case 2: watermark exists → only newer events dispatch.
	st = newStore()
	seed := job.NewJob("seed-event", "repo/x", "echo-test")
	seed.CreatedAt = now
	_ = st.AddJob(seed)

	dir2 := t.TempDir()
	path2 := writeEventsLog(t, dir2, []event.Event{
		{ID: "evt-old", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: now.Add(-time.Hour)},
		{ID: "evt-new", Type: event.TypeGitPush, Repo: "repo/x", Timestamp: now.Add(time.Minute)},
	})
	disp2 := dispatcher.New([]plugin.Plugin{echoPlugin}, dir2)
	work2 := worker.NewWorker(8, artifact.NewStore(dir2), st, dir2)
	if n := replayMissedEvents(st, disp2, work2, path2, dir2); n != 1 {
		t.Fatalf("replay count = %d, want 1 (only evt-new)", n)
	}

	jobs, err := st.ListJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	var sawNew, sawOld bool
	for _, j := range jobs {
		switch j.EventID {
		case "evt-new":
			sawNew = true
		case "evt-old":
			sawOld = true
		}
	}
	if !sawNew {
		t.Error("evt-new was not dispatched")
	}
	if sawOld {
		t.Error("evt-old (before watermark) must not be dispatched")
	}
}
