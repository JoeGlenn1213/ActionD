// Copyright (c) 2025 JoeGlenn1213
// ActionD Plugin Interface

package plugin

import (
	"context"

	"github.com/JoeGlenn1213/actiond/internal/event"
)

// Plugin defines the interface for action plugins
type Plugin interface {
	// Name returns the unique identifier of the plugin
	Name() string

	// Version returns the plugin manifest version (verifier provenance,
	// ASSURANCE Phase B). Empty when unknown.
	Version() string

	// Triggers returns the event types this plugin responds to
	Triggers() []string

	// Languages returns the programming languages this plugin supports
	// Use ["*"] for language-agnostic plugins
	Languages() []string

	// Match determines if this plugin should run for the given event
	Match(evt event.Event) bool

	// Profiles returns the supported execution profiles (e.g. "fast", "full", "release").
	// Empty or nil means "all profiles" (backward compatible).
	Profiles() []string

	// Run executes the plugin's action
	Run(ctx Context) error
}

// LogLineCallback is called for each line of output from a plugin
type LogLineCallback func(line string, isError bool)

// Context provides the execution environment for plugins
type Context struct {
	Ctx       context.Context
	Event     event.Event
	RepoPath  string   // Local path to the repository
	Diff      string   // Git diff content (if applicable)
	Files     []string // Changed files
	Artifacts ArtifactWriter
	Env       map[string]string
	LogWriter LogLineCallback // Optional callback for streaming output
}

// ArtifactWriter allows plugins to write output artifacts
type ArtifactWriter interface {
	Write(name string, data []byte) error
	WriteJSON(name string, v interface{}) error
	Dir() string
}
