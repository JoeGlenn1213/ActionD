// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Goal Run Report tests (ASSURANCE Phase A)

package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// statusToVerdict: only done/failed produce verdicts, everything else is unknown.
func TestStatusToVerdict(t *testing.T) {
	cases := map[string]string{
		"done":           "pass",
		"failed":         "fail",
		"pending":        "unknown",
		"queued":         "unknown",
		"running":        "unknown",
		"cancelled":      "unknown",
		"blocked":        "unknown",
		"needs_approval": "unknown",
		"retrying":       "unknown",
		"retryable":      "unknown",
		"":               "unknown",
		"weird":          "unknown",
	}
	for status, want := range cases {
		if got := statusToVerdict(status); got != want {
			t.Errorf("statusToVerdict(%q) = %q, want %q", status, got, want)
		}
	}
}

// overallVerdict: silence is NOT a pass.
func TestOverallVerdict(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []VerdictEntry
		want     string
	}{
		{"empty is unknown", nil, "unknown"},
		{"all pass", []VerdictEntry{{Status: "pass"}, {Status: "pass"}}, "pass"},
		{"any fail wins", []VerdictEntry{{Status: "pass"}, {Status: "fail"}}, "fail"},
		{"mixed pass+pending is unknown", []VerdictEntry{{Status: "pass"}, {Status: "unknown"}}, "unknown"},
		{"fail+unknown is fail", []VerdictEntry{{Status: "fail"}, {Status: "unknown"}}, "fail"},
		{"all unknown", []VerdictEntry{{Status: "unknown"}}, "unknown"},
	}
	for _, tt := range tests {
		if got := overallVerdict(tt.verdicts); got != tt.want {
			t.Errorf("%s: overallVerdict = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// buildIntent: R1 evidence levels — unknown is L0, a concrete task id is L2.
func TestBuildIntent(t *testing.T) {
	got := buildIntent("", false)
	if got.Status != "unknown" || got.EvidenceLevel != 0 {
		t.Errorf("empty intent: %+v, want status=unknown level=0", got)
	}

	got = buildIntent("task-1", true)
	if got.Status != "known" || got.TaskID != "task-1" || got.EvidenceLevel != 2 {
		t.Errorf("caller intent: %+v, want known/task-1/L2", got)
	}

	got = buildIntent("task-2", false)
	if got.Status != "known" || got.EvidenceLevel != 2 {
		t.Errorf("message intent: %+v, want known/L2", got)
	}
}

// buildRecovery: R1 levels — shas alone are L1 (no drill performed anywhere).
func TestBuildRecovery(t *testing.T) {
	got := buildRecovery(nil)
	if got.EvidenceLevel != 0 {
		t.Errorf("no history: level = %d, want 0", got.EvidenceLevel)
	}

	changes := []ChangeEntry{
		{CommitSHA: "a"}, {CommitSHA: "b"}, {CommitSHA: "c"}, {CommitSHA: "d"},
	}
	got = buildRecovery(changes)
	if got.EvidenceLevel != 1 {
		t.Errorf("shas without drill: level = %d, want 1", got.EvidenceLevel)
	}
	if len(got.RecoveryPoints) != 3 || got.RecoveryPoints[0] != "a" {
		t.Errorf("recovery points = %v, want first 3 shas", got.RecoveryPoints)
	}
}

// buildCoverage: the R2 anti-gaming rule — reconstructed mutations never
// count as native, and unknown verdicts never count as verified.
func TestBuildCoverageAntiGaming(t *testing.T) {
	changes := []ChangeEntry{
		{CommitSHA: "s1", Source: "reconstructed"},
		{CommitSHA: "s2", Source: "reconstructed"},
		{CommitSHA: "s3", Source: "reconstructed", Intent: "task-9"}, // has intent, still reconstructed
	}
	verdicts := []VerdictEntry{
		{CommitSHA: "s1", Status: "pass"},
		{CommitSHA: "s2", Status: "unknown"}, // unknown never verifies a mutation
	}
	got := buildCoverage(changes, verdicts)
	if got.MutationsTotal != 3 || got.ReconstructedMutations != 3 {
		t.Errorf("totals wrong: %+v", got)
	}
	if got.NativeMutations != 0 || got.NativeCoverage != 0 {
		t.Errorf("anti-gaming violated: native = %d/%v, want 0/0 (reconstructed must never count as native)", got.NativeMutations, got.NativeCoverage)
	}
	if got.VerificationCoverage != 1.0/3.0 {
		t.Errorf("verification coverage = %v, want 1/3", got.VerificationCoverage)
	}
	if got.UnverifiedMutations != 2 {
		t.Errorf("unverified = %d, want 2", got.UnverifiedMutations)
	}
}

// buildVerification: totals and the fail-wins rule.
func TestBuildVerification(t *testing.T) {
	verdicts := []VerdictEntry{
		{JobID: "a", Status: "pass"},
		{JobID: "b", Status: "pass"},
		{JobID: "c", Status: "fail"},
		{JobID: "d", Status: "unknown"},
	}
	got := buildVerification(verdicts, "")
	if got.Status != "fail" {
		t.Errorf("status = %q, want fail", got.Status)
	}
	if got.JobsTotal != 4 || got.JobsPassed != 2 || got.JobsFailed != 1 || got.JobsUnknown != 1 {
		t.Errorf("totals wrong: %+v", got)
	}
}

// TestBuildHandoffNoTask: without a task id, handoff must be explicit unknown.
func TestBuildHandoffNoTask(t *testing.T) {
	got := buildHandoff("demo", "")
	if got.Status != "unknown" {
		t.Errorf("status = %q, want unknown", got.Status)
	}
}

// TestBuildHandoffRMSUnconfigured: with a task id but no RMS key, handoff
// must degrade to explicit unknown (never a fabricated "no handoff").
func TestBuildHandoffRMSUnconfigured(t *testing.T) {
	t.Setenv("RMS_API_KEY", "")
	got := buildHandoff("demo", "task-1")
	if got.Status != "unknown" || !strings.Contains(got.Detail, "RMS") {
		t.Errorf("handoff = %+v, want explicit unknown with RMS reason", got)
	}
}

// TestNormalizeRepoName: loose matching between paths and job records.
func TestNormalizeRepoName(t *testing.T) {
	cases := map[string]string{
		"lgh":           "lgh",
		"lgh.git":       "lgh",
		"ActionD.git":   "actiond",
		" LocalGitHub ": "localgithub",
	}
	for in, want := range cases {
		if got := normalizeRepoName(in); got != want {
			t.Errorf("normalizeRepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- git-backed tests -------------------------------------------------------

// newGitRepo creates a temp git repo with a few commits; skips when git is
// unavailable (defensive: CI images should have it, dev boxes may not).
func newGitRepo(t *testing.T, dirName string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := filepath.Join(t.TempDir(), dirName)
	runGit := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runGit("init", "-q", dir)
	runGit("-C", dir, "config", "user.email", "test@example.com")
	runGit("-C", dir, "config", "user.name", "test")
	runGit("-C", dir, "commit", "--allow-empty", "-q", "-m", "feat: init task:task-1")
	runGit("-C", dir, "commit", "--allow-empty", "-q", "-m", "fix: second commit")
	return dir
}

// TestReadGitLog: entries are reconstructed, intent is extracted from the
// task: convention, and focused commits return exactly one entry.
func TestReadGitLog(t *testing.T) {
	dir := newGitRepo(t, "demo")

	changes, err := readGitLog(dir, "", 5)
	if err != nil {
		t.Fatalf("readGitLog: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	// Newest first: "fix: second commit".
	if changes[0].Message != "fix: second commit" || changes[0].Source != "reconstructed" {
		t.Errorf("entry[0] = %+v", changes[0])
	}
	if changes[1].Intent != "task-1" {
		t.Errorf("intent extraction failed: %+v", changes[1])
	}

	// Focused commit returns exactly one entry and no others.
	focused, err := readGitLog(dir, changes[1].CommitSHA, 5)
	if err != nil {
		t.Fatalf("focused readGitLog: %v", err)
	}
	if len(focused) != 1 || focused[0].CommitSHA != changes[1].CommitSHA {
		t.Errorf("focused = %+v, want exactly commit %s", focused, changes[1].CommitSHA)
	}
}

// TestBuildRunReportEndToEnd: fake ActionD data + real git repo + no RMS key.
// Verifies repo filtering, verdict mapping, six-question sections, and the
// dual-number coverage.
func TestBuildRunReportEndToEnd(t *testing.T) {
	t.Setenv("RMS_API_KEY", "")
	dir := newGitRepo(t, "demo")
	changes, err := readGitLog(dir, "", 5)
	if err != nil || len(changes) == 0 {
		t.Fatalf("readGitLog: %v", err)
	}
	headSHA := changes[0].CommitSHA

	fc := &fakeClient{
		actions: []ActionInfo{
			{ID: "j-pass", Repo: "demo.git", PluginName: "go-test", Status: "done", CreatedAt: "2026-08-22T00:00:01Z"},
			{ID: "j-fail", Repo: "demo.git", PluginName: "go-lint", Status: "failed", CreatedAt: "2026-08-22T00:00:00Z"},
			{ID: "j-other-repo", Repo: "other.git", PluginName: "go-test", Status: "done"},
			{ID: "j-pending", Repo: "demo.git", PluginName: "security_scan", Status: "pending"},
		},
		actionDetail: map[string]*ActionDetail{
			"j-pass": {ID: "j-pass", Commit: map[string]interface{}{"hash": headSHA}},
		},
	}

	report, err := buildRunReport(fc, dir, "", "task-77", "demo-proj", 5)
	if err != nil {
		t.Fatalf("buildRunReport: %v", err)
	}

	// Repo filtering: other.git must not leak in.
	if report.Verification.JobsTotal != 3 {
		t.Errorf("jobs total = %d, want 3 (other repo filtered)", report.Verification.JobsTotal)
	}
	if report.Verification.Status != "fail" {
		t.Errorf("verification = %q, want fail (j-fail)", report.Verification.Status)
	}
	if report.Verification.JobsUnknown != 1 {
		t.Errorf("jobs unknown = %d, want 1 (pending)", report.Verification.JobsUnknown)
	}

	// Six questions present with honest values.
	if report.Intent.Status != "known" || report.Intent.TaskID != "task-77" {
		t.Errorf("intent = %+v", report.Intent)
	}
	// Phase B: tier machinery implemented but fake details carry no
	// profile/version, so the honest values are unknown-tier / no provenance.
	if !report.Trust.TierImplemented {
		t.Errorf("tier machinery must be implemented: %+v", report.Trust)
	}
	if report.Trust.MaxTier != "unknown" {
		t.Errorf("max tier = %q, want unknown (no profile in fake details)", report.Trust.MaxTier)
	}
	if report.Trust.VerifierProvenance {
		t.Errorf("no verifier versions in fake data, provenance must be false: %+v", report.Trust)
	}
	if report.Handoff.Status != "unknown" {
		t.Errorf("handoff = %+v, want unknown (no RMS key)", report.Handoff)
	}
	if report.Recovery.EvidenceLevel != 1 || len(report.Recovery.RecoveryPoints) == 0 {
		t.Errorf("recovery = %+v, want L1 with points", report.Recovery)
	}

	// Dual-number coverage: everything reconstructed, only head commit verified.
	if report.Coverage.NativeMutations != 0 || report.Coverage.NativeCoverage != 0 {
		t.Errorf("anti-gaming: native must stay 0: %+v", report.Coverage)
	}
	if report.Coverage.MutationsTotal != 2 {
		t.Errorf("mutations total = %d, want 2", report.Coverage.MutationsTotal)
	}
	if report.Coverage.VerificationCoverage != 0.5 {
		t.Errorf("verification coverage = %v, want 0.5 (j-pass matched head, j-fail has no sha)", report.Coverage.VerificationCoverage)
	}

	// Limitations admit what is not guaranteed.
	if len(report.Limitations) < 5 {
		t.Errorf("limitations too short: %v", report.Limitations)
	}
}

// TestProfileToTier: Phase B tier mapping (TARGETED not implemented).
func TestProfileToTier(t *testing.T) {
	cases := map[string]string{
		"fast":    "FAST",
		"Fast":    "FAST",
		"full":    "FULL",
		"release": "FULL",
		"nightly": "FULL",
		"":        "unknown",
		"bogus":   "unknown",
	}
	for profile, want := range cases {
		if got := profileToTier(profile); got != want {
			t.Errorf("profileToTier(%q) = %q, want %q", profile, got, want)
		}
	}
}

// TestPromotionGate: promotion_allowed is true ONLY for FULL pass — a FAST
// pass must never be treated as充分验证.
func TestPromotionGate(t *testing.T) {
	cases := []struct {
		status string
		tier   string
		want   bool
	}{
		{"pass", "FULL", true},
		{"pass", "FAST", false},
		{"pass", "unknown", false},
		{"fail", "FULL", false},
		{"unknown", "FULL", false},
	}
	for _, c := range cases {
		got := c.status == "pass" && c.tier == "FULL"
		if got != c.want {
			t.Errorf("promotion(%s,%s) = %v, want %v", c.status, c.tier, got, c.want)
		}
	}
}

// TestBuildTrustWithFullPass: verifier provenance + FULL pass surface in
// the Trust section.
func TestBuildTrustWithFullPass(t *testing.T) {
	verdicts := []VerdictEntry{
		{Plugin: "go-test", Tier: "FAST", Status: "pass", VerifierID: "go-test", VerifierVersion: "1.0.0"},
		{Plugin: "integration-test", Tier: "FULL", Status: "pass", VerifierID: "integration-test", VerifierVersion: "2.1.0"},
	}
	got := buildTrust(verdicts)
	if !got.TierImplemented {
		t.Errorf("tier_implemented must be true: %+v", got)
	}
	if got.MaxTier != "FULL" {
		t.Errorf("max tier = %q, want FULL", got.MaxTier)
	}
	if !got.HasFullPass {
		t.Errorf("has_full_pass must be true: %+v", got)
	}
	if !got.VerifierProvenance {
		t.Errorf("verifier provenance must be true (versions present): %+v", got)
	}
	if !strings.Contains(got.Detail, "FULL pass") {
		t.Errorf("detail should mention FULL pass: %s", got.Detail)
	}
}

// TestBuildTrustNoFullPass: without FULL pass the report must refuse the
// "充分验证" claim.
func TestBuildTrustNoFullPass(t *testing.T) {
	verdicts := []VerdictEntry{
		{Plugin: "go-test", Tier: "FAST", Status: "pass"},
	}
	got := buildTrust(verdicts)
	if got.MaxTier != "FAST" || got.HasFullPass {
		t.Errorf("trust = %+v, want MaxTier=FAST HasFullPass=false", got)
	}
	if !strings.Contains(got.Detail, "不得视为充分验证") {
		t.Errorf("detail must refuse sufficient-verification claim: %s", got.Detail)
	}
}

// TestHandleRunReportTool: the MCP entry point returns structured output.
func TestHandleRunReportTool(t *testing.T) {
	t.Setenv("RMS_API_KEY", "")
	dir := newGitRepo(t, "demo")
	fc := &fakeClient{}

	r, err := handleRunReport(fc, context.Background(), callReq(map[string]any{"path": dir}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	for _, want := range []string{"what_changed", "verification", "coverage", "limitations", "reconstructed"} {
		if !strings.Contains(body, want) {
			t.Errorf("report body missing %q: %s", want, body)
		}
	}
}

// TestBuildCoverageNativeIntent: dispatch-recorded intent (native v1) is the
// ONLY thing that moves native coverage off zero — message-derived
// reconstruction still never counts (R2 anti-gaming).
func TestBuildCoverageNativeIntent(t *testing.T) {
	changes := []ChangeEntry{
		{CommitSHA: "s1", Source: "reconstructed"},
		{CommitSHA: "s2", Source: "reconstructed", Intent: "from-message-only"},
	}
	verdicts := []VerdictEntry{
		{CommitSHA: "s1", Status: "pass", Intent: "task-9"}, // dispatch-recorded
	}
	got := buildCoverage(changes, verdicts)
	if got.NativeMutations != 1 {
		t.Errorf("native = %d, want 1 (only s1 has dispatch intent)", got.NativeMutations)
	}
	if got.ReconstructedMutations != 1 {
		t.Errorf("reconstructed = %d, want 1", got.ReconstructedMutations)
	}
}
