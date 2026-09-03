// Copyright (c) 2025 JoeGlenn1213
// ActionD - Local AI Action Execution Engine
// Licensed under MIT

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SetupConfig holds configuration for setup command
type SetupConfig struct {
	SkipDeps    bool // Skip dependency checks
	SkipWeb     bool // Skip web assets setup
	StartDaemon bool // Start daemon after setup
	Force       bool // Force overwrite existing files
}

// SetupResult holds the result of setup operation
type SetupResult struct {
	DirsCreated  []string
	DirsExisting []string
	Warnings     []string
	Errors       []string
	Dependencies map[string]string // name -> version
}

// DefaultDirs returns the standard ActionD directory structure
func DefaultDirs() map[string]string {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".localgithub")

	return map[string]string{
		"base":      base,
		"repos":     filepath.Join(base, "repos"),
		"actions":   filepath.Join(base, "actions"),
		"plugins":   filepath.Join(base, "plugins"),
		"web":       filepath.Join(base, "actiond-web"),
		"webOut":    filepath.Join(base, "actiond-web", "out"),
		"artifacts": filepath.Join(base, "actions", "artifacts"),
	}
}

// RunSetup executes the setup process
func RunSetup(cfg SetupConfig) (*SetupResult, error) {
	result := &SetupResult{
		Dependencies: make(map[string]string),
	}

	// Step 1: Create directory structure
	fmt.Println("📁 Setting up directories...")
	dirs := DefaultDirs()
	for name, path := range dirs {
		if name == "base" {
			continue // Base dir will be created with subdirs
		}

		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			result.DirsExisting = append(result.DirsExisting, path)
			fmt.Printf("   ✓ %s exists: %s\n", name, path)
		} else if os.IsNotExist(err) {
			if err := os.MkdirAll(path, 0755); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to create %s: %v", path, err))
				fmt.Printf("   ✗ Failed to create %s: %v\n", name, err)
			} else {
				result.DirsCreated = append(result.DirsCreated, path)
				fmt.Printf("   + Created %s: %s\n", name, path)
			}
		}
	}

	// Step 2: Check dependencies
	if !cfg.SkipDeps {
		fmt.Println("\n🔍 Checking dependencies...")
		checkDeps(result)
	}

	// Step 3: Check web assets
	if !cfg.SkipWeb {
		fmt.Println("\n🌐 Checking web assets...")
		webDir := DetectDefaultWebDir()
		if webDir != "" {
			fmt.Printf("   ✓ Web assets found: %s\n", webDir)
		} else {
			msg := "Web assets not found. Install actiond-web or pass --web-dir when starting"
			result.Warnings = append(result.Warnings, msg)
			fmt.Printf("   ⚠ %s\n", msg)
		}
	}

	// Check LGH Server
	fmt.Println("\n🔌 Checking LGH connection...")
	sockPath := DetectLGHSocketPath()
	if sockPath != "" {
		fmt.Printf("   ✓ LGH socket found: %s\n", sockPath)
	} else {
		msg := "LGH socket not found. Make sure LGH is initialized and running ('lgh init' then 'lgh serve -d')"
		result.Warnings = append(result.Warnings, msg)
		fmt.Printf("   ⚠ %s\n", msg)
	}

	return result, nil
}

func checkDeps(result *SetupResult) {
	// Check Git
	checkDep(result, "git", "git", "--version", true)

	// Check Python3
	checkDep(result, "python3", "python3", "--version", true)

	// Check Go
	checkDep(result, "go", "go", "version", false)

	// Check Node.js (optional)
	checkDep(result, "node", "node", "--version", false)

	// Check golangci-lint (optional but recommended for Go projects)
	checkDep(result, "golangci-lint", "golangci-lint", "version", false)

	// Check npm (optional)
	checkDep(result, "npm", "npm", "--version", false)
}

func checkDep(result *SetupResult, name, cmd string, args string, required bool) {
	path, err := exec.LookPath(cmd)
	if err != nil {
		msg := fmt.Sprintf("%s not found", name)
		if required {
			result.Errors = append(result.Errors, msg+" (required)")
			fmt.Printf("   ✗ %s (required)\n", name)
		} else {
			result.Warnings = append(result.Warnings, msg+" (optional)")
			fmt.Printf("   ⚠ %s (optional, not found)\n", name)
		}
		return
	}

	// Get version
	var version string
	if output, err := exec.Command(path, args).Output(); err == nil {
		version = strings.TrimSpace(string(output))
		// Take first line only
		if idx := strings.Index(version, "\n"); idx > 0 {
			version = version[:idx]
		}
		// Truncate long output
		if len(version) > 50 {
			version = version[:50] + "..."
		}
	}

	result.Dependencies[name] = version
	fmt.Printf("   ✓ %s: %s (%s)\n", name, version, path)
}

// SystemInfo returns basic system information
func SystemInfo() map[string]string {
	return map[string]string{
		"OS":      runtime.GOOS,
		"Arch":    runtime.GOARCH,
		"Home":    homeDir(),
		"BaseDir": filepath.Join(homeDir(), ".localgithub"),
	}
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
