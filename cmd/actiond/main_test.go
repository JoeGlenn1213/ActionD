package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JoeGlenn1213/actiond/internal/server"
)

func TestSetPluginsEnabledWritesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	if _, err := setPluginsEnabled(coreGoPlugins, false); err != nil {
		t.Fatalf("disable core go plugins: %v", err)
	}
	if _, err := setPluginsEnabled(coreGoPlugins, true); err != nil {
		t.Fatalf("enable core go plugins: %v", err)
	}

	cm := server.NewConfigManager()
	for _, name := range coreGoPlugins {
		if !cm.IsPluginEnabled(name) {
			t.Fatalf("expected %s to be enabled", name)
		}
	}
}

func TestDisabledPluginsReturnsOnlyDisabledEntries(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	cm := server.NewConfigManager()
	if err := cm.SetPluginEnabled("go-lint", false); err != nil {
		t.Fatalf("disable go-lint: %v", err)
	}
	if err := cm.SetPluginEnabled("go-build", false); err != nil {
		t.Fatalf("disable go-build: %v", err)
	}
	if err := cm.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got := disabledPlugins(cm, coreGoPlugins)
	want := []string{"go-lint", "go-build"}

	if len(got) != len(want) {
		t.Fatalf("disabledPlugins() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("disabledPlugins()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSetPluginsEnabledRejectsUnknownPlugins(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	_, err := setPluginsEnabled([]string{"missing-plugin"}, true)
	if err == nil {
		t.Fatalf("expected unknown plugin to fail")
	}
	if !strings.Contains(err.Error(), "unknown plugin") {
		t.Fatalf("expected unknown plugin error, got %v", err)
	}
}

func TestSetPluginsEnabledAllowsCustomPluginFromConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	cm := server.NewConfigManager()
	err := cm.AddPlugin("custom-lint", server.PluginConfig{
		Type:     "exec",
		Command:  "python3",
		Args:     []string{"run.py"},
		Triggers: []string{"git.push"},
	})
	if err != nil {
		t.Fatalf("add custom plugin: %v", err)
	}
	if err := cm.Save(); err != nil {
		t.Fatalf("save custom plugin: %v", err)
	}

	if _, err := setPluginsEnabled([]string{"custom-lint"}, true); err != nil {
		t.Fatalf("enable custom plugin: %v", err)
	}

	cm = server.NewConfigManager()
	if !cm.IsPluginEnabled("custom-lint") {
		t.Fatalf("expected custom-lint to remain enabled")
	}
}

func TestKnownPluginNames(t *testing.T) {
	names, err := knownPluginNames()
	if err != nil {
		t.Fatalf("knownPluginNames failed: %v", err)
	}

	// Should have at least some known plugins
	if len(names) == 0 {
		t.Fatal("expected some known plugin names")
	}

	// Check that some common plugins exist
	if _, ok := names["go-lint"]; !ok {
		t.Error("expected go-lint to be known")
	}
	if _, ok := names["go-test-fast"]; !ok {
		t.Error("expected go-test-fast to be known")
	}
}

func TestValidatePluginNames(t *testing.T) {
	// Valid plugin names should not error
	err := validatePluginNames([]string{"go-lint", "go-test-fast"})
	if err != nil {
		t.Fatalf("validatePluginNames failed for valid names: %v", err)
	}

	// Empty list should not error
	err = validatePluginNames([]string{})
	if err != nil {
		t.Fatalf("validatePluginNames failed for empty list: %v", err)
	}
}

func TestValidatePluginNamesInvalid(t *testing.T) {
	err := validatePluginNames([]string{"nonexistent-plugin-xyz"})
	if err == nil {
		t.Fatal("expected error for invalid plugin name")
	}
	if !strings.Contains(err.Error(), "unknown plugin") {
		t.Fatalf("expected 'unknown plugin' error, got: %v", err)
	}
}

func TestWriteAndReadPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write PID
	testPid := os.Getpid()
	writePid(pidPath, testPid)

	// Read PID
	readPidVal := readPid(pidPath)
	if readPidVal != testPid {
		t.Errorf("readPid mismatch: got %d, want %d", readPidVal, testPid)
	}
}

func TestCleanPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write then clean
	writePid(pidPath, 12345)
	cleanPidFile(pidPath)

	// File should not exist
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected pid file to be removed")
	}

	// Clean non-existent file should not error
	cleanPidFile(pidPath)
}

func TestIsRunning(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write current process PID (definitely running)
	writePid(pidPath, os.Getpid())
	if !isRunning(pidPath) {
		t.Error("expected current process to be running")
	}

	// Write non-existent PID
	writePid(pidPath, 999999)
	if isRunning(pidPath) {
		t.Error("expected non-existent PID to not be running")
	}
}

func TestGetPidFilePath(t *testing.T) {
	path := getPidFilePath()
	if path == "" {
		t.Error("getPidFilePath should not return empty string")
	}
	// Should end with .pid
	if !strings.HasSuffix(path, ".pid") {
		t.Errorf("expected .pid suffix, got: %s", path)
	}
}

