// Copyright (c) 2025 JoeGlenn1213
// ActionD App - builtin plugin materialization tests

package app

import (
	"os"
	"path/filepath"
	"testing"

	actiond "github.com/JoeGlenn1213/actiond"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
)

func contains(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func TestInstallBuiltinPluginsFreshInstall(t *testing.T) {
	dst := t.TempDir()
	installed, skipped, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, false)
	if err != nil {
		t.Fatalf("InstallBuiltinPlugins: %v", err)
	}
	if len(installed) < 30 {
		t.Errorf("installed %d plugins, want >= 30 (bundle shrank?)", len(installed))
	}
	if len(skipped) != 0 {
		t.Errorf("fresh install reported skipped = %v, want none", skipped)
	}
	for _, want := range []string{"go-lint", "go-test-fast", "python-pytest", "web-lint"} {
		if !contains(installed, want) {
			t.Errorf("bundle missing %s (installed=%v)", want, installed)
		}
		if _, err := os.Stat(filepath.Join(dst, want, "manifest.json")); err != nil {
			t.Errorf("%s/manifest.json not materialized: %v", want, err)
		}
		if _, err := os.Stat(filepath.Join(dst, want, "run.py")); err != nil {
			t.Errorf("%s/run.py not materialized: %v", want, err)
		}
	}
	// Org-specific / disabled / manifest-less entries must never ship.
	for _, banned := range []string{"nanjie-client-gate", "town-runtime-test", "deepwiki-action", "echo", "common"} {
		if contains(installed, banned) {
			t.Errorf("excluded plugin %q was installed", banned)
		}
		if _, err := os.Stat(filepath.Join(dst, banned)); !os.IsNotExist(err) {
			t.Errorf("excluded dir %q exists in dst (err=%v)", banned, err)
		}
	}
}

func TestInstallBuiltinPluginsIdempotent(t *testing.T) {
	dst := t.TempDir()
	if _, _, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	installed, skipped, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("second run installed %v, want none", installed)
	}
	if len(skipped) == 0 {
		t.Error("second run reported no skipped plugins, want all")
	}
}

func TestInstallBuiltinPluginsNeverOverwritesUserFiles(t *testing.T) {
	dst := t.TempDir()
	if _, _, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Simulate a user edit to an installed plugin.
	manifest := filepath.Join(dst, "go-lint", "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"user":"edit"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, false); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"user":"edit"}` {
		t.Error("reinstall overwrote a user-edited manifest without --force")
	}

	// With force the bundle version is restored.
	if _, _, err := InstallBuiltinPlugins(actiond.PluginsFS, dst, true); err != nil {
		t.Fatalf("force reinstall: %v", err)
	}
	data, err = os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == `{"user":"edit"}` {
		t.Error("force reinstall did not restore the bundled manifest")
	}
}

func TestRunSetupInstallsPluginsEndToEnd(t *testing.T) {
	// Empty HOME = fresh install; setup must leave a discoverable plugin set.
	t.Setenv("HOME", t.TempDir())

	result, err := RunSetup(SetupConfig{SkipDeps: true, SkipWeb: true})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(result.PluginsInstalled) < 30 {
		t.Errorf("PluginsInstalled = %d, want >= 30", len(result.PluginsInstalled))
	}

	dirs := DetectPluginDirs()
	var found string
	for _, dir := range dirs {
		if filepath.Base(dir) == "plugins" {
			found = dir
			break
		}
	}
	if found == "" {
		t.Fatalf("DetectPluginDirs found no plugins dir under temp HOME: %v", dirs)
	}

	plugins, err := plugin.NewDiscovery(found).ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(plugins) < 30 {
		t.Errorf("discovered %d plugins in %s, want >= 30", len(plugins), found)
	}
	for _, p := range plugins {
		if excludedPlugins[p.Name()] {
			t.Errorf("excluded plugin %q discovered in a fresh install", p.Name())
		}
	}
}
