package app

import (
	"testing"
)

func TestDetectPluginDirs(t *testing.T) {
	dirs := DetectPluginDirs()
	// Should return a list (may be empty if no dirs exist)
	if dirs == nil {
		t.Error("DetectPluginDirs should return non-nil slice")
	}
	// Should not contain duplicates
	seen := make(map[string]bool)
	for _, d := range dirs {
		if seen[d] {
			t.Errorf("Duplicate directory: %s", d)
		}
		seen[d] = true
	}
	// Should not contain empty strings
	for _, d := range dirs {
		if d == "" {
			t.Error("Should not contain empty directory")
		}
	}
}

func TestDetectPluginDirsNoCrash(t *testing.T) {
	// Should not panic even with unusual environment
	dirs := DetectPluginDirs()
	_ = dirs // Just verify no panic
}

func TestDetectDefaultWebDir(t *testing.T) {
	dir := DetectDefaultWebDir()
	// May be empty if no web assets found, but should not panic
	// Just verify no panic
	_ = dir
}

func TestDetectLGHSocketPath(t *testing.T) {
	path := DetectLGHSocketPath()
	// May be empty if socket doesn't exist
	// Just verify no panic
	_ = path
}
