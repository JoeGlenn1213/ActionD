package app

import (
	"os"
	"path/filepath"
)

// DetectPluginDirs returns existing plugin directories in priority order.
func DetectPluginDirs() []string {
	exePath, err := os.Executable()
	if err != nil {
		exePath = ""
	}
	exeDir := filepath.Dir(exePath)

	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	candidates := []string{
		filepath.Join(exeDir, "plugins"),
		filepath.Join(wd, "plugins"),
		filepath.Join(home, ".localgithub", "actiond", "plugins"),
		filepath.Join(home, ".localgithub", "plugins"),
	}

	// Non-nil even when nothing exists — callers (and tests) treat this as
	// a list that may legitimately be empty, not as a lookup failure.
	dirs := []string{}
	seen := make(map[string]bool)
	for _, dir := range candidates {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// DetectDefaultWebDir tries common locations for exported ActionD-Web assets.
func DetectDefaultWebDir() string {
	exePath, err := os.Executable()
	if err != nil {
		exePath = ""
	}
	exeDir := filepath.Dir(exePath)

	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	candidates := []string{
		filepath.Join(exeDir, "out"),
		filepath.Join(exeDir, "..", "share", "actiond-web", "out"),
		filepath.Join(exeDir, "..", "libexec", "actiond-web", "out"),
		filepath.Join(wd, "out"),
		filepath.Join(wd, "LGH-ActionD-Web", "out"),
		filepath.Join(wd, "..", "LGH-ActionD-Web", "out"),
		filepath.Join(home, ".localgithub", "actiond-web", "out"),
	}

	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !info.IsDir() {
			return dir
		}
	}

	return ""
}

// DetectLGHSocketPath returns the default LGH socket path if it exists.
func DetectLGHSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}

	sock := filepath.Join(home, ".localgithub", "lgh.sock")
	if _, err := os.Stat(sock); err == nil {
		return sock
	}
	return ""
}
