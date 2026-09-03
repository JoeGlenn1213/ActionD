// Copyright (c) 2025 JoeGlenn1213
// ActionD Event Types - Aligned with LGH event model

package event

import "time"

// Event represents an LGH event received from the socket
type Event struct {
	ID           string                 `json:"id"`
	Version      string                 `json:"version"` // Protocol version envelope (e.g. "1.0")
	Type         string                 `json:"type"`    // e.g., "git.push", "repo.added"
	Repo         string                 `json:"repo"`
	Ref          string                 `json:"ref,omitempty"`
	Old          string                 `json:"old,omitempty"`
	New          string                 `json:"new,omitempty"`
	Actor        string                 `json:"actor,omitempty"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	ChangedFiles []string               `json:"changed_files,omitempty"` // Files changed in this push
	Replayed     bool                   `json:"_replayed,omitempty"`
}

// Common event types from LGH
const (
	TypeRepoAdded   = "repo.added"
	TypeRepoRemoved = "repo.removed"
	TypeGitPush     = "git.push"
	TypeGitTag      = "git.tag" // Tag created/pushed
)

// SHAFromEvent returns the pushed commit sha from an lgh event payload.
// Covers the changes[] payload shape and the legacy flat "new" field.
func SHAFromEvent(evt Event) string {
	if changes, ok := evt.Payload["changes"].(map[string]interface{}); ok {
		for _, changeRaw := range changes {
			change, ok := changeRaw.(map[string]interface{})
			if !ok {
				continue
			}
			if newHash, ok := change["new"].(string); ok && newHash != "" {
				return newHash
			}
		}
	}
	if after, ok := evt.Payload["after"].(string); ok && after != "" {
		return after
	}
	return evt.New
}
