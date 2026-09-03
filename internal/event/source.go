// Copyright (c) 2025 JoeGlenn1213
// ActionD Event Source - Interface for receiving events

package event

import "context"

// Source defines the interface for receiving events
type Source interface {
	// Start begins listening for events
	Start(ctx context.Context) error

	// Events returns a channel of incoming events
	Events() <-chan Event

	// Close stops the source
	Close() error
}
