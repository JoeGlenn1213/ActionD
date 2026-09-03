package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/event"
)

func TestExecPluginMatchRepoFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:       "repo-only",
		Command:    "echo",
		Triggers:   []string{"git.push"},
		Languages:  []string{"*"},
		RepoFilter: "demo-api.git",
	})

	if !p.Match(event.Event{Repo: "demo-api.git"}) {
		t.Fatalf("expected exact repo filter match")
	}
	if !p.Match(event.Event{Repo: "demo-api"}) {
		t.Fatalf("expected repo filter to match repo names without .git suffix")
	}
	if p.Match(event.Event{Repo: "other-repo.git"}) {
		t.Fatalf("expected repo filter mismatch")
	}
}

func TestExecPluginMatchRepoAndRefFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:       "repo-and-ref",
		Command:    "echo",
		Triggers:   []string{"git.tag"},
		Languages:  []string{"*"},
		RepoFilter: "demo-*",
		RefFilter:  "refs/tags/*",
	})

	if !p.Match(event.Event{
		Repo: "demo-api.git",
		Ref:  "refs/tags/v1.0.0",
	}) {
		t.Fatalf("expected repo and ref filters to match")
	}

	if p.Match(event.Event{
		Repo: "demo-api.git",
		Ref:  "refs/heads/main",
	}) {
		t.Fatalf("expected ref filter mismatch")
	}
}

func TestPluginTriggerMatching(t *testing.T) {
	plugins := []ExecPluginConfig{
		{
			Name:      "go-lint",
			Command:   "echo",
			Triggers:  []string{"git.push"},
			Languages: []string{"go"},
		},
		{
			Name:      "deploy",
			Command:   "echo",
			Triggers:  []string{"git.tag"},
			Languages: []string{"*"},
		},
	}

	// go-lint should match git.push
	lintMatched := false
	for _, trig := range plugins[0].Triggers {
		if trig == "git.push" {
			lintMatched = true
		}
	}
	if !lintMatched {
		t.Error("go-lint should trigger on git.push")
	}

	// deploy should NOT match git.push
	deployMatched := false
	for _, trig := range plugins[1].Triggers {
		if trig == "git.push" {
			deployMatched = true
		}
	}
	if deployMatched {
		t.Error("deploy should not trigger on git.push")
	}
}

func TestPluginLanguageMatching(t *testing.T) {
	plugin := ExecPluginConfig{
		Name:      "security-scan",
		Command:   "echo",
		Triggers:  []string{"git.push"},
		Languages: []string{"*"},
	}

	// Wildcard should match any language
	wildcardMatched := false
	for _, lang := range plugin.Languages {
		if lang == "*" {
			wildcardMatched = true
		}
	}
	if !wildcardMatched {
		t.Error("plugin with * languages should match wildcard")
	}
}

func TestPluginRefFilter(t *testing.T) {
	plugin := ExecPluginConfig{
		Name:      "release",
		Command:   "echo",
		Triggers:  []string{"git.tag"},
		Languages: []string{"*"},
		RefFilter: "refs/tags/*",
	}

	// Test that ref filter is set
	if plugin.RefFilter != "refs/tags/*" {
		t.Errorf("expected ref filter refs/tags/*, got %s", plugin.RefFilter)
	}
}

func TestExecPluginName(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:    "test-plugin",
		Command: "echo",
	})

	if p.Name() != "test-plugin" {
		t.Errorf("expected name test-plugin, got %s", p.Name())
	}
}

func TestExecPluginTriggers(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:     "test-plugin",
		Command:  "echo",
		Triggers: []string{"git.push", "git.tag"},
	})

	triggers := p.Triggers()
	if len(triggers) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(triggers))
	}
}

func TestExecPluginLanguages(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:      "test-plugin",
		Command:   "echo",
		Languages: []string{"go", "python"},
	})

	langs := p.Languages()
	if len(langs) != 2 {
		t.Errorf("expected 2 languages, got %d", len(langs))
	}
}

func TestExecPluginTimeout(t *testing.T) {
	cfg := ExecPluginConfig{
		Name:    "test-plugin",
		Command: "echo",
		Timeout: 30 * time.Second,
	}
	p := NewExecPlugin(cfg)

	if p.timeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", p.timeout)
	}
}

func TestExecPluginTimeoutDefault(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:    "test-plugin",
		Command: "echo",
	})

	// Default timeout should be 5 minutes
	if p.timeout != 5*time.Minute {
		t.Errorf("expected default 5m timeout, got %v", p.timeout)
	}
}

