// Copyright (c) 2025 JoeGlenn1213
// ActionD Structured Result Protocol
// Defines the standard format for plugin execution results

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ResultProtocolVersion is the version of the result protocol
const ResultProtocolVersion = "1.0"

// StructuredResult is the standard format for plugin execution results
// Plugins should write this to stdout as JSON or to a result.json file
type StructuredResult struct {
	// Protocol version
	Version string `json:"version,omitempty"`

	// Status: "success", "failure", "error", "skipped"
	Status string `json:"status"`

	// Human-readable summary (1-2 sentences)
	Summary string `json:"summary"`

	// Which step failed (if status is failure)
	FailedStep string `json:"failed_step,omitempty"`

	// List of artifact files produced
	Artifacts []string `json:"artifacts,omitempty"`

	// Execution metrics
	Metrics *ResultMetrics `json:"metrics,omitempty"`

	// AI-friendly hints for next steps
	Hints []string `json:"hints,omitempty"`

	// Error details (if status is error/failure)
	Error *ResultError `json:"error,omitempty"`

	// Additional structured data
	Details map[string]string `json:"details,omitempty"`

	// Timestamp
	Timestamp time.Time `json:"timestamp"`
}

// ResultMetrics contains execution metrics
type ResultMetrics struct {
	DurationMs int64 `json:"duration_ms,omitempty"`
	// For AI plugins
	TokensUsed int    `json:"tokens_used,omitempty"`
	ModelUsed  string `json:"model_used,omitempty"`
	// For test plugins
	TestsTotal   int `json:"tests_total,omitempty"`
	TestsPassed  int `json:"tests_passed,omitempty"`
	TestsFailed  int `json:"tests_failed,omitempty"`
	TestsSkipped int `json:"tests_skipped,omitempty"`
	// For lint plugins
	LintErrors   int `json:"lint_errors,omitempty"`
	LintWarnings int `json:"lint_warnings,omitempty"`
}

// ResultError contains error details
type ResultError struct {
	Type    string `json:"type"` // "timeout", "command_failed", "parse_error", etc.
	Message string `json:"message"`
	Code    int    `json:"code,omitempty"` // Exit code
	Raw     string `json:"raw,omitempty"`  // Raw error output
}

// NewSuccessResult creates a successful result
func NewSuccessResult(summary string) *StructuredResult {
	return &StructuredResult{
		Version:   ResultProtocolVersion,
		Status:    "success",
		Summary:   summary,
		Timestamp: time.Now(),
	}
}

// NewFailureResult creates a failure result
func NewFailureResult(summary, failedStep string, err error) *StructuredResult {
	result := &StructuredResult{
		Version:    ResultProtocolVersion,
		Status:     "failure",
		Summary:    summary,
		FailedStep: failedStep,
		Timestamp:  time.Now(),
	}
	if err != nil {
		result.Error = &ResultError{
			Type:    "plugin_error",
			Message: err.Error(),
		}
	}
	return result
}

// NewErrorResult creates an error result (infrastructure/process error)
func NewErrorResult(errType, message string, exitCode int) *StructuredResult {
	return &StructuredResult{
		Version: ResultProtocolVersion,
		Status:  "error",
		Summary: message,
		Error: &ResultError{
			Type:    errType,
			Message: message,
			Code:    exitCode,
		},
		Timestamp: time.Now(),
	}
}

// WithArtifacts adds artifacts to the result
func (r *StructuredResult) WithArtifacts(artifacts ...string) *StructuredResult {
	r.Artifacts = append(r.Artifacts, artifacts...)
	return r
}

// WithHint adds a hint to the result
func (r *StructuredResult) WithHint(hint string) *StructuredResult {
	r.Hints = append(r.Hints, hint)
	return r
}

// WithMetric adds a detail to the result
func (r *StructuredResult) WithDetail(key, value string) *StructuredResult {
	if r.Details == nil {
		r.Details = make(map[string]string)
	}
	r.Details[key] = value
	return r
}

// WithMetrics sets the metrics
func (r *StructuredResult) WithMetrics(m *ResultMetrics) *StructuredResult {
	r.Metrics = m
	return r
}

// ToJSON converts the result to JSON
func (r *StructuredResult) ToJSON() (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteToFile writes the result to a result.json file
func (r *StructuredResult) WriteToFile(dir string) error {
	path := filepath.Join(dir, "result.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write result file: %w", err)
	}
	return nil
}

// ParseResult parses a StructuredResult from JSON
func ParseResult(data []byte) (*StructuredResult, error) {
	var result StructuredResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}
	return &result, nil
}

// ReadResultFromFile reads a result.json file
func ReadResultFromFile(dir string) (*StructuredResult, error) {
	path := filepath.Join(dir, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read result file: %w", err)
	}
	return ParseResult(data)
}

// IsSuccess returns true if the result indicates success
func (r *StructuredResult) IsSuccess() bool {
	return r.Status == "success"
}

// IsFailure returns true if the result indicates failure
func (r *StructuredResult) IsFailure() bool {
	return r.Status == "failure"
}

// IsError returns true if the result indicates an infrastructure error
func (r *StructuredResult) IsError() bool {
	return r.Status == "error"
}

// Common error types
const (
	ErrorTypeTimeout       = "timeout"
	ErrorTypeCommandFailed = "command_failed"
	ErrorTypeParseError    = "parse_error"
	ErrorTypeMissingDep    = "missing_dependency"
	ErrorTypePermission    = "permission_denied"
	ErrorTypeCanceled      = "canceled"
)

// Common failure patterns (for Hints)
var CommonFailureHints = map[string][]string{
	"npm_install_failed": {
		"Try deleting node_modules and package-lock.json, then run npm install again",
		"Check if the npm registry is accessible",
	},
	"go_mod_tidy_failed": {
		"Run 'go mod tidy' to fix module dependencies",
		"Check if all imported packages are correctly declared in go.mod",
	},
	"test_failed": {
		"Check the test output for specific test failures",
		"Run tests locally to reproduce the issue",
	},
	"lint_failed": {
		"Review the lint errors in the output",
		"Run 'golangci-lint run --fix' to auto-fix some issues",
	},
	"build_failed": {
		"Check the build errors in the output",
		"Ensure all dependencies are installed",
	},
}
