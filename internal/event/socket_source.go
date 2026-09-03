// Copyright (c) 2025 JoeGlenn1213
// ActionD Socket Source - Connects to LGH's lgh.sock

package event

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// SocketSource subscribes to LGH's Unix Domain Socket for real-time events
type SocketSource struct {
	sockPath string
	conn     net.Conn
	events   chan Event
	done     chan struct{}
}

// NewSocketSource creates a new socket-based event source
func NewSocketSource(sockPath string) *SocketSource {
	if sockPath == "" {
		// Default to LGH's socket path
		home, _ := os.UserHomeDir()
		sockPath = filepath.Join(home, ".localgithub", "lgh.sock")
	}
	return &SocketSource{
		sockPath: sockPath,
		events:   make(chan Event, 100),
		done:     make(chan struct{}),
	}
}

// Start connects to the socket and begins receiving events
func (s *SocketSource) Start(ctx context.Context) error {
	conn, err := net.Dial("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("failed to connect to LGH socket at %s: %w", s.sockPath, err)
	}
	s.conn = conn

	go s.readLoop(ctx)

	// Ensure connection is closed when context is cancelled to unblock Read
	go func() {
		select {
		case <-ctx.Done():
			s.closeConn()
		case <-s.done:
			s.closeConn()
		}
	}()

	return nil
}

func (s *SocketSource) readLoop(ctx context.Context) {
	defer close(s.events)

	reader := bufio.NewReader(s.conn)
	decoder := json.NewDecoder(reader)

	for {
		var evt Event
		if err := decoder.Decode(&evt); err != nil {
			// Connection closed or error (e.g. context cancelled -> conn closed)
			return
		}

		select {
		case s.events <- evt:
		case <-ctx.Done():
			return
		case <-s.done:
			return
		}
	}
}

// Events returns the event channel
func (s *SocketSource) Events() <-chan Event {
	return s.events
}

// Close stops the source
func (s *SocketSource) Close() error {
	close(s.done)
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *SocketSource) closeConn() {
	if s.conn == nil {
		return
	}
	if err := s.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		fmt.Printf("failed to close LGH socket: %v\n", err)
	}
}
