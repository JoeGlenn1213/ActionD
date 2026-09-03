// Copyright (c) 2025 JoeGlenn1213
// ActionD Plugin Manifest - Plugin metadata definition and parsing

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest represents plugin metadata from manifest.json
type Manifest struct {
	// APIVersion is the version of the manifest schema
	APIVersion string `json:"apiVersion"`
	// Name is the unique identifier for the plugin
	Name string `json:"name"`
	// Version is the plugin version (semver recommended)
	Version string `json:"version"`
	// Description is a human-readable description
	Description string `json:"description"`
	// Command is the executable to run
	Command string `json:"command"`
	// Args are the arguments to pass to the command
	Args []string `json:"args"`
	// Triggers are the event types that activate this plugin
	Triggers []string `json:"triggers"`
	// Languages are the programming languages this plugin supports (["*"] for all)
	Languages []string `json:"languages"`
	// Timeout is the maximum execution time (e.g., "5m", "30s")
	Timeout string `json:"timeout"`
	// WorkingDir is the working directory relative to manifest.json
	WorkingDir string `json:"workingDir"`
	// RefFilter is a glob pattern for matching refs (e.g., "refs/tags/*")
	RefFilter string `json:"refFilter"`
	// RepoFilter is a glob pattern for matching repository names
	// (e.g. "demo-api.git" or "*-web.git").
	RepoFilter string `json:"repoFilter"`
	// SupportedProfiles lists the execution profiles this plugin belongs to
	// (e.g. ["fast", "full", "release"]). Empty means all profiles.
	SupportedProfiles []string `json:"supported_profiles"`
	// Env are environment variables to set for the plugin
	Env map[string]string `json:"env"`
	// Artifacts are the expected output files
	Artifacts []string `json:"artifacts"`
}

// ParseManifest reads and parses a manifest.json file
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	return &m, nil
}

// Validate checks that the manifest has all required fields
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest 'name' is required")
	}
	if m.Command == "" {
		return fmt.Errorf("manifest 'command' is required")
	}
	if len(m.Triggers) == 0 {
		return fmt.Errorf("manifest 'triggers' must have at least one trigger")
	}
	return nil
}

// ToExecPluginConfig converts Manifest to ExecPluginConfig
func (m *Manifest) ToExecPluginConfig(baseDir string) ExecPluginConfig {
	// Parse timeout duration
	timeout, _ := time.ParseDuration(m.Timeout)
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	// Resolve working directory
	workingDir := m.WorkingDir
	if workingDir != "" && !filepath.IsAbs(workingDir) {
		workingDir = filepath.Join(baseDir, m.WorkingDir)
	} else if workingDir == "" {
		workingDir = baseDir
	}

	// Default languages to wildcard if not specified
	languages := m.Languages
	if len(languages) == 0 {
		languages = []string{"*"}
	}

	return ExecPluginConfig{
		Name:       m.Name,
		Version:    m.Version,
		Command:    m.Command,
		Args:       m.Args,
		Triggers:   m.Triggers,
		Languages:  languages,
		Timeout:    timeout,
		WorkingDir: workingDir,
		RefFilter:  m.RefFilter,
		RepoFilter: m.RepoFilter,
		Profiles:   m.SupportedProfiles,
	}
}
