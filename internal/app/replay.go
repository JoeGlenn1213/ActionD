// Copyright (c) 2025 JoeGlenn1213
// ActionD App - startup event-gap replay (ASSURANCE §7 前置债)

package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/dispatcher"
	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/store"
	"github.com/JoeGlenn1213/actiond/internal/worker"
)

// replayOverlapBuffer allows a small overlap window: events slightly older
// than the watermark may be dispatched again, but the worker's dispatch
// dedup index makes that overlap a cheap no-op. A fresh install (no jobs at
// all) skips replay entirely — replaying the whole history would duplicate
// real work.
const replayOverlapBuffer = 30 * time.Second

// maxReplayEvents caps how many lines of the lgh event log we consider.
const maxReplayEvents = 200

// defaultEventsLogPath resolves the lgh event log on this machine.
func defaultEventsLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".localgithub", "events", "events.jsonl")
	}
	return filepath.Join(home, ".localgithub", "events", "events.jsonl")
}

// replayMissedEvents dispatches events from lgh's events.jsonl that arrived
// after the newest persisted job (the watermark). Called once at startup,
// after pending-job recovery, so an ActionD restart no longer loses the
// pushes that happened while it was down (idempotency covers overlaps).
func replayMissedEvents(jobStore store.Store, disp *dispatcher.Dispatcher, work *worker.Worker, eventsPath, repoRoot string) int {
	if eventsPath == "" {
		eventsPath = defaultEventsLogPath()
	}

	watermark := newestJobTime(jobStore)
	if watermark.IsZero() {
		fmt.Println("   ↺  replay skipped: no persisted jobs (fresh install)")
		return 0
	}

	events, err := readRecentEvents(eventsPath, maxReplayEvents)
	if err != nil {
		fmt.Printf("⚠️  replay skipped: %v\n", err)
		return 0
	}

	replayed := 0
	for _, evt := range events {
		if evt.Timestamp.Before(watermark.Add(-replayOverlapBuffer)) {
			continue
		}
		replayed++
		fmt.Printf("   ↺  replaying: %s [%s] (%s)\n", evt.Type, evt.Repo, evt.Timestamp.Format(time.RFC3339))
		dispatchEvent(disp, work, evt, repoRoot)
	}
	return replayed
}

// newestJobTime returns the created_at of the newest persisted job, or the
// zero time when the store is empty.
func newestJobTime(jobStore store.Store) time.Time {
	jobs, err := jobStore.ListJobs(1)
	if err != nil || len(jobs) == 0 {
		return time.Time{}
	}
	return jobs[0].CreatedAt
}

// readRecentEvents parses the tail of the lgh event log (JSONL) into events,
// newest last. Malformed lines are skipped — the log is append-only and one
// bad line must not block replay of everything after it.
func readRecentEvents(path string, limit int) ([]event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no events yet — nothing to replay
		}
		return nil, fmt.Errorf("open events log: %w", err)
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read events log: %w", err)
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	events := make([]event.Event, 0, len(lines))
	for _, line := range lines {
		var evt event.Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue // skip malformed line, keep the rest
		}
		if evt.ID == "" || evt.Type == "" {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}