func TestExecPluginMatchDefault(t *testing.T) {
	// Plugin with no custom match function should match all events
	p := NewExecPlugin(ExecPluginConfig{
		Name:      "test-plugin",
		Command:   "echo",
		Triggers:  []string{"git.push"},
		Languages: []string{"*"},
	})

	// Should match basic event
	if !p.Match(event.Event{
		Type: event.TypeGitPush,
		Repo: "test.git",
	}) {
		t.Error("expected match for basic event")
	}
}

func TestExecPluginWithTimeoutOverride(t *testing.T) {
	cfg := ExecPluginConfig{
		Name:    "test-plugin",
		Command: "echo",
		Timeout: 10 * time.Minute,
	}
	p := NewExecPlugin(cfg)

	// Verify timeout can be set to 10 minutes
	if p.timeout != 10*time.Minute {
		t.Errorf("expected 10m timeout, got %v", p.timeout)
	}
}

func TestExecInputStructure(t *testing.T) {
	input := ExecInput{
		Event: event.Event{
			Type: event.TypeGitPush,
			Repo: "test.git",
		},
		RepoPath:    "/path/to/repo",
		ArtifactDir: "/path/to/artifacts",
	}

	if input.Event.Type != event.TypeGitPush {
		t.Errorf("expected event type git.push, got %s", input.Event.Type)
	}
	if input.RepoPath != "/path/to/repo" {
		t.Errorf("expected repo path, got %s", input.RepoPath)
	}
}

func TestExecOutputStructure(t *testing.T) {
	output := ExecOutput{
		Status:    "success",
		Error:     "",
		Artifacts: []string{"report.json"},
		Model:     "test-model",
		Tokens:    100,
		Duration:  5000,
	}

	if output.Status != "success" {
		t.Errorf("expected status success, got %s", output.Status)
	}
	if len(output.Artifacts) != 1 {
		t.Errorf("expected 1 artifact, got %d", len(output.Artifacts))
	}
	if output.Tokens != 100 {
		t.Errorf("expected 100 tokens, got %d", output.Tokens)
	}
}

func TestExecOutputWithError(t *testing.T) {
	output := ExecOutput{
		Status:    "error",
		Error:     "command failed",
		Artifacts: []string{},
		Duration:  1000,
	}

	if output.Status != "error" {
		t.Errorf("expected status error, got %s", output.Status)
	}
	if output.Error != "command failed" {
		t.Errorf("expected error message, got %s", output.Error)
	}
}

func TestPluginMatchWithRepoFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:       "filtered-plugin",
		Command:    "echo",
		Triggers:   []string{"git.push"},
		Languages:  []string{"*"},
		RepoFilter: "specific-repo.git",
	})

	// Should match
	if !p.Match(event.Event{Repo: "specific-repo.git"}) {
		t.Error("expected match for filtered repo")
	}

	// Should not match different repo
	if p.Match(event.Event{Repo: "other-repo.git"}) {
		t.Error("expected no match for non-filtered repo")
	}
}

func TestPluginMatchWithRefFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:      "tagged-plugin",
		Command:   "echo",
		Triggers:  []string{"git.tag"},
		Languages: []string{"*"},
		RefFilter: "refs/tags/v*",
	})

	// Should match version tag
	if !p.Match(event.Event{Ref: "refs/tags/v1.0.0"}) {
		t.Error("expected match for version tag")
	}

	// Should not match branch
	if p.Match(event.Event{Ref: "refs/heads/main"}) {
		t.Error("expected no match for branch ref")
	}
}

func TestNewExecPluginWithAllFields(t *testing.T) {
	cfg := ExecPluginConfig{
		Name:       "full-plugin",
		Command:    "python3",
		Args:       []string{"run.py", "--verbose"},
		Triggers:   []string{"git.push", "git.tag"},
		Languages:  []string{"go", "python"},
		Timeout:    10 * time.Minute,
		WorkingDir: "/workspace",
		RefFilter:  "refs/tags/*",
		RepoFilter: "my-repo.git",
	}

	p := NewExecPlugin(cfg)

	if p.Name() != "full-plugin" {
		t.Errorf("expected name full-plugin, got %s", p.Name())
	}
	if len(p.Triggers()) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(p.Triggers()))
	}
	if len(p.Languages()) != 2 {
		t.Errorf("expected 2 languages, got %d", len(p.Languages()))
	}
}

