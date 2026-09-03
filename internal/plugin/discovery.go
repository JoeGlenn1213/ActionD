// Copyright (c) 2025 JoeGlenn1213
// ActionD Plugin Discovery - Scan directories for plugin manifests

package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Discovery scans directories for plugin manifests
type Discovery struct {
	pluginDirs []string
}

// NewDiscovery creates a new plugin discovery instance
func NewDiscovery(pluginDirs ...string) *Discovery {
	return &Discovery{pluginDirs: pluginDirs}
}

// ScanAll scans all plugin directories and returns discovered plugins
func (d *Discovery) ScanAll() ([]Plugin, error) {
	var plugins []Plugin
	seen := make(map[string]bool) // Prevent duplicates

	for _, dir := range d.pluginDirs {
		// Skip non-existent directories
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		discovered, err := d.scanDirectory(dir)
		if err != nil {
			fmt.Printf("   ⚠️  Warning: failed to scan %s: %v\n", dir, err)
			continue
		}

		for _, p := range discovered {
			name := p.Name()
			if seen[name] {
				fmt.Printf("   ⚠️  Warning: duplicate plugin '%s', skipping\n", name)
				continue
			}
			seen[name] = true
			plugins = append(plugins, p)
		}
	}

	return plugins, nil
}

// scanDirectory scans a single directory for manifest.json files
func (d *Discovery) scanDirectory(dir string) ([]Plugin, error) {
	var plugins []Plugin

	// Walk the directory tree
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Look for manifest.json files
		if info.Name() == "manifest.json" {
			pluginDir := filepath.Dir(path)

			// Parse manifest
			manifest, err := ParseManifest(path)
			if err != nil {
				fmt.Printf("   ⚠️  Warning: failed to parse %s: %v\n", path, err)
				return nil
			}

			// Validate manifest
			if err := manifest.Validate(); err != nil {
				fmt.Printf("   ⚠️  Warning: invalid manifest %s: %v\n", path, err)
				return nil
			}

			// Convert to plugin
			cfg := manifest.ToExecPluginConfig(pluginDir)
			plugins = append(plugins, NewExecPlugin(cfg))

			fmt.Printf("   📦 Discovered plugin: %s (from %s)\n", manifest.Name, pluginDir)
		}

		return nil
	})

	return plugins, err
}

// ScanDirectory is a convenience function to scan a single directory
func ScanDirectory(dir string) ([]Plugin, error) {
	d := NewDiscovery(dir)
	return d.ScanAll()
}
