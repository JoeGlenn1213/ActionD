// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Handoff Package tests (ASSURANCE Phase C)

package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestBuildHandoffPackageEndToEnd: real git repo + fake ActionD data.
func TestBuildHandoffPackageEndToEnd(t *testing.T) {
	t.Setenv("RMS_API_KEY", "")
	dir := newGitRepo(t, "demo")
	changes, err := readGitLog(dir, "", 5)
	if err != nil || len(changes) == 0 {
		t.Fatalf("readGitLog: %v", err)
	}
	headSHA := changes[0].CommitSHA

	fc := &fakeClient{
		actions: []ActionInfo{
			{ID: "j-1", Repo: "demo.git", PluginName: "go-test", Status: "failed", CreatedAt: "2026-08-22T00:00:00Z"},
		},
	}

	pkg, err := buildHandoffPackage(fc, dir, "task-9", "dsh:codex", "dsh:next-window",
		"add feature X", "run the tests", "fix the failing test", "demo-proj", 24)
	if err != nil {
		t.Fatalf("buildHandoffPackage: %v", err)
	}

	if pkg.SchemaVersion != HandoffSchemaVersion {
		t.Errorf("schema version = %q", pkg.SchemaVersion)
	}
	if pkg.Envelope.FromAgent != "dsh:codex" || pkg.Envelope.ToAgent != "dsh:next-window" {
		t.Errorf("envelope routing wrong: %+v", pkg.Envelope)
	}
	if pkg.Envelope.Revision != headSHA {
		t.Errorf("revision = %q, want HEAD %s", pkg.Envelope.Revision, headSHA)
	}
	if pkg.Envelope.ExpiresAt <= pkg.Envelope.CreatedAt {
		t.Errorf("expires_at must be after created_at")
	}
	if pkg.Payload.Task != "task-9" {
		t.Errorf("task = %q", pkg.Payload.Task)
	}
	if len(pkg.Payload.CompletedWork) != 2 {
		t.Errorf("completed work = %v, want 2 commits", pkg.Payload.CompletedWork)
	}
	// Failed verdict must surface as a known failure.
	if len(pkg.Payload.KnownFailures) != 1 || !strings.Contains(pkg.Payload.KnownFailures[0], "go-test") {
		t.Errorf("known failures = %v, want the failed go-test job", pkg.Payload.KnownFailures)
	}
	if pkg.Payload.VerificationState.Status != "fail" {
		t.Errorf("verification = %q, want fail", pkg.Payload.VerificationState.Status)
	}
	// This package is complete (from/to/revision/next_action all set, fail
	// captured in known_failures) — it must carry zero warnings.
	if len(pkg.Validation) != 0 {
		t.Errorf("complete package should have no warnings, got %v", pkg.Validation)
	}

	// Markdown must be a self-contained brief for the incoming agent.
	for _, want := range []string{"# Handoff Package", "from", "to", "Suggested Next Action", "Known Failures", "Verification State"} {
		if !strings.Contains(pkg.Markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, pkg.Markdown)
		}
	}
}

// TestValidateHandoff: required-field warnings are explicit, never fatal.
func TestValidateHandoff(t *testing.T) {
	pkg := &HandoffPackage{Envelope: HandoffEnvelope{}, Payload: HandoffPayload{}}
	warnings := validateHandoff(pkg)
	if len(warnings) < 4 {
		t.Errorf("expected >=4 warnings for empty package, got %v", warnings)
	}

	pkg = &HandoffPackage{
		Envelope: HandoffEnvelope{FromAgent: "a", ToAgent: "b", Revision: "deadbeef"},
		Payload: HandoffPayload{
			VerificationState:   VerificationInfo{Status: "pass", JobsPassed: 1, JobsTotal: 1},
			SuggestedNextAction: "continue",
		},
	}
	if warnings = validateHandoff(pkg); len(warnings) != 0 {
		t.Errorf("complete package should have no warnings, got %v", warnings)
	}
}

// TestBuildHandoffState: tri-state current-state with R1 evidence levels.
func TestBuildHandoffState(t *testing.T) {
	changes := []ChangeEntry{{CommitSHA: "abc"}}

	got := buildHandoffState([]VerdictEntry{{Status: "pass"}}, changes)
	if got.Status != "known" || got.EvidenceLevel != 3 {
		t.Errorf("pass state = %+v, want known/L3", got)
	}

	got = buildHandoffState([]VerdictEntry{{Status: "fail"}}, changes)
	if got.Status != "known" || !strings.Contains(got.Detail, "fail") {
		t.Errorf("fail state = %+v", got)
	}

	got = buildHandoffState(nil, changes)
	if got.Status != "partial" || got.EvidenceLevel != 1 {
		t.Errorf("unknown-verdict state = %+v, want partial/L1", got)
	}

	got = buildHandoffState(nil, nil)
	if got.Status != "unknown" || got.EvidenceLevel != 0 {
		t.Errorf("no-history state = %+v, want unknown/L0", got)
	}
}

// TestHandleHandoffPackTool: the MCP entry point returns structured output.
func TestHandleHandoffPackTool(t *testing.T) {
	t.Setenv("RMS_API_KEY", "")
	dir := newGitRepo(t, "demo")
	fc := &fakeClient{}

	r, err := handleHandoffPack(fc, context.Background(), callReq(map[string]any{
		"path": dir, "task_id": "t-1", "from_agent": "a", "to_agent": "b",
	}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	for _, want := range []string{"schema_version", "envelope", "payload", "markdown", "from_agent"} {
		if !strings.Contains(body, want) {
			t.Errorf("handoff body missing %q: %s", want, body)
		}
	}
}