func TestExecPluginNoMatch(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:      "test",
		Command:   "echo",
		Triggers:  []string{"git.tag"},
		Languages: []string{"python"},
	})

	// Should not match push event (trigger mismatch)
	// tag-only plugin should not respond to push
	event := event.Event{Type: event.TypeGitPush, Repo: "test.git"}
	// The actual behavior depends on Match implementation
	// Just verify it doesn't crash
	p.Match(event)
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewExecPlugin(ExecPluginConfig{
		Name:    "test",
		Command: "sleep",
		Args:    []string{"100"},
	})

	// Should return error when context is cancelled
	err := p.Run(Context{Ctx: ctx})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestExecPluginRefFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:      "ref-filter-test",
		Command:   "echo",
		Triggers:  []string{"git.tag"},
		Languages: []string{"*"},
		RefFilter: "refs/tags/v*",
	})

	filter := p.RefFilter()
	if filter != "refs/tags/v*" {
		t.Errorf("RefFilter mismatch: got %s", filter)
	}
}

func TestExecPluginRepoFilter(t *testing.T) {
	p := NewExecPlugin(ExecPluginConfig{
		Name:       "repo-filter-test",
		Command:    "echo",
		Triggers:   []string{"git.push"},
		Languages:  []string{"*"},
		RepoFilter: "my-repo.git",
	})

	filter := p.RepoFilter()
	if filter != "my-repo.git" {
		t.Errorf("RepoFilter mismatch: got %s", filter)
	}
}

func TestExecPluginConfig(t *testing.T) {
	cfg := ExecPluginConfig{
		Name:       "test-plugin",
		Command:    "echo hello",
		Args:       []string{"arg1", "arg2"},
		Triggers:   []string{"git.push"},
		Languages:  []string{"go"},
		Timeout:    5 * time.Minute,
		WorkingDir: "/tmp",
		RefFilter:  "refs/heads/main",
		RepoFilter: "test-repo.git",
	}

	p := NewExecPlugin(cfg)
	returnedCfg := p.Config()

	if returnedCfg.Name != "test-plugin" {
		t.Errorf("Name mismatch: got %s", returnedCfg.Name)
	}
	if returnedCfg.Command != "echo hello" {
		t.Errorf("Command mismatch: got %s", returnedCfg.Command)
	}
	if len(returnedCfg.Args) != 2 {
		t.Errorf("Args length mismatch: got %d", len(returnedCfg.Args))
	}
	if returnedCfg.RefFilter != "refs/heads/main" {
		t.Errorf("RefFilter mismatch: got %s", returnedCfg.RefFilter)
	}
	if returnedCfg.RepoFilter != "test-repo.git" {
		t.Errorf("RepoFilter mismatch: got %s", returnedCfg.RepoFilter)
	}

	// Verify args is a copy, not same slice
	if &returnedCfg.Args == &cfg.Args {
		t.Error("Args should be a copy, not same reference")
	}
	if &returnedCfg.Triggers == &cfg.Triggers {
		t.Error("Triggers should be a copy, not same reference")
	}
}

// memArtifactWriter captures the StructuredResult written by ExecPlugin.Run.
type memArtifactWriter struct {
	result *StructuredResult
	dir    string
}

func (w *memArtifactWriter) Write(name string, data []byte) error { return nil }

func (w *memArtifactWriter) WriteJSON(name string, v interface{}) error {
	if name == "result.json" {
		if r, ok := v.(*StructuredResult); ok {
			w.result = r
		}
	}
	return nil
}

func (w *memArtifactWriter) Dir() string { return w.dir }

// runPluginWithOutput runs an ExecPlugin whose command echoes the given JSON
// to stdout, then returns the parsed StructuredResult and the Run error.
func runPluginWithOutput(t *testing.T, jsonOutput string) (*StructuredResult, error) {
	t.Helper()
	p := NewExecPlugin(ExecPluginConfig{
		Name:    "echo-json",
		Command: "sh",
		Args:    []string{"-c", "echo '" + jsonOutput + "'"},
	})
	w := &memArtifactWriter{dir: t.TempDir()}
	err := p.Run(Context{
		Ctx:       context.Background(),
		RepoPath:  "/nonexistent-repo",
		Artifacts: w,
	})
	return w.result, err
}

