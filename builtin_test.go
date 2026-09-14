// Copyright (c) 2025 JoeGlenn1213
// ActionD - embedded plugin bundle parity test

package actiond

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPluginsFSMatchesDisk guards the go:embed pattern: every plugin file on
// disk (excluding editor/OS noise: dotfiles, __pycache__) must be present in
// the embedded bundle, and the bundle must not carry anything the source tree
// lacks. A silent embed-pattern rot (e.g. dir rename) fails here, not in a
// user's fresh install.
func TestPluginsFSMatchesDisk(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate source path")
	}
	repoRoot := filepath.Dir(thisFile)
	diskRoot := filepath.Join(repoRoot, "plugins")

	var diskFiles []string
	err := filepath.WalkDir(diskRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip noise dirs the default embed rules also skip.
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		diskFiles = append(diskFiles, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk disk plugins/: %v", err)
	}

	var embeddedFiles []string
	err = fs.WalkDir(PluginsFS, "plugins", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		embeddedFiles = append(embeddedFiles, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded plugins/: %v", err)
	}

	diskSet := make(map[string]bool, len(diskFiles))
	for _, f := range diskFiles {
		diskSet[f] = true
	}
	embedSet := make(map[string]bool, len(embeddedFiles))
	for _, f := range embeddedFiles {
		embedSet[f] = true
	}

	for _, f := range diskFiles {
		if !embedSet[f] {
			t.Errorf("file on disk but missing from embedded bundle: %s", f)
		}
	}
	for _, f := range embeddedFiles {
		if !diskSet[f] {
			t.Errorf("file in embedded bundle but not on disk: %s", f)
		}
	}
	if len(diskFiles) == 0 {
		t.Fatal("no plugin files found on disk — fixture broken")
	}
}
