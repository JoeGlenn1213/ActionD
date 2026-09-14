// Copyright (c) 2025 JoeGlenn1213
// ActionD - builtin plugin materialization
// Licensed under MIT

package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// excludedPlugins are bundled in the source tree but never materialized into
// a user install: org-specific gates and upstream-disabled entries that do
// not belong in a public setup. Keep this list explicit — anything added to
// plugins/ without a manifest or without a reason to ship publicly should be
// listed here or left manifest-less.
var excludedPlugins = map[string]bool{
	"nanjie-client-gate": true, // org-specific desktop-client gate, no repoFilter
	"town-runtime-test":  true, // internal repo only (repoFilter town-runtime.git)
	"deepwiki-action":    true, // disabled upstream (enabled: false)
}

// InstallBuiltinPlugins materializes the embedded plugin bundle into dst
// (typically ~/.localgithub/plugins). Only directories carrying a
// manifest.json count as plugins; excluded entries are skipped entirely.
//
// Safety rules:
//   - a plugin whose dst manifest.json already exists is left untouched
//     (user's local edits and versions always win) unless force is set;
//   - every bundled plugin runs as "python3 run.py", so plain 0644 copies
//     need no executable bit.
//
// It returns the plugin names installed and the names already present.
func InstallBuiltinPlugins(fsys fs.FS, dst string, force bool) (installed, skipped []string, err error) {
	entries, err := fs.ReadDir(fsys, "plugins")
	if err != nil {
		return nil, nil, fmt.Errorf("read embedded plugins: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || excludedPlugins[name] {
			continue
		}
		// Only manifest-bearing directories are installable plugins; the
		// tree also carries non-plugin sources (common/, echo/, governance/).
		manifestPath := filepath.Join("plugins", name, "manifest.json")
		if _, statErr := fs.Stat(fsys, manifestPath); statErr != nil {
			continue
		}

		pluginDst := filepath.Join(dst, name)
		if !force {
			if _, statErr := os.Stat(filepath.Join(pluginDst, "manifest.json")); statErr == nil {
				skipped = append(skipped, name)
				continue
			}
		}

		copyErr := fs.WalkDir(fsys, filepath.Join("plugins", name), func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return walkErr
			}
			rel, relErr := filepath.Rel("plugins", path)
			if relErr != nil {
				return relErr
			}
			target := filepath.Join(dst, rel)
			if !force {
				if _, statErr := os.Stat(target); statErr == nil {
					return nil // never overwrite an existing file
				}
			}
			return copyFile(fsys, path, target)
		})
		if copyErr != nil {
			return installed, skipped, fmt.Errorf("install plugin %s: %w", name, copyErr)
		}
		installed = append(installed, name)
	}
	return installed, skipped, nil
}

// copyFile writes one embedded file to target, creating parent dirs.
func copyFile(fsys fs.FS, src, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := fsys.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
