// Copyright (c) 2025 JoeGlenn1213
// ActionD Dispatcher - Routes events to matching plugins

package dispatcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/repopath"
)

// Dispatcher routes events to matching plugins
// Core responsibility: "Which plugins should handle this event?"
type Dispatcher struct {
	plugins        []plugin.Plugin
	repoRoot       string
	activeProfile  string            // Execution profile: "fast", "full", "release"
	enabledChecker func(string) bool // Callback to check if plugin is enabled
	mu             sync.RWMutex

	reposDir     string // LGH bare repository root; enables isolated checkouts
	checkoutRoot string // root for isolated checkouts
}

// SetCheckoutDirs enables isolated checkouts for language detection so
// dispatch never keys off "future" code in the working tree.
func (d *Dispatcher) SetCheckoutDirs(reposDir, checkoutRoot string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reposDir = reposDir
	d.checkoutRoot = checkoutRoot
}

// New creates a new dispatcher with the given plugins
func New(plugins []plugin.Plugin, repoRoot string) *Dispatcher {
	return &Dispatcher{
		plugins:        plugins,
		repoRoot:       repoRoot,
		activeProfile:  "fast",                            // Default to fast profile
		enabledChecker: func(string) bool { return true }, // Default: all enabled
	}
}

// SetEnabledChecker sets the callback function to check if a plugin is enabled
func (d *Dispatcher) SetEnabledChecker(checker func(string) bool) {
	d.enabledChecker = checker
}

// SetPlugins replaces the active plugin list after a reload.
func (d *Dispatcher) SetPlugins(plugins []plugin.Plugin) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.plugins = plugins
}

// SetProfile sets the active execution profile ("fast", "full", "release").
func (d *Dispatcher) SetProfile(profile string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.activeProfile = profile
}

// Profile returns the current active execution profile.
func (d *Dispatcher) Profile() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.activeProfile
}

// Dispatch finds all plugins that should handle this event
func (d *Dispatcher) Dispatch(e event.Event) []plugin.Plugin {
	d.mu.RLock()
	reposDir, checkoutRoot := d.reposDir, d.checkoutRoot
	d.mu.RUnlock()

	// Resolve repo path for language detection. Prefer an isolated checkout
	// of the pushed sha so detection reflects the pushed code, not the
	// developer's current (possibly newer/dirtier) working tree.
	repoPath := repopath.Resolve(d.repoRoot, e.Repo)
	if sha := event.SHAFromEvent(e); sha != "" && reposDir != "" {
		if p, err := repopath.Checkout(reposDir, e.Repo, sha, checkoutRoot); err == nil {
			repoPath = p
		}
	}

	// Detect project language
	languages := detectProjectLanguage(repoPath)

	d.mu.RLock()
	plugins := append([]plugin.Plugin(nil), d.plugins...)
	d.mu.RUnlock()

	var matched []plugin.Plugin
	for _, p := range plugins {
		// Check if plugin is enabled (via ConfigManager callback)
		if !d.enabledChecker(p.Name()) {
			continue
		}

		// Check trigger match
		triggerMatched := false
		for _, t := range p.Triggers() {
			if t == e.Type {
				triggerMatched = true
				break
			}
		}
		if !triggerMatched {
			continue
		}

		// Check language match
		if !matchesLanguage(p.Languages(), languages) {
			continue
		}

		// Check profile match: if plugin defines profiles, it must include the active one
		if pProfiles := p.Profiles(); len(pProfiles) > 0 {
			if !containsString(pProfiles, d.activeProfile) {
				continue
			}
		}

		// Check plugin-specific match logic
		if p.Match(e) {
			matched = append(matched, p)
		}
	}
	return matched
}

// detectProjectLanguage detects programming languages in the repository
func detectProjectLanguage(repoPath string) []string {
	languages := []string{}
	add := func(lang string) {
		for _, existing := range languages {
			if existing == lang {
				return
			}
		}
		languages = append(languages, lang)
	}

	// Check for Java
	if hasAnyFile(repoPath, "pom.xml", "build.gradle", "build.gradle.kts") {
		add("java")
	}

	// Check for Go
	if hasAnyFile(repoPath, "go.mod") {
		add("go")
	}

	// Check for Python
	if hasAnyFile(repoPath, "requirements.txt", "setup.py", "pyproject.toml") {
		add("python")
	}

	// Check for Node/Web projects
	if hasAnyFile(repoPath, "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock") {
		add("node")
		add("web")
		add("javascript")
	}

	// Refine frontend stack tags
	if hasAnyFile(repoPath, "tsconfig.json", "tsconfig.app.json", "tsconfig.build.json") {
		add("typescript")
		add("web")
	}
	if hasAnyFile(repoPath, "next.config.js", "next.config.mjs", "next.config.ts") {
		add("nextjs")
		add("web")
	}

	// If no specific language detected, return empty (will match wildcard plugins only)
	return languages
}

// hasAnyFile checks if any of the given files exist in the repo
func hasAnyFile(repoPath string, files ...string) bool {
	for _, file := range files {
		path := filepath.Join(repoPath, file)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// containsString checks if a string slice contains a given string.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

// matchesLanguage checks if plugin languages match project languages
func matchesLanguage(pluginLangs, projectLangs []string) bool {
	// Wildcard: plugin runs on all projects
	for _, pl := range pluginLangs {
		if pl == "*" {
			return true
		}
	}

	// If no project languages detected, only wildcard plugins match
	if len(projectLangs) == 0 {
		return false
	}

	// Check for overlap
	for _, pl := range pluginLangs {
		for _, proj := range projectLangs {
			if strings.EqualFold(pl, proj) {
				return true
			}
		}
	}

	return false
}
