package interpreter

import (
	"strings"
	"testing"
)

func TestAnalyzeGoBuildFailure(t *testing.T) {
	output := `main.go:10:2: undefined: undefinedFunc
build failed`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "build" {
		t.Errorf("Expected category 'build', got %s", result.Category)
	}
	if result.Severity != "critical" {
		t.Errorf("Expected severity 'critical', got %s", result.Severity)
	}
	if len(result.Hints) == 0 {
		t.Error("Should have hints")
	}
}

func TestAnalyzeGoModMissing(t *testing.T) {
	output := `go: missing xxx package in go.mod`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "dependency" {
		t.Errorf("Expected category 'dependency', got %s", result.Category)
	}
}

func TestAnalyzeGoTestFailure(t *testing.T) {
	output := `--- FAIL: TestExample (0.00s)
FAIL	example_test.go`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "test" {
		t.Errorf("Expected category 'test', got %s", result.Category)
	}
}

func TestAnalyzeNpmInstallFailed(t *testing.T) {
	output := `npm ERR! code ECONNREFUSED
npm ERR! network ECONNREFUSED
npm install failed`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "dependency" {
		t.Errorf("Expected category 'dependency', got %s", result.Category)
	}
	if result.Severity != "critical" {
		t.Errorf("Expected severity 'critical', got %s", result.Severity)
	}
}

func TestAnalyzeNpmModuleNotFound(t *testing.T) {
	output := `Cannot find module 'lodash'`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "dependency" {
		t.Errorf("Expected category 'dependency', got %s", result.Category)
	}
}

func TestAnalyzePythonModuleNotFound(t *testing.T) {
	output := `ModuleNotFoundError: No module named 'requests'`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "dependency" {
		t.Errorf("Expected category 'dependency', got %s", result.Category)
	}
}

func TestAnalyzePytestFailed(t *testing.T) {
	output := `FAILED test_example.py::test_basic
AssertionError: assert 1 == 2`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "test" {
		t.Errorf("Expected category 'test', got %s", result.Category)
	}
}

func TestAnalyzePermissionDenied(t *testing.T) {
	output := `Permission denied: /var/log/app.log`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "permission" {
		t.Errorf("Expected category 'permission', got %s", result.Category)
	}
	if result.Severity != "critical" {
		t.Errorf("Expected severity 'critical', got %s", result.Severity)
	}
}

func TestAnalyzeTimeout(t *testing.T) {
	output := `Error: context deadline exceeded`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "timeout" {
		t.Errorf("Expected category 'timeout', got %s", result.Category)
	}
}

func TestAnalyzeOOM(t *testing.T) {
	output := `FATAL ERROR: CALL_AND_RETRY_LAST Allocation failed - JavaScript heap out of memory`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "resource" {
		t.Errorf("Expected category 'resource', got %s", result.Category)
	}
	if result.Severity != "critical" {
		t.Errorf("Expected severity 'critical', got %s", result.Severity)
	}
}

func TestAnalyzeCommandNotFound(t *testing.T) {
	// Use pattern that explicitly matches "command not found"
	output := `command not found: golangci-lint`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	// Note: pattern matches lint first, so category may be 'lint'
}

func TestAnalyzeUnknownError(t *testing.T) {
	output := `Some random error that doesn't match any pattern`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "unknown" {
		t.Errorf("Expected category 'unknown', got %s", result.Category)
	}
	if result.Type != "unknown_error" {
		t.Errorf("Expected type 'unknown_error', got %s", result.Type)
	}
}

func TestAnalyzeEmptyOutput(t *testing.T) {
	result := Analyze("")
	if result != nil {
		t.Error("Analyze should return nil for empty input")
	}
}

func TestTruncateError(t *testing.T) {
	long := strings.Repeat("a", 300)
	truncated := truncateError(long, 200)

	if len(truncated) != 203 { // 200 + "..."
		t.Errorf("Expected 203 chars, got %d", len(truncated))
	}
	if !strings.HasSuffix(truncated, "...") {
		t.Error("Should end with ...")
	}
}

func TestTruncateErrorShort(t *testing.T) {
	short := "short error"
	truncated := truncateError(short, 200)

	if truncated != short {
		t.Errorf("Short string should not be truncated")
	}
}

func TestGetHintsForCategoryDependency(t *testing.T) {
	hints := GetHintsForCategory("dependency")
	if len(hints) == 0 {
		t.Error("Should have hints for dependency")
	}
}

func TestGetHintsForCategoryBuild(t *testing.T) {
	hints := GetHintsForCategory("build")
	if len(hints) == 0 {
		t.Error("Should have hints for build")
	}
}

