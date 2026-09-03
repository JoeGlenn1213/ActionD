// Copyright (c) 2025 JoeGlenn1213
// ActionD Failure Interpreter
// Analyzes error output and provides actionable suggestions

package interpreter

import (
	"regexp"
	"strings"
)

// FailurePattern represents a known failure pattern
type FailurePattern struct {
	ID          string
	Name        string
	Category    string // build, test, lint, dependency, permission, etc.
	Pattern     *regexp.Regexp
	Description string
	Hints       []string
	Severity    string // critical, warning, info
}

// FailureAnalysis contains the analysis result
type FailureAnalysis struct {
	Category         string   `json:"category"`
	Type             string   `json:"type"`
	Summary          string   `json:"summary"`
	Cause            string   `json:"cause"`
	Hints            []string `json:"hints"`
	RelatedFile      string   `json:"related_file,omitempty"`
	Severity         string   `json:"severity"`
	Confidence       float64  `json:"confidence"`
	EvidenceLines    int      `json:"evidence_lines"`
	AlternativeTypes []string `json:"alternative_types,omitempty"`
}

// Known failure patterns
var patterns = []FailurePattern{
	// === NPM / Node.js ===
	{
		ID:          "npm_install_failed",
		Name:        "npm install failed",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(npm ERR!|npm install.*failed|ECONNREFUSED.*registry)`),
		Description: "npm package installation failed",
		Hints: []string{
			"Check if the npm registry is accessible",
			"Try deleting node_modules and package-lock.json, then run npm install",
			"Check for network connectivity issues",
			"Verify the package.json has valid dependencies",
		},
		Severity: "critical",
	},
	{
		ID:          "npm_lockfile_mismatch",
		Name:        "package-lock.json mismatch",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(lockfile.*mismatch|package-lock.json.*out of sync)`),
		Description: "package-lock.json is out of sync with package.json",
		Hints: []string{
			"Run 'npm install' to update package-lock.json",
			"Commit the updated package-lock.json",
		},
		Severity: "warning",
	},
	{
		ID:          "npm_module_not_found",
		Name:        "Module not found",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(Cannot find module|Module not found|ERR_MODULE_NOT_FOUND)`),
		Description: "A required module could not be found",
		Hints: []string{
			"Run 'npm install' to install missing dependencies",
			"Check if the module name is correct",
			"Verify the module is listed in package.json",
		},
		Severity: "critical",
	},
	{
		ID:          "jest_test_failed",
		Name:        "Jest test failure",
		Category:    "test",
		Pattern:     regexp.MustCompile(`(?i)(FAIL\s+\S+\.test\.|jest.*failed|Test Suites:.*failed)`),
		Description: "One or more Jest tests failed",
		Hints: []string{
			"Check the test output for specific failing tests",
			"Run 'npm test -- --verbose' for more details",
			"Check if snapshots need updating: 'npm test -- -u'",
		},
		Severity: "warning",
	},

	// === Go ===
	{
		ID:          "go_mod_tidy",
		Name:        "go.mod needs tidy",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(go:.*missing.*in go\.mod|go\.mod.*inconsistent)`),
		Description: "go.mod file is inconsistent",
		Hints: []string{
			"Run 'go mod tidy' to fix module dependencies",
			"Ensure all imported packages are declared in go.mod",
		},
		Severity: "warning",
	},
	{
		ID:          "go_build_failed",
		Name:        "Go build failure",
		Category:    "build",
		Pattern:     regexp.MustCompile(`(?i)(build.*cannot find|undefined:|not declared|no such file)`),
		Description: "Go code failed to compile",
		Hints: []string{
			"Check for syntax errors or typos",
			"Ensure all imports are correct",
			"Run 'go build ./...' to see all errors",
		},
		Severity: "critical",
	},
	{
		ID:          "go_test_failed",
		Name:        "Go test failure",
		Category:    "test",
		Pattern:     regexp.MustCompile(`(?i)(FAIL\s+\S+\s+\[build|--- FAIL:|panic:|DATA RACE)`),
		Description: "Go tests failed",
		Hints: []string{
			"Check the test output for specific failures",
			"Run 'go test -v ./...' for verbose output",
			"If race condition, run with -race flag to identify",
		},
		Severity: "warning",
	},
	{
		ID:          "golangci_lint_failed",
		Name:        "golangci-lint errors",
		Category:    "lint",
		Pattern:     regexp.MustCompile(`(?i)(golangci-lint|errcheck|staticcheck|ineffassign|govet)`),
		Description: "Code style or quality issues detected",
		Hints: []string{
			"Review the lint errors in the output",
			"Run 'golangci-lint run --fix' to auto-fix some issues",
			"Configure .golangci.yml to adjust rules",
		},
		Severity: "warning",
	},

	// === Python ===
	{
		ID:          "python_module_not_found",
		Name:        "Python module not found",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(ModuleNotFoundError|ImportError|No module named)`),
		Description: "A required Python module is not installed",
		Hints: []string{
			"Install the missing module: pip install <module>",
			"Check requirements.txt for missing dependencies",
			"Ensure you're using the correct virtual environment",
		},
		Severity: "critical",
	},
	{
		ID:          "pytest_failed",
		Name:        "pytest failure",
		Category:    "test",
		Pattern:     regexp.MustCompile(`(?i)(FAILED\s+test_|pytest.*failed|ERROR at|AssertionError)`),
		Description: "Python tests failed",
		Hints: []string{
			"Check the test output for specific failures",
			"Run 'pytest -v' for verbose output",
			"Check for missing fixtures or resources",
		},
		Severity: "warning",
	},

	// === Java ===
	{
		ID:          "maven_build_failed",
		Name:        "Maven build failure",
		Category:    "build",
		Pattern:     regexp.MustCompile(`(?i)(BUILD FAILURE|Could not resolve dependencies|Failed to execute goal)`),
		Description: "Maven build failed",
		Hints: []string{
			"Check if all dependencies are available",
			"Run 'mvn clean install' for a fresh build",
			"Check the pom.xml for errors",
		},
		Severity: "critical",
	},
	{
		ID:          "gradle_build_failed",
		Name:        "Gradle build failure",
		Category:    "build",
		Pattern:     regexp.MustCompile(`(?i)(BUILD FAILED|Could not resolve|Task failed)`),
		Description: "Gradle build failed",
		Hints: []string{
			"Check if all dependencies are available",
			"Run './gradlew clean build' for a fresh build",
			"Check build.gradle for errors",
		},
		Severity: "critical",
	},
	{
		ID:          "java_test_failed",
		Name:        "Java test failure",
		Category:    "test",
		Pattern:     regexp.MustCompile(`(?i)(Tests run:.*Failures|Failed tests:|BUILD FAILURE.*test)`),
		Description: "Java tests failed",
		Hints: []string{
			"Check the test output for specific failures",
			"Run tests with verbose output",
			"Check for missing test resources",
		},
		Severity: "warning",
	},

	// === General ===
	{
		ID:          "permission_denied",
		Name:        "Permission denied",
		Category:    "permission",
		Pattern:     regexp.MustCompile(`(?i)(Permission denied|EACCES|access denied)`),
		Description: "Permission denied error",
		Hints: []string{
			"Check file permissions",
			"Run with appropriate privileges if needed",
			"Ensure the user has access to required resources",
		},
		Severity: "critical",
	},
	{
		ID:          "timeout",
		Name:        "Operation timeout",
		Category:    "timeout",
		Pattern:     regexp.MustCompile(`(?i)(timeout|timed out|deadline exceeded|context deadline)`),
		Description: "Operation timed out",
		Hints: []string{
			"Check network connectivity",
			"Increase timeout if needed",
			"Check if the service is responding slowly",
		},
		Severity: "warning",
	},
	{
		ID:          "out_of_memory",
		Name:        "Out of memory",
		Category:    "resource",
		Pattern:     regexp.MustCompile(`(?i)(out of memory|OOM|cannot allocate|heap)`),
		Description: "Process ran out of memory",
		Hints: []string{
			"Increase available memory",
			"Check for memory leaks in the code",
			"Reduce data set size or batch processing",
		},
		Severity: "critical",
	},
	{
		ID:          "command_not_found",
		Name:        "Command not found",
		Category:    "dependency",
		Pattern:     regexp.MustCompile(`(?i)(command not found|not recognized as an internal|no such file or directory.*\.sh)`),
		Description: "A required command is not installed",
		Hints: []string{
			"Install the missing command/program",
			"Check if the command is in PATH",
			"Verify the installation was successful",
		},
		Severity: "critical",
	},
}

// Analyze analyzes an error message and returns suggestions.
//
// Every known pattern is scored against the output (one point per matching
// line); the highest-scoring pattern becomes the primary diagnosis and the
// remaining matches are reported as alternative_types, so downstream agents
// can see competing hypotheses instead of a single first-match guess.
func Analyze(errorOutput string) *FailureAnalysis {
	if errorOutput == "" {
		return nil
	}

	lines := splitNonEmptyLines(errorOutput)

	type scoredPattern struct {
		pattern FailurePattern
		score   int
	}
	var matches []scoredPattern
	for _, p := range patterns {
		score := countMatchingLines(lines, p.Pattern)
		if score > 0 {
			matches = append(matches, scoredPattern{pattern: p, score: score})
		}
	}

	if len(matches) == 0 {
		// No pattern matched - return generic analysis
		return &FailureAnalysis{
			Category: "unknown",
			Type:     "unknown_error",
			Summary:  "An unexpected error occurred",
			Cause:    truncateError(errorOutput, 200),
			Hints: []string{
				"Check the error log for details",
				"Try running the command locally to reproduce",
			},
			Severity:   "warning",
			Confidence: 0.3,
		}
	}

	// Primary = highest line count; ties keep declaration order (stable).
	best := matches[0]
	for _, m := range matches[1:] {
		if m.score > best.score {
			best = m
		}
	}

	analysis := &FailureAnalysis{
		Category:      best.pattern.Category,
		Type:          best.pattern.ID,
		Summary:       best.pattern.Description,
		Cause:         determineCause(errorOutput, best.pattern),
		Hints:         best.pattern.Hints,
		RelatedFile:   extractRelatedFile(errorOutput),
		Severity:      best.pattern.Severity,
		Confidence:    confidenceFor(best.score, len(matches)),
		EvidenceLines: best.score,
	}

	if len(matches) > 1 {
		for _, m := range matches {
			if m.pattern.ID != best.pattern.ID {
				analysis.AlternativeTypes = append(analysis.AlternativeTypes, m.pattern.ID)
			}
		}
	}

	return analysis
}

// splitNonEmptyLines splits the output into trimmed, non-empty lines.
func splitNonEmptyLines(output string) []string {
	raw := strings.Split(output, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// countMatchingLines returns how many lines match the pattern.
func countMatchingLines(lines []string, re *regexp.Regexp) int {
	count := 0
	for _, line := range lines {
		if re.MatchString(line) {
			count++
		}
	}
	return count
}

// confidenceFor maps evidence strength to a deterministic confidence value:
// more matching lines raise confidence; competing pattern hypotheses lower it.
func confidenceFor(score, numMatches int) float64 {
	c := 0.6 + 0.1*float64(score)
	if c > 0.95 {
		c = 0.95
	}
	if numMatches > 1 {
		c -= 0.05
	}
	if c < 0.3 {
		c = 0.3
	}
	return c
}

// extractRelatedFile pulls the first file:line reference from the output.
func extractRelatedFile(output string) string {
	re := regexp.MustCompile(`(?m)(?:^|\s)([\w./\\-]+\.(?:go|py|ts|tsx|js|jsx|java|rb|rs|c|h|cpp):\d+)`)
	if m := re.FindStringSubmatch(output); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`File "([^"]+)"`).FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return ""
}

func determineCause(output string, p FailurePattern) string {
	// Try to extract a more specific cause from the output
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p.Pattern.MatchString(line) {
			return truncateError(line, 150)
		}
	}
	return p.Description
}

func truncateError(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetHintsForCategory returns common hints for a category
func GetHintsForCategory(category string) []string {
	switch category {
	case "dependency":
		return []string{
			"Check if all required packages are installed",
			"Verify version constraints in dependency files",
			"Try cleaning and reinstalling dependencies",
		}
	case "build":
		return []string{
			"Check for syntax errors",
			"Verify all required files exist",
			"Check build configuration",
		}
	case "test":
		return []string{
			"Run tests locally to reproduce",
			"Check test data and fixtures",
			"Verify test environment setup",
		}
	case "lint":
		return []string{
			"Review and fix reported issues",
			"Configure linter rules if needed",
			"Use auto-fix features where available",
		}
	case "timeout":
		return []string{
			"Increase timeout value",
			"Check for slow network responses",
			"Optimize slow operations",
		}
	default:
		return []string{
			"Check the error log for details",
			"Try reproducing the issue locally",
		}
	}
}
