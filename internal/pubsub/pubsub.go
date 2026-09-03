// Copyright (c) 2025 JoeGlenn1213
// ActionD PubSub - Simple publish-subscribe for progress streaming

package pubsub

import (
	"sync"
	"time"
)

// ProgressMessage represents a single progress update from a running job
type ProgressMessage struct {
	JobID     string    `json:"job_id"`
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`               // Log line or status message
	Progress  float64   `json:"progress,omitempty"` // Optional: 0-100 percentage
	IsError   bool      `json:"is_error,omitempty"` // True if from stderr
	Done      bool      `json:"done,omitempty"`     // True when job completes
}

// PubSub manages subscriptions for job progress updates
type PubSub struct {
	subscribers map[string][]chan ProgressMessage
	mu          sync.RWMutex
}

// New creates a new PubSub instance
func New() *PubSub {
	return &PubSub{
		subscribers: make(map[string][]chan ProgressMessage),
	}
}

// Subscribe creates a channel to receive progress updates for a specific job
// Returns the channel and a cleanup function
func (ps *PubSub) Subscribe(jobID string) (<-chan ProgressMessage, func()) {
	ch := make(chan ProgressMessage, 100) // Buffered to avoid blocking worker

	ps.mu.Lock()
	ps.subscribers[jobID] = append(ps.subscribers[jobID], ch)
	ps.mu.Unlock()

	cleanup := func() {
		ps.mu.Lock()
		defer ps.mu.Unlock()

		subs := ps.subscribers[jobID]
		for i, sub := range subs {
			if sub == ch {
				ps.subscribers[jobID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		close(ch)

		// Clean up empty subscriber list
		if len(ps.subscribers[jobID]) == 0 {
			delete(ps.subscribers, jobID)
		}
	}

	return ch, cleanup
}

// Publish sends a progress message to all subscribers of a job
func (ps *PubSub) Publish(msg ProgressMessage) {
	ps.mu.RLock()
	subs := ps.subscribers[msg.JobID]
	ps.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			// Channel full, skip to avoid blocking worker
		}
	}
}

// PublishLine is a convenience method to publish a single log line
func (ps *PubSub) PublishLine(jobID, line string, isError bool) {
	ps.Publish(ProgressMessage{
		JobID:     jobID,
		Timestamp: time.Now(),
		Line:      line,
		IsError:   isError,
	})
}

// PublishDone signals that a job has completed
func (ps *PubSub) PublishDone(jobID string) {
	ps.Publish(ProgressMessage{
		JobID:     jobID,
		Timestamp: time.Now(),
		Done:      true,
	})
}

// HasSubscribers checks if a job has any active subscribers
func (ps *PubSub) HasSubscribers(jobID string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.subscribers[jobID]) > 0
}