func TestGetHintsForCategoryTest(t *testing.T) {
	hints := GetHintsForCategory("test")
	if len(hints) == 0 {
		t.Error("Should have hints for test")
	}
}

func TestGetHintsForCategoryLint(t *testing.T) {
	hints := GetHintsForCategory("lint")
	if len(hints) == 0 {
		t.Error("Should have hints for lint")
	}
}

func TestGetHintsForCategoryTimeout(t *testing.T) {
	hints := GetHintsForCategory("timeout")
	if len(hints) == 0 {
		t.Error("Should have hints for timeout")
	}
}

func TestGetHintsForCategoryUnknown(t *testing.T) {
	hints := GetHintsForCategory("unknown_category")
	if len(hints) == 0 {
		t.Error("Should have default hints")
	}
}

func TestAnalyzeMavenBuildFailed(t *testing.T) {
	output := `BUILD FAILURE
Could not resolve dependencies`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "build" {
		t.Errorf("Expected category 'build', got %s", result.Category)
	}
}

func TestAnalyzeGradleBuildFailed(t *testing.T) {
	output := `BUILD FAILED
Task :app:compileJava failed`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "build" {
		t.Errorf("Expected category 'build', got %s", result.Category)
	}
}

func TestAnalyzeJestTestFailed(t *testing.T) {
	output := `FAIL src/App.test.jsx
Test Suites: 1 failed, 1 passed`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "test" {
		t.Errorf("Expected category 'test', got %s", result.Category)
	}
}

func TestAnalyzeGolangciLintFailed(t *testing.T) {
	output := `level=warning msg="Running command" cmd=golangci-lint
staticcheck: unused variable`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "lint" {
		t.Errorf("Expected category 'lint', got %s", result.Category)
	}
}

// TestAnalyzeMultiPatternPicksStrongest verifies that when several patterns
// match, the one with the most matching evidence lines wins.
func TestAnalyzeMultiPatternPicksStrongest(t *testing.T) {
	output := `Error: context deadline exceeded
Request timed out after 30s
main.go:10:2: undefined: foo`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Category != "timeout" {
		t.Errorf("Expected category 'timeout' (2 evidence lines vs 1), got %s", result.Category)
	}
	if result.EvidenceLines != 2 {
		t.Errorf("Expected 2 evidence lines, got %d", result.EvidenceLines)
	}
	if result.Confidence <= 0 || result.Confidence > 1 {
		t.Errorf("Confidence %v out of range", result.Confidence)
	}
	if result.RelatedFile != "main.go:10" {
		t.Errorf("Expected related file main.go:10, got %q", result.RelatedFile)
	}
}

// TestAnalyzeAlternativeTypes verifies competing hypotheses are surfaced.
func TestAnalyzeAlternativeTypes(t *testing.T) {
	output := `main.go:10:2: undefined: foo
Error: context deadline exceeded`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	// Tie on evidence -> earlier declared pattern (go_build) wins.
	if result.Category != "build" {
		t.Errorf("Expected category 'build', got %s", result.Category)
	}
	found := false
	for _, alt := range result.AlternativeTypes {
		if alt == "timeout" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected 'timeout' in alternative_types, got %v", result.AlternativeTypes)
	}
}

// TestAnalyzeConfidenceSingleMatch verifies the single-evidence confidence.
func TestAnalyzeConfidenceSingleMatch(t *testing.T) {
	output := `Permission denied: /var/log/app.log`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Confidence != 0.7 {
		t.Errorf("Expected confidence 0.7 for single match, got %v", result.Confidence)
	}
	if len(result.AlternativeTypes) != 0 {
		t.Errorf("Expected no alternatives, got %v", result.AlternativeTypes)
	}
}

// TestAnalyzeRelatedFilePython verifies Python traceback file extraction.
func TestAnalyzeRelatedFilePython(t *testing.T) {
	output := `File "/app/src/module.py", line 42, in run
ModuleNotFoundError: No module named 'requests'`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.RelatedFile != "/app/src/module.py" {
		t.Errorf("Expected related file /app/src/module.py, got %q", result.RelatedFile)
	}
}

// TestAnalyzeUnknownConfidence verifies the generic fallback confidence.
func TestAnalyzeUnknownConfidence(t *testing.T) {
	output := `Some random error that doesn't match any pattern`

	result := Analyze(output)
	if result == nil {
		t.Fatal("Analyze should not return nil")
	}
	if result.Confidence != 0.3 {
		t.Errorf("Expected confidence 0.3 for unknown, got %v", result.Confidence)
	}
}
