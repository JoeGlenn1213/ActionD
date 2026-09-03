// Copyright (c) 2025 JoeGlenn1213
// ActionD App - plugin override regression tests (ASSURANCE Phase B)

package app

import (
	"testing"

	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/server"
)

// TestApplyPluginOverridePreservesVersion guards the verifier-provenance
// roundtrip: rebuilding a plugin with config overrides must NOT drop the
// manifest version (regression: applyPluginOverride rebuilt via
// pluginFromConfig, which has no version field).
func TestApplyPluginOverridePreservesVersion(t *testing.T) {
	p := plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name:       "go-lint",
		Version:    "1.0.0",
		Command:    "python3",
		Args:       []string{"run.py"},
		Triggers:   []string{"git.push"},
		Languages:  []string{"go"},
		Profiles:   []string{"fast"},
		RepoFilter: "*.git",
	})

	// Override with a config that touches several fields (as the runtime
	// config.json enabled-flag entries do), but carries no version concept.
	got := applyPluginOverride(p, server.PluginConfig{
		Enabled: boolPtr(true),
	})
	if got.Version() != "1.0.0" {
		t.Fatalf("override dropped plugin version: got %q, want 1.0.0", got.Version())
	}
	// Overridden fields must still win.
	if got.(*plugin.ExecPlugin).RepoFilter() != "*.git" {
		t.Errorf("repo filter lost: %q", got.(*plugin.ExecPlugin).RepoFilter())
	}
}

func boolPtr(b bool) *bool { return &b }
