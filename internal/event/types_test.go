package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventJSON(t *testing.T) {
	event := Event{
		ID:        "evt-123",
		Type:      TypeGitPush,
		Repo:      "test/repo",
		Ref:       "refs/heads/main",
		Actor:     "developer",
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if parsed.ID != event.ID {
		t.Errorf("ID mismatch: got %s", parsed.ID)
	}
	if parsed.Type != event.Type {
		t.Errorf("Type mismatch: got %s", parsed.Type)
	}
	if parsed.Repo != event.Repo {
		t.Errorf("Repo mismatch: got %s", parsed.Repo)
	}
	if parsed.Ref != event.Ref {
		t.Errorf("Ref mismatch: got %s", parsed.Ref)
	}
}

func TestEventWithPayload(t *testing.T) {
	event := Event{
		ID:   "evt-456",
		Type: TypeGitPush,
		Repo: "test/repo",
		Payload: map[string]interface{}{
			"commits":     5,
			"branch":      "main",
			"has_secrets": false,
		},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if parsed.Payload == nil {
		t.Fatal("Payload should not be nil")
	}
	if parsed.Payload["commits"] != float64(5) {
		t.Errorf("Payload commits mismatch")
	}
}

func TestEventTypeConstants(t *testing.T) {
	if TypeRepoAdded != "repo.added" {
		t.Errorf("TypeRepoAdded mismatch")
	}
	if TypeRepoRemoved != "repo.removed" {
		t.Errorf("TypeRepoRemoved mismatch")
	}
	if TypeGitPush != "git.push" {
		t.Errorf("TypeGitPush mismatch")
	}
	if TypeGitTag != "git.tag" {
		t.Errorf("TypeGitTag mismatch")
	}
}

func TestEventGitTag(t *testing.T) {
	event := Event{
		ID:    "evt-tag",
		Type:  TypeGitTag,
		Repo:  "test/repo",
		Ref:   "refs/tags/v1.0.0",
		New:   "abc123def",
		Actor: "developer",
	}

	if event.Type != TypeGitTag {
		t.Errorf("Expected TypeGitTag")
	}
	if event.Ref == "" {
		t.Error("Tag event should have Ref")
	}
}

func TestEventGitPush(t *testing.T) {
	event := Event{
		ID:   "evt-push",
		Type: TypeGitPush,
		Repo: "test/repo",
		Ref:  "refs/heads/main",
		Old:  "oldcommit",
		New:  "newcommit",
	}

	if event.Type != TypeGitPush {
		t.Errorf("Expected TypeGitPush")
	}
	if event.Old == "" || event.New == "" {
		t.Error("Push event should have Old and New")
	}
}

func TestEventRepoAdded(t *testing.T) {
	event := Event{
		ID:    "evt-add",
		Type:  TypeRepoAdded,
		Repo:  "new/repo",
		Actor: "admin",
	}

	if event.Type != TypeRepoAdded {
		t.Errorf("Expected TypeRepoAdded")
	}
}

func TestEventReplayed(t *testing.T) {
	event := Event{
		ID:       "evt-replay",
		Type:     TypeGitPush,
		Repo:     "test/repo",
		Replayed: true,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Replayed field uses _replayed in JSON
	if !parsed.Replayed {
		t.Error("Replayed should be true")
	}
}

func TestEventEmptyPayload(t *testing.T) {
	event := Event{
		ID:        "evt-empty",
		Type:      TypeGitPush,
		Repo:      "test/repo",
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if parsed.Payload != nil {
		t.Error("Empty payload should be nil, not empty map")
	}
}

func TestNewSocketSource(t *testing.T) {
	// Test with explicit path
	source := NewSocketSource("/tmp/test.sock")
	if source == nil {
		t.Fatal("NewSocketSource should not return nil")
	}
	if source.sockPath != "/tmp/test.sock" {
		t.Errorf("Expected /tmp/test.sock, got %s", source.sockPath)
	}
	if source.events == nil {
		t.Error("events channel should be initialized")
	}
	if source.done == nil {
		t.Error("done channel should be initialized")
	}
}

func TestNewSocketSourceDefaultPath(t *testing.T) {
	// Test with empty path (uses default)
	source := NewSocketSource("")
	if source == nil {
		t.Fatal("NewSocketSource should not return nil")
	}

	// Should use default path
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".localgithub", "lgh.sock")
	if source.sockPath != expected {
		t.Errorf("Expected default path %s, got %s", expected, source.sockPath)
	}
}

func TestSocketSourceEvents(t *testing.T) {
	source := NewSocketSource("/tmp/test.sock")
	ch := source.Events()
	if ch == nil {
		t.Error("Events() should return channel")
	}
}

func TestSocketSourceCloseWithoutStart(t *testing.T) {
	source := NewSocketSource("/tmp/test.sock")
	// Closing without starting should not error
	err := source.Close()
	if err != nil {
		t.Errorf("Close() should not error: %v", err)
	}
}

func TestSocketSourceCloseWithNilConn(t *testing.T) {
	source := &SocketSource{
		sockPath: "/tmp/test.sock",
		events:   make(chan Event),
		done:     make(chan struct{}),
		conn:     nil, // no connection
	}
	err := source.Close()
	if err != nil {
		t.Errorf("Close() should not error with nil conn: %v", err)
	}
}

func TestSocketSourceCloseConn(t *testing.T) {
	// Test closeConn with nil connection (should not panic)
	source := &SocketSource{
		sockPath: "/tmp/test.sock",
		events:   make(chan Event),
		done:     make(chan struct{}),
		conn:     nil,
	}
	// closeConn is unexported, but we can test via Close
	_ = source.Close()
}

func TestEventWithOldAndNew(t *testing.T) {
	event := Event{
		ID:   "evt-push",
		Type: TypeGitPush,
		Repo: "test/repo",
		Ref:  "refs/heads/main",
		Old:  "aaa111",
		New:  "bbb222",
	}

	if event.Old != "aaa111" {
		t.Errorf("Old mismatch: got %s", event.Old)
	}
	if event.New != "bbb222" {
		t.Errorf("New mismatch: got %s", event.New)
	}
}

func TestEventActor(t *testing.T) {
	event := Event{
		ID:    "evt-1",
		Type:  TypeGitPush,
		Repo:  "test/repo",
		Actor: "developer@example.com",
	}

	if event.Actor != "developer@example.com" {
		t.Errorf("Actor mismatch: got %s", event.Actor)
	}
}

func TestEventTimestamp(t *testing.T) {
	now := time.Now()
	event := Event{
		ID:        "evt-1",
		Type:      TypeGitPush,
		Repo:      "test/repo",
		Timestamp: now,
	}

	if !event.Timestamp.Equal(now) {
		t.Errorf("Timestamp mismatch")
	}
}

func TestEventMarshalAllFields(t *testing.T) {
	event := Event{
		ID:        "evt-full",
		Type:      TypeGitTag,
		Repo:      "my/repo",
		Ref:       "refs/tags/v1.0.0",
		Old:       "oldtag",
		New:       "newtag",
		Actor:     "user",
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Replayed:  true,
		Payload: map[string]interface{}{
			"key": "value",
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if parsed.ID != event.ID {
		t.Errorf("ID mismatch")
	}
	if parsed.Type != event.Type {
		t.Errorf("Type mismatch")
	}
	if parsed.Repo != event.Repo {
		t.Errorf("Repo mismatch")
	}
	if parsed.Ref != event.Ref {
		t.Errorf("Ref mismatch")
	}
	if parsed.Old != event.Old {
		t.Errorf("Old mismatch")
	}
	if parsed.New != event.New {
		t.Errorf("New mismatch")
	}
	if parsed.Actor != event.Actor {
		t.Errorf("Actor mismatch")
	}
	if !parsed.Replayed {
		t.Errorf("Replayed should be true")
	}
}
