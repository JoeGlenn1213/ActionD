package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDiscovery(t *testing.T) {
	d := NewDiscovery("/path/to/plugins", "/another/path")

	if len(d.pluginDirs) != 2 {
		t.Errorf("Expected 2 plugin dirs, got %d", len(d.pluginDirs))
	}
	if d.pluginDirs[0] != "/path/to/plugins" {
		t.Errorf("First dir mismatch: got %s", d.pluginDirs[0])
	}
	if d.pluginDirs[1] != "/another/path" {
		t.Errorf("Second dir mismatch: got %s", d.pluginDirs[1])
	}
}

func TestNewDiscoveryEmpty(t *testing.T) {
	d := NewDiscovery()

	if len(d.pluginDirs) != 0 {
		t.Errorf("Expected 0 plugin dirs, got %d", len(d.pluginDirs))
	}
}

func TestScanAllNonExistentDir(t *testing.T) {
	d := NewDiscovery("/nonexistent/directory/12345")
	plugins, err := d.ScanAll()

	if err != nil {
		t.Fatalf("ScanAll should not error on non-existent dir: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("Expected 0 plugins for non-existent dir, got %d", len(plugins))
	}
}

func TestScanAllWithValidManifest(t *testing.T) {
	// Create temp directory with valid manifest
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test-plugin")
	err := os.MkdirAll(pluginDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create plugin dir: %v", err)
	}

	// Create valid manifest
	manifest := `{
		"name": "test-plugin",
		"version": "1.0.0",
		"description": "Test plugin",
		"command": "./run.sh",
		"triggers": ["git.push"],
		"languages": ["go"]
	}`
	err = os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0644)
	if err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	d := NewDiscovery(tmpDir)
	plugins, err := d.ScanAll()

	if err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}
	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin, got %d", len(plugins))
	}
	if len(plugins) > 0 && plugins[0].Name() != "test-plugin" {
		t.Errorf("Plugin name mismatch: got %s", plugins[0].Name())
	}
}

func TestScanAllDuplicatePlugins(t *testing.T) {
	// Create temp directory with two plugins of same name
	tmpDir := t.TempDir()
	pluginDir1 := filepath.Join(tmpDir, "plugin1")
	pluginDir2 := filepath.Join(tmpDir, "plugin2")
	_ = os.MkdirAll(pluginDir1, 0755)
	_ = os.MkdirAll(pluginDir2, 0755)

	manifest := `{
		"name": "same-name",
		"version": "1.0.0",
		"command": "./run.sh",
		"triggers": ["git.push"],
		"languages": ["go"]
	}`
	_ = os.WriteFile(filepath.Join(pluginDir1, "manifest.json"), []byte(manifest), 0644)
	_ = os.WriteFile(filepath.Join(pluginDir2, "manifest.json"), []byte(manifest), 0644)

	d := NewDiscovery(tmpDir)
	plugins, err := d.ScanAll()

	if err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}
	// Should only have 1 plugin (duplicate skipped)
	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin (duplicates skipped), got %d", len(plugins))
	}
}

func TestScanDirectory(t *testing.T) {
	// Create temp directory with nested structure
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "subdir", "nested", "plugin")
	_ = os.MkdirAll(pluginDir, 0755)

	manifest := `{
		"name": "nested-plugin",
		"version": "1.0.0",
		"command": "./run.sh",
		"triggers": ["git.push"],
		"languages": ["go"]
	}`
	_ = os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0644)

	plugins, err := ScanDirectory(tmpDir)

	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}
	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin, got %d", len(plugins))
	}
}

func TestScanDirectoryInvalidManifest(t *testing.T) {
	// Create temp directory with invalid manifest
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "bad-plugin")
	_ = os.MkdirAll(pluginDir, 0755)

	invalidManifest := `{"name": "test"}` // Missing required fields
	_ = os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(invalidManifest), 0644)

	// Should not error, just skip invalid manifest
	plugins, err := ScanDirectory(tmpDir)

	if err != nil {
		t.Fatalf("ScanDirectory should not error on invalid manifest: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("Expected 0 plugins for invalid manifest, got %d", len(plugins))
	}
}

func TestScanDirectoryNoManifest(t *testing.T) {
	tmpDir := t.TempDir()
	// No manifest.json file
	_ = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644)

	plugins, err := ScanDirectory(tmpDir)

	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("Expected 0 plugins for dir without manifest, got %d", len(plugins))
	}
}

func TestScanAllMultipleDirs(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	// Create plugin in first dir
	pluginDir1 := filepath.Join(tmpDir1, "plugin1")
	_ = os.MkdirAll(pluginDir1, 0755)
	manifest1 := `{
		"name": "plugin-one",
		"version": "1.0.0",
		"command": "./run.sh",
		"triggers": ["git.push"],
		"languages": ["go"]
	}`
	_ = os.WriteFile(filepath.Join(pluginDir1, "manifest.json"), []byte(manifest1), 0644)

	// Create plugin in second dir
	pluginDir2 := filepath.Join(tmpDir2, "plugin2")
	_ = os.MkdirAll(pluginDir2, 0755)
	manifest2 := `{
		"name": "plugin-two",
		"version": "1.0.0",
		"command": "./run.sh",
		"triggers": ["git.push"],
		"languages": ["go"]
	}`
	_ = os.WriteFile(filepath.Join(pluginDir2, "manifest.json"), []byte(manifest2), 0644)

	d := NewDiscovery(tmpDir1, tmpDir2)
	plugins, err := d.ScanAll()

	if err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}
	if len(plugins) != 2 {
		t.Errorf("Expected 2 plugins, got %d", len(plugins))
	}
}