func TestGetLogFilePath(t *testing.T) {
	path := getLogFilePath()
	if path == "" {
		t.Error("getLogFilePath should not return empty string")
	}
}

func TestDisabledPluginsEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	cm := server.NewConfigManager()
	got := disabledPlugins(cm, coreGoPlugins)
	if len(got) != 0 {
		t.Errorf("expected no disabled plugins, got: %v", got)
	}
}

func TestIsValidPluginName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"valid-plugin", true},
		{"valid_plugin", true},
		{"plugin123", true},
		{"a", true},
		{"", false},
		{"Invalid", false},      // uppercase
		{"invalid name", false}, // space
		{"invalid.name", false}, // dot
		{"invalid/name", false}, // slash
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidPluginName(tt.name)
			if got != tt.want {
				t.Errorf("isValidPluginName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsValidLanguage(t *testing.T) {
	tests := []struct {
		lang string
		want bool
	}{
		{"python", true},
		{"shell", true},
		{"go", true},
		{"java", false},
		{"", false},
		{"python3", false},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := isValidLanguage(tt.lang)
			if got != tt.want {
				t.Errorf("isValidLanguage(%q) = %v, want %v", tt.lang, got, tt.want)
			}
		})
	}
}

func TestGeneratePluginFiles(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatalf("failed to create plugin dir: %v", err)
	}

	err := generatePluginFiles(pluginDir, "test-plugin", "shell")
	if err != nil {
		t.Fatalf("generatePluginFiles failed: %v", err)
	}

	// Check manifest.json exists
	manifestPath := filepath.Join(pluginDir, "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Error("manifest.json should exist")
	}

	// Check run.sh exists
	runPath := filepath.Join(pluginDir, "run.sh")
	if _, err := os.Stat(runPath); os.IsNotExist(err) {
		t.Error("run.sh should exist")
	}

	// Check README.md exists
	readmePath := filepath.Join(pluginDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Error("README.md should exist")
	}
}

func TestGeneratePluginFilesPython(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "my-plugin")
	_ = os.MkdirAll(pluginDir, 0755)

	err := generatePluginFiles(pluginDir, "my-plugin", "python")
	if err != nil {
		t.Fatalf("generatePluginFiles (python) failed: %v", err)
	}

	runPath := filepath.Join(pluginDir, "run.py")
	if _, err := os.Stat(runPath); os.IsNotExist(err) {
		t.Error("run.py should exist for python plugin")
	}
}

func TestGeneratePluginFilesGo(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "go-plugin")
	_ = os.MkdirAll(pluginDir, 0755)

	err := generatePluginFiles(pluginDir, "go-plugin", "go")
	if err != nil {
		t.Fatalf("generatePluginFiles (go) failed: %v", err)
	}

	runPath := filepath.Join(pluginDir, "run.go")
	if _, err := os.Stat(runPath); os.IsNotExist(err) {
		t.Error("run.go should exist for go plugin")
	}
}

func TestCloseWithLog(t *testing.T) {
	// Test successful close
	f, err := os.CreateTemp("", "test*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	closeWithLog(f, "temp file")
	// File should be closed - no assertion needed, just verify no panic
}

func TestRemovePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Remove existing file
	filePath := filepath.Join(tmpDir, "to_remove.txt")
	_ = os.WriteFile(filePath, []byte("test"), 0644)
	removePath(filePath)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}

	// Remove non-existent file should not error
	removePath(filepath.Join(tmpDir, "nonexistent.txt"))
}

func TestRemovePathDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Remove empty directory
	dirPath := filepath.Join(tmpDir, "to_remove_dir")
	_ = os.MkdirAll(dirPath, 0755)
	removePath(dirPath)
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Error("empty directory should be removed")
	}
}

func TestShowLogTail(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create log file with 10 lines
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("log line %d", i))
	}
	_ = os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	// Test reading last 5 lines
	showLogTail(logFile, 5, "", "")
}

func TestShowLogTailWithFilter(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create log file with job IDs
	_ = os.WriteFile(logFile, []byte("log line 1\n[job-123] task started\nlog line 3\n[job-456] task done\n"), 0644)

	// Test filtering by job ID
	showLogTail(logFile, 10, "job-123", "")

	// Test filtering by plugin name
	showLogTail(logFile, 10, "", "go-lint")
}

func TestShowLogTailNonExistent(t *testing.T) {
	// Should not panic on non-existent file
	showLogTail("/nonexistent/file.log", 10, "", "")
}

func TestRotateLogIfNeeded(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create a file smaller than maxLogSize (50MB)
	_ = os.WriteFile(logFile, []byte("small log content"), 0644)
	rotateLogIfNeeded(logFile)
	// No rotation should happen for small file

	// Check file still exists and has original content
	data, _ := os.ReadFile(logFile)
	if string(data) != "small log content" {
		t.Error("file content should be unchanged")
	}
}

func TestRotateLogIfNeededNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "nonexistent.log")

	// Should not error when file doesn't exist
	rotateLogIfNeeded(logFile)
}

func TestWritePid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write current process PID
	writePid(pidPath, os.Getpid())

	// Read and verify
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("Failed to read pid file: %v", err)
	}

	readPid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if readPid != os.Getpid() {
		t.Errorf("PID mismatch: got %d, want %d", readPid, os.Getpid())
	}
}

func TestReadPidNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	// Read non-existent file should return 0
	pid := readPid(pidPath)
	if pid != 0 {
		t.Errorf("Expected 0 for non-existent file, got %d", pid)
	}
}

func TestIsRunningWithZeroPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "zero.pid")

	// Write 0 as PID
	_ = os.WriteFile(pidPath, []byte("0"), 0644)

	// 0 should not be considered running
	if isRunning(pidPath) {
		t.Error("PID 0 should not be considered running")
	}
}

func TestIsRunningInvalidPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "invalid.pid")

	// Write invalid PID content
	_ = os.WriteFile(pidPath, []byte("not a number"), 0644)

	// Invalid PID should not be running
	if isRunning(pidPath) {
		t.Error("invalid PID should not be considered running")
	}
}

func TestShowLogTailEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "empty.log")

	_ = os.WriteFile(logFile, []byte(""), 0644)
	showLogTail(logFile, 10, "", "")
}

func TestShowLogTailSingleLine(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "single.log")

	_ = os.WriteFile(logFile, []byte("single line\n"), 0644)
	showLogTail(logFile, 5, "", "")
}

func TestShowLogTailLimitGreaterThanLines(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "short.log")

	_ = os.WriteFile(logFile, []byte("line 1\nline 2\n"), 0644)
	showLogTail(logFile, 100, "", "") // limit > lines
}

func TestReadPidInvalidContent(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "invalid.pid")

	// Write invalid content
	_ = os.WriteFile(pidPath, []byte("abc123"), 0644)

	// Should return 0 for invalid content
	pid := readPid(pidPath)
	if pid != 0 {
		t.Errorf("Expected 0 for invalid content, got %d", pid)
	}
}

func TestCleanPidFileNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	// Clean non-existent file should not error
	cleanPidFile(pidPath)
}

func TestSetPluginsEnabledWithCustomPlugin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	// Add a custom plugin first
	cm := server.NewConfigManager()
	err := cm.AddPlugin("custom-test", server.PluginConfig{
		Type:     "exec",
		Command:  "echo",
		Triggers: []string{"git.push"},
	})
	if err != nil {
		t.Fatalf("add custom plugin: %v", err)
	}
	_ = cm.Save()

	// Enable custom plugin
	_, err = setPluginsEnabled([]string{"custom-test"}, true)
	if err != nil {
		t.Fatalf("setPluginsEnabled failed: %v", err)
	}

	// Verify
	cm = server.NewConfigManager()
	if !cm.IsPluginEnabled("custom-test") {
		t.Error("custom plugin should be enabled")
	}
}

func TestSetPluginsEnabledDisableAll(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ACTIOND_CONFIG_PATH", configPath)

	// Disable all core go plugins
	_, err := setPluginsEnabled(coreGoPlugins, false)
	if err != nil {
		t.Fatalf("setPluginsEnabled failed: %v", err)
	}

	// Verify
	cm := server.NewConfigManager()
	for _, name := range coreGoPlugins {
		if cm.IsPluginEnabled(name) {
			t.Errorf("expected %s to be disabled", name)
		}
	}
}

func TestGenerateReadme(t *testing.T) {
	content := generateReadme("test-plugin", "shell")
	if !strings.Contains(content, "test-plugin") {
		t.Error("README should contain plugin name")
	}
	// README contains run.sh for shell language
	if !strings.Contains(content, "run.sh") {
		t.Error("README should contain run.sh for shell")
	}
}

func TestGenerateReadmeGo(t *testing.T) {
	content := generateReadme("go-plugin", "go")
	if !strings.Contains(content, "run.go") {
		t.Error("README should contain run.go for go")
	}
}

func TestGenerateManifest(t *testing.T) {
	manifest := generateManifest("my-plugin", "python")
	if !strings.Contains(manifest, "my-plugin") {
		t.Error("manifest should contain plugin name")
	}
	if !strings.Contains(manifest, "run.py") {
		t.Error("manifest should contain run.py for python")
	}
}

func TestGenerateManifestShell(t *testing.T) {
	manifest := generateManifest("shell-plugin", "shell")
	if !strings.Contains(manifest, "run.sh") {
		t.Error("manifest should contain run.sh for shell")
	}
}

func TestGenerateManifestGo(t *testing.T) {
	manifest := generateManifest("go-plugin", "go")
	if !strings.Contains(manifest, "run.go") {
		t.Error("manifest should contain run.go for go")
	}
}

func TestGeneratePythonPlugin(t *testing.T) {
	content := generatePythonPlugin("test-python")
	if !strings.Contains(content, "test-python") {
		t.Error("content should contain plugin name")
	}
}

func TestGenerateShellPlugin(t *testing.T) {
	content := generateShellPlugin("test-shell")
	if !strings.Contains(content, "test-shell") {
		t.Error("content should contain plugin name")
	}
}

func TestGenerateGoPlugin(t *testing.T) {
	content := generateGoPlugin("test-go")
	if !strings.Contains(content, "test-go") {
		t.Error("content should contain plugin name")
	}
}
