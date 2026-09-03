package dispatcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
)

func TestDispatchResolvesRepoPathWithoutExplicitRepoRoot(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "actiond")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/actiond\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	t.Chdir(repoDir)

	plugins := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "go-test-fast",
			Command:   "python3",
			Args:      []string{"run.py"},
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"go"},
		}),
	}

	disp := New(plugins, "")
	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "actiond.git",
	})
	if len(matched) != 1 {
		t.Fatalf("expected 1 matched plugin, got %d", len(matched))
	}
	if matched[0].Name() != "go-test-fast" {
		t.Fatalf("matched plugin = %q, want go-test-fast", matched[0].Name())
	}
}

func TestDispatchWithEnabledChecker(t *testing.T) {
	plugins := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "test-plugin",
			Command:   "echo",
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"*"},
		}),
	}

	disp := New(plugins, "")

	// Set enabled checker that disables the plugin
	disp.SetEnabledChecker(func(name string) bool {
		return name != "test-plugin"
	})

	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "test.git",
	})

	if len(matched) != 0 {
		t.Fatalf("expected 0 matched plugins, got %d", len(matched))
	}
}

func TestDispatchWithTriggerMismatch(t *testing.T) {
	plugins := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "tag-plugin",
			Command:   "echo",
			Triggers:  []string{event.TypeGitTag},
			Languages: []string{"*"},
		}),
	}

	disp := New(plugins, "")

	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "test.git",
	})

	if len(matched) != 0 {
		t.Fatalf("expected 0 matched plugins for trigger mismatch, got %d", len(matched))
	}
}

func TestDispatchWithLanguageMismatch(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "python-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	// Only has Python files, not Go
	if err := os.WriteFile(filepath.Join(repoDir, "setup.py"), []byte("from setuptools import setup"), 0644); err != nil {
		t.Fatalf("write setup.py: %v", err)
	}

	t.Chdir(repoDir)

	plugins := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "go-lint",
			Command:   "echo",
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"go"},
		}),
	}

	disp := New(plugins, "")

	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "python-repo.git",
	})

	if len(matched) != 0 {
		t.Fatalf("expected 0 matched plugins for language mismatch, got %d", len(matched))
	}
}

func TestDispatchWithWildcardLanguage(t *testing.T) {
	plugins := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "universal-plugin",
			Command:   "echo",
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"*"},
		}),
	}

	disp := New(plugins, "")

	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "any-repo.git",
	})

	if len(matched) != 1 {
		t.Fatalf("expected 1 matched plugin for wildcard, got %d", len(matched))
	}
}

func TestDispatchSetPlugins(t *testing.T) {
	plugins1 := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "plugin-1",
			Command:   "echo",
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"*"},
		}),
	}

	disp := New(plugins1, "")

	matched := disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "test.git",
	})

	if len(matched) != 1 || matched[0].Name() != "plugin-1" {
		t.Fatal("initial plugins not set correctly")
	}

	// Replace plugins
	plugins2 := []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:      "plugin-2",
			Command:   "echo",
			Triggers:  []string{event.TypeGitPush},
			Languages: []string{"*"},
		}),
	}

	disp.SetPlugins(plugins2)

	matched = disp.Dispatch(event.Event{
		Type: event.TypeGitPush,
		Repo: "test.git",
	})

	if len(matched) != 1 || matched[0].Name() != "plugin-2" {
		t.Fatal("plugins not replaced correctly")
	}
}

func TestDetectProjectLanguageGo(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "go-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/go-repo"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	langs := detectProjectLanguage(repoDir)

	found := false
	for _, l := range langs {
		if l == "go" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect Go language")
	}
}

func TestDetectProjectLanguagePython(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "py-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "requirements.txt"), []byte("flask==2.0"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	langs := detectProjectLanguage(repoDir)

	found := false
	for _, l := range langs {
		if l == "python" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect Python language")
	}
}

func TestDetectProjectLanguageJava(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "java-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "pom.xml"), []byte("<project>...</project>"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	langs := detectProjectLanguage(repoDir)

	found := false
	for _, l := range langs {
		if l == "java" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect Java language")
	}
}

func TestDetectProjectLanguageWeb(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "web-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "package.json"), []byte(`{"name":"web-repo"}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "tsconfig.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	langs := detectProjectLanguage(repoDir)

	found := false
	for _, l := range langs {
		if l == "typescript" || l == "web" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect TypeScript/web language")
	}
}

func TestMatchesLanguage(t *testing.T) {
	tests := []struct {
		name    string
		plugin  []string
		project []string
		want    bool
	}{
		{"wildcard matches all", []string{"*"}, []string{"go", "python"}, true},
		{"exact match", []string{"go"}, []string{"go", "python"}, true},
		{"case insensitive", []string{"GO"}, []string{"go"}, true},
		{"no overlap", []string{"java"}, []string{"go", "python"}, false},
		{"empty project", []string{"go"}, []string{}, false},
		{"empty plugin", []string{}, []string{"go"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesLanguage(tt.plugin, tt.project)
			if got != tt.want {
				t.Errorf("matchesLanguage(%v, %v) = %v, want %v", tt.plugin, tt.project, got, tt.want)
			}
		})
	}
}

func TestHasAnyFile(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "test-repo")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !hasAnyFile(repoDir, "go.mod", "package.json") {
		t.Error("expected go.mod to exist")
	}

	if hasAnyFile(repoDir, " Cargo.toml", "package.json") {
		t.Error("expected no match")
	}
}