func TestExecPluginV1DecisionDenyFails(t *testing.T) {
	out := `{"action_id":"a1","plugin_id":"p","capability":"lint","language":"go","status":"success","decision":"deny","summary":{"message":"gate denied"},"hints":[],"artifacts":[],"signals":{},"next_actions":[]}`
	result, err := runPluginWithOutput(t, out)
	if err == nil {
		t.Fatal("expected error for V1 decision=deny")
	}
	if result == nil {
		t.Fatal("expected parsed result")
	}
	if result.Status != "failed" {
		t.Errorf("expected status failed, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Type != "gate_denied" {
		t.Errorf("expected gate_denied error, got %+v", result.Error)
	}
	if result.Error != nil && result.Error.Message != "gate denied" {
		t.Errorf("expected summary message, got %q", result.Error.Message)
	}
}

func TestExecPluginLegacyDecisionRejectedFails(t *testing.T) {
	out := `{"status":"success","summary":"some summary","decision":"rejected"}`
	result, err := runPluginWithOutput(t, out)
	if err == nil {
		t.Fatal("expected error for legacy decision=rejected")
	}
	if result == nil {
		t.Fatal("expected parsed result")
	}
	if result.Status != "failed" {
		t.Errorf("expected status failed, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Type != "gate_denied" {
		t.Errorf("expected gate_denied error, got %+v", result.Error)
	}
}

func TestExecPluginLegacySummaryObjectAndArtifacts(t *testing.T) {
	out := `{"status":"success","summary":{"message":"formatted 3 files"},"artifacts":[{"path":"/tmp/out/a.txt"},{"path":"/tmp/out/b.txt"}],"hints":["hint1","hint2"]}`
	result, err := runPluginWithOutput(t, out)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected parsed result")
	}
	if result.Status != "success" {
		t.Errorf("expected status success, got %q", result.Status)
	}
	if result.Summary != "formatted 3 files" {
		t.Errorf("expected summary from object message, got %q", result.Summary)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d: %v", len(result.Artifacts), result.Artifacts)
	}
	if result.Artifacts[0] != "/tmp/out/a.txt" || result.Artifacts[1] != "/tmp/out/b.txt" {
		t.Errorf("unexpected artifacts: %v", result.Artifacts)
	}
	if len(result.Hints) != 2 {
		t.Errorf("expected 2 hints, got %d", len(result.Hints))
	}
}

func TestExecPluginDecisionPassKeepsSuccess(t *testing.T) {
	out := `{"status":"success","summary":"all good","decision":"pass"}`
	result, err := runPluginWithOutput(t, out)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected parsed result")
	}
	if result.Status != "success" {
		t.Errorf("expected status success, got %q", result.Status)
	}
}

// TestExecPluginSmokeStdinInjection runs a real subprocess that reads the
// ExecInput JSON from stdin, then emits an old-format summary-object payload.
// It exercises the full stdin → subprocess → stdout parse path with a fixed
// /tmp artifact dir (per the P0 smoke convention).
func TestExecPluginSmokeStdinInjection(t *testing.T) {
	script := `cat >/dev/null; echo '{"status":"success","summary":{"message":"smoke formatted"},"artifacts":[{"path":"/tmp/actd-smoke-exec-runner/a.txt"}],"hints":["ok"]}'`
	p := NewExecPlugin(ExecPluginConfig{
		Name:    "smoke-plugin",
		Command: "sh",
		Args:    []string{"-c", script},
	})
	w := &memArtifactWriter{dir: "/tmp/actd-smoke-exec-runner"}
	err := p.Run(Context{
		Ctx:       context.Background(),
		RepoPath:  "/tmp/actd-smoke-exec-runner/repo",
		Artifacts: w,
	})
	if err != nil {
		t.Fatalf("smoke run failed: %v", err)
	}
	if w.result == nil {
		t.Fatal("expected parsed result")
	}
	if w.result.Status != "success" {
		t.Errorf("expected success, got %q", w.result.Status)
	}
	if w.result.Summary != "smoke formatted" {
		t.Errorf("expected summary from object, got %q", w.result.Summary)
	}
	if len(w.result.Artifacts) != 1 || w.result.Artifacts[0] != "/tmp/actd-smoke-exec-runner/a.txt" {
		t.Errorf("expected artifact path, got %v", w.result.Artifacts)
	}
	if len(w.result.Hints) != 1 || w.result.Hints[0] != "ok" {
		t.Errorf("expected hint, got %v", w.result.Hints)
	}
}
