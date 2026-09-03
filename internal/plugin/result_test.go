package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSuccessResult(t *testing.T) {
	result := NewSuccessResult("All checks passed")

	if !result.IsSuccess() {
		t.Error("expected IsSuccess to be true")
	}
	if result.IsFailure() {
		t.Error("expected IsFailure to be false")
	}
	if result.IsError() {
		t.Error("expected IsError to be false")
	}
	if result.Summary != "All checks passed" {
		t.Errorf("Summary mismatch: got %s", result.Summary)
	}
	if result.Version != ResultProtocolVersion {
		t.Errorf("Version mismatch: got %s", result.Version)
	}
	if result.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestNewFailureResult(t *testing.T) {
	err := &testError{msg: "lint error"}
	result := NewFailureResult("Lint failed", "go vet", err)

	if !result.IsFailure() {
		t.Error("expected IsFailure to be true")
	}
	if result.IsSuccess() {
		t.Error("expected IsSuccess to be false")
	}
	if result.FailedStep != "go vet" {
		t.Errorf("FailedStep mismatch: got %s", result.FailedStep)
	}
	if result.Error == nil {
		t.Fatal("Error should be set")
	}
	if result.Error.Type != "plugin_error" {
		t.Errorf("Error.Type mismatch: got %s", result.Error.Type)
	}
}

func TestNewFailureResultWithoutError(t *testing.T) {
	result := NewFailureResult("Test failed", "test phase", nil)

	if !result.IsFailure() {
		t.Error("expected IsFailure to be true")
	}
	if result.Error != nil {
		t.Error("Error should be nil when no error passed")
	}
}

func TestNewErrorResult(t *testing.T) {
	result := NewErrorResult(ErrorTypeTimeout, "Command timed out", 124)

	if !result.IsError() {
		t.Error("expected IsError to be true")
	}
	if result.IsSuccess() {
		t.Error("expected IsSuccess to be false")
	}
	if result.IsFailure() {
		t.Error("expected IsFailure to be false")
	}
	if result.Error == nil {
		t.Fatal("Error should be set")
	}
	if result.Error.Type != ErrorTypeTimeout {
		t.Errorf("Error.Type mismatch: got %s", result.Error.Type)
	}
	if result.Error.Code != 124 {
		t.Errorf("Error.Code mismatch: got %d", result.Error.Code)
	}
}

func TestWithArtifacts(t *testing.T) {
	result := NewSuccessResult("Build succeeded")
	result.WithArtifacts("build/output.bin")
	result.WithArtifacts("build/log.txt", "build/coverage.json")

	if len(result.Artifacts) != 3 {
		t.Errorf("Expected 3 artifacts, got %d", len(result.Artifacts))
	}
	if result.Artifacts[0] != "build/output.bin" {
		t.Errorf("First artifact mismatch: got %s", result.Artifacts[0])
	}
}

func TestWithHint(t *testing.T) {
	result := NewSuccessResult("Tests passed")
	result.WithHint("Run full test suite")
	result.WithHint("Check coverage report")

	if len(result.Hints) != 2 {
		t.Errorf("Expected 2 hints, got %d", len(result.Hints))
	}
}

func TestWithDetail(t *testing.T) {
	result := NewSuccessResult("Lint passed")
	result.WithDetail("lint_version", "1.5.0")
	result.WithDetail("issues_found", "0")

	if len(result.Details) != 2 {
		t.Errorf("Expected 2 details, got %d", len(result.Details))
	}
	if result.Details["lint_version"] != "1.5.0" {
		t.Errorf("lint_version mismatch: got %s", result.Details["lint_version"])
	}
}

func TestWithDetailInitializesMap(t *testing.T) {
	result := NewSuccessResult("Test passed")
	// Should not panic when Details is nil
	result.WithDetail("key", "value")
}

func TestWithMetrics(t *testing.T) {
	result := NewSuccessResult("Build complete")
	metrics := &ResultMetrics{
		DurationMs: 5000,
	}
	result.WithMetrics(metrics)

	if result.Metrics == nil {
		t.Fatal("Metrics should be set")
	}
	if result.Metrics.DurationMs != 5000 {
		t.Errorf("DurationMs mismatch: got %d", result.Metrics.DurationMs)
	}
}

func TestToJSON(t *testing.T) {
	result := NewSuccessResult("Test passed")
	result.WithArtifacts("test.log")

	json, err := result.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	if json == "" {
		t.Error("JSON should not be empty")
	}
	// Should contain key fields
	if json == "{}" {
		t.Error("JSON should not be empty object")
	}
}

func TestWriteToFile(t *testing.T) {
	result := NewSuccessResult("Build succeeded")

	tmpDir := t.TempDir()
	err := result.WriteToFile(tmpDir)
	if err != nil {
		t.Fatalf("WriteToFile failed: %v", err)
	}

	// Verify file exists
	path := filepath.Join(tmpDir, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read result file: %v", err)
	}

	if len(data) == 0 {
		t.Error("Result file should not be empty")
	}
}

func TestParseResult(t *testing.T) {
	jsonData := []byte(`{
		"version": "1.0",
		"status": "success",
		"summary": "All tests passed",
		"artifacts": ["coverage.json"],
		"hints": ["Review coverage report"]
	}`)

	result, err := ParseResult(jsonData)
	if err != nil {
		t.Fatalf("ParseResult failed: %v", err)
	}

	if !result.IsSuccess() {
		t.Error("Expected success status")
	}
	if result.Summary != "All tests passed" {
		t.Errorf("Summary mismatch: got %s", result.Summary)
	}
	if len(result.Artifacts) != 1 {
		t.Errorf("Expected 1 artifact, got %d", len(result.Artifacts))
	}
	if len(result.Hints) != 1 {
		t.Errorf("Expected 1 hint, got %d", len(result.Hints))
	}
}

func TestParseResultError(t *testing.T) {
	invalidJSON := []byte(`{invalid json}`)

	_, err := ParseResult(invalidJSON)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestReadResultFromFile(t *testing.T) {
	result := NewSuccessResult("Lint passed")
	tmpDir := t.TempDir()

	err := result.WriteToFile(tmpDir)
	if err != nil {
		t.Fatalf("WriteToFile failed: %v", err)
	}

	readResult, err := ReadResultFromFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadResultFromFile failed: %v", err)
	}

	if !readResult.IsSuccess() {
		t.Error("Expected success status")
	}
	if readResult.Summary != "Lint passed" {
		t.Errorf("Summary mismatch: got %s", readResult.Summary)
	}
}

func TestReadResultFromFileError(t *testing.T) {
	tmpDir := t.TempDir()
	// File doesn't exist

	_, err := ReadResultFromFile(tmpDir)
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestResultStatusMethods(t *testing.T) {
	success := NewSuccessResult("ok")
	failure := NewFailureResult("failed", "step", nil)
	error := NewErrorResult(ErrorTypeTimeout, "timeout", 124)

	// Success checks
	if !success.IsSuccess() {
		t.Error("success should be IsSuccess")
	}
	if success.IsFailure() {
		t.Error("success should not be IsFailure")
	}
	if success.IsError() {
		t.Error("success should not be IsError")
	}

	// Failure checks
	if failure.IsSuccess() {
		t.Error("failure should not be IsSuccess")
	}
	if !failure.IsFailure() {
		t.Error("failure should be IsFailure")
	}
	if failure.IsError() {
		t.Error("failure should not be IsError")
	}

	// Error checks
	if error.IsSuccess() {
		t.Error("error should not be IsSuccess")
	}
	if error.IsFailure() {
		t.Error("error should not be IsFailure")
	}
	if !error.IsError() {
		t.Error("error should be IsError")
	}
}

func TestResultMetrics(t *testing.T) {
	metrics := &ResultMetrics{
		DurationMs:   1000,
		TestsTotal:   100,
		TestsPassed:  95,
		TestsFailed:  5,
		TestsSkipped: 0,
		LintErrors:   2,
		LintWarnings: 10,
	}

	result := NewSuccessResult("Tests complete")
	result.WithMetrics(metrics)

	if result.Metrics.TestsTotal != 100 {
		t.Errorf("TestsTotal mismatch: got %d", result.Metrics.TestsTotal)
	}
	if result.Metrics.TestsPassed != 95 {
		t.Errorf("TestsPassed mismatch: got %d", result.Metrics.TestsPassed)
	}
	if result.Metrics.LintErrors != 2 {
		t.Errorf("LintErrors mismatch: got %d", result.Metrics.LintErrors)
	}
}

func TestErrorTypes(t *testing.T) {
	if ErrorTypeTimeout != "timeout" {
		t.Errorf("ErrorTypeTimeout mismatch")
	}
	if ErrorTypeCommandFailed != "command_failed" {
		t.Errorf("ErrorTypeCommandFailed mismatch")
	}
	if ErrorTypeParseError != "parse_error" {
		t.Errorf("ErrorTypeParseError mismatch")
	}
	if ErrorTypeMissingDep != "missing_dependency" {
		t.Errorf("ErrorTypeMissingDep mismatch")
	}
	if ErrorTypePermission != "permission_denied" {
		t.Errorf("ErrorTypePermission mismatch")
	}
	if ErrorTypeCanceled != "canceled" {
		t.Errorf("ErrorTypeCanceled mismatch")
	}
}

func TestCommonFailureHints(t *testing.T) {
	hints, ok := CommonFailureHints["npm_install_failed"]
	if !ok {
		t.Error("npm_install_failed hints should exist")
	}
	if len(hints) < 1 {
		t.Error("npm_install_failed should have hints")
	}

	_, ok = CommonFailureHints["go_mod_tidy_failed"]
	if !ok {
		t.Error("go_mod_tidy_failed hints should exist")
	}

	_, ok = CommonFailureHints["test_failed"]
	if !ok {
		t.Error("test_failed hints should exist")
	}

	_, ok = CommonFailureHints["lint_failed"]
	if !ok {
		t.Error("lint_failed hints should exist")
	}

	_, ok = CommonFailureHints["build_failed"]
	if !ok {
		t.Error("build_failed hints should exist")
	}
}

// testError is a simple error for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
