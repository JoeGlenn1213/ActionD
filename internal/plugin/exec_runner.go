// Copyright (c) 2025 JoeGlenn1213
// ActionD ExecPlugin - Run external commands as plugins

package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/interpreter"
)

// baseToolchainPathDirs lists well-known toolchain locations that plugin
// commands (go, golangci-lint, python3, ...) may live in. They are prepended
// to PATH when plugins execute so toolchains resolve even when the daemon
// itself runs under a minimal environment (e.g. a launchd default PATH of
// /usr/bin:/bin — which has cost CI failures like "no such file or
// directory: 'go'"). Entries may not exist on this platform; PATH tolerates
// missing dirs, so no per-OS gating is needed.
var baseToolchainPathDirs = []string{
	"/opt/homebrew/bin",  // macOS (Apple Silicon) Homebrew
	"/opt/homebrew/sbin", // macOS (Apple Silicon) Homebrew
	"/usr/local/bin",     // macOS (Intel) Homebrew & generic Linux local installs
	"/usr/local/go/bin",  // Go default install prefix
}

// macOSJVMRoot is scanned for installed JDKs so java/mvn resolve without
// relying on the daemon's ambient PATH. It simply doesn't exist elsewhere.
const macOSJVMRoot = "/Library/Java/JavaVirtualMachines"

// pluginEnv returns the environment for plugin subprocesses: the daemon's
// own environment plus a PATH whose head is the toolchain dirs (including
// any installed JDK bin dirs), so plugin commands never depend on the
// ambient PATH of whoever started the daemon. It must stay safe for
// concurrent use, so the candidate list is built per call.
func pluginEnv() []string {
	base := os.Environ()

	candidates := slices.Clone(baseToolchainPathDirs)
	if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
		candidates = append(candidates, filepath.Join(userHome, ".local", "bin"))
	}
	if entries, err := os.ReadDir(macOSJVMRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates,
					filepath.Join(macOSJVMRoot, e.Name(), "Contents", "Home", "bin"))
			}
		}
	}

	seen := make(map[string]bool, len(candidates))
	head := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		head = append(head, dir)
	}

	newPath := strings.Join(head, string(os.PathListSeparator))
	if existing := os.Getenv("PATH"); existing != "" {
		newPath += string(os.PathListSeparator) + existing
	}
	// os.Environ may already carry PATH; override it with a single value.
	filtered := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if !strings.HasPrefix(kv, "PATH=") {
			filtered = append(filtered, kv)
		}
	}
	return append(filtered, "PATH="+newPath)
}

// ExecPluginConfig configures an ExecPlugin
type ExecPluginConfig struct {
	Name       string        `yaml:"name"`
	Version    string        `yaml:"version"` // Manifest version (verifier provenance)
	Command    string        `yaml:"command"`
	Args       []string      `yaml:"args"`
	Triggers   []string      `yaml:"triggers"`
	Languages  []string      `yaml:"languages"` // Supported languages, ["*"] for all
	Timeout    time.Duration `yaml:"timeout"`
	WorkingDir string        `yaml:"working_dir"`
	RefFilter  string        `yaml:"ref_filter"` // Glob pattern for Ref (e.g. "refs/tags/*")
	RepoFilter string        `yaml:"repo_filter"`
	Profiles   []string      `yaml:"profiles"` // Supported execution profiles (e.g. "fast", "full", "release")
}

// ExecPlugin wraps an external executable as a Plugin
// This allows Python/Node/any external script to act as an ActionD plugin
type ExecPlugin struct {
	name       string
	version    string   // Manifest version (verifier provenance)
	command    string   // Path to executable
	args       []string // Base arguments
	triggers   []string // Event types to respond to
	languages  []string // Supported languages
	timeout    time.Duration
	workingDir string
	refFilter  string
	repoFilter string
	profiles   []string
}

// ExecInput is the JSON sent to the external command via stdin
type ExecInput struct {
	Event       event.Event `json:"event"`
	RepoPath    string      `json:"repo_path"`
	ArtifactDir string      `json:"artifact_dir"`
	Diff        string      `json:"diff,omitempty"`
	Files       []string    `json:"files,omitempty"`
}

// ExecOutput is the JSON expected from the external command via stdout
type ExecOutput struct {
	Status    string   `json:"status"` // "success" or "error"
	Error     string   `json:"error,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"` // List of created files
	Model     string   `json:"model,omitempty"`     // AI model used

	// Gate-level decision: "pass" | "deny" | "rejected" | "expired" | "revoked" | "error".
	// deny/rejected/expired/revoked/error force a failure (fail-closed).
	Decision string `json:"decision,omitempty"`
	Tokens   int    `json:"tokens,omitempty"` // Tokens consumed
	Duration int64  `json:"duration_ms,omitempty"`

	// Structured result fields (v0.3)
	// This matches the StructuredResult type defined in result.go
	Summary    string            `json:"summary,omitempty"`
	FailedStep string            `json:"failed_step,omitempty"`
	Hints      []string          `json:"hints,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
}

// NewExecPlugin creates a new external executable plugin
func NewExecPlugin(cfg ExecPluginConfig) *ExecPlugin {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	// Default to wildcard if no languages specified
	if len(cfg.Languages) == 0 {
		cfg.Languages = []string{"*"}
	}
	return &ExecPlugin{
		name:       cfg.Name,
		version:    cfg.Version,
		command:    cfg.Command,
		args:       cfg.Args,
		triggers:   cfg.Triggers,
		languages:  cfg.Languages,
		timeout:    cfg.Timeout,
		workingDir: cfg.WorkingDir,
		refFilter:  cfg.RefFilter,
		repoFilter: cfg.RepoFilter,
		profiles:   cfg.Profiles,
	}
}

// Name returns the plugin identifier
func (p *ExecPlugin) Name() string {
	return p.name
}

// Version returns the plugin manifest version (empty when unknown).
func (p *ExecPlugin) Version() string {
	return p.version
}

// Triggers returns the event types this plugin responds to
func (p *ExecPlugin) Triggers() []string {
	return p.triggers
}

// Languages returns the programming languages this plugin supports
func (p *ExecPlugin) Languages() []string {
	return p.languages
}

// RefFilter returns the ref filter pattern
func (p *ExecPlugin) RefFilter() string {
	return p.refFilter
}

// RepoFilter returns the repository filter pattern.
func (p *ExecPlugin) RepoFilter() string {
	return p.repoFilter
}

// Profiles returns the supported execution profiles for this plugin.
func (p *ExecPlugin) Profiles() []string {
	return p.profiles
}

// Config returns the plugin configuration so callers can rebuild with overrides.
func (p *ExecPlugin) Config() ExecPluginConfig {
	return ExecPluginConfig{
		Name:       p.name,
		Version:    p.version,
		Command:    p.command,
		Args:       append([]string(nil), p.args...),
		Triggers:   append([]string(nil), p.triggers...),
		Languages:  append([]string(nil), p.languages...),
		Timeout:    p.timeout,
		WorkingDir: p.workingDir,
		RefFilter:  p.refFilter,
		RepoFilter: p.repoFilter,
		Profiles:   append([]string(nil), p.profiles...),
	}
}

// Match determines if this plugin should run
func (p *ExecPlugin) Match(evt event.Event) bool {
	if p.repoFilter != "" && !matchesRepoFilter(p.repoFilter, evt.Repo) {
		return false
	}

	if p.refFilter == "" {
		return true
	}

	ref := evt.Ref
	if ref == "" {
		if r, ok := evt.Payload["ref"].(string); ok {
			ref = r
		}
	}

	if ref == "" {
		return false
	}

	matched, _ := filepath.Match(p.refFilter, ref)
	return matched
}

func matchesRepoFilter(pattern, repo string) bool {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return false
	}

	candidates := []string{repo}
	trimmed := strings.TrimSuffix(repo, ".git")
	if trimmed != repo {
		candidates = append(candidates, trimmed)
	}

	normalizedPattern := strings.TrimSpace(pattern)
	patterns := []string{normalizedPattern}
	if strings.HasSuffix(normalizedPattern, ".git") {
		patterns = append(patterns, strings.TrimSuffix(normalizedPattern, ".git"))
	}

	for _, currentPattern := range patterns {
		for _, candidate := range candidates {
			if matched, _ := filepath.Match(currentPattern, candidate); matched {
				return true
			}
		}
	}

	return false
}

// Run executes the external command with streaming output
func (p *ExecPlugin) Run(ctx Context) error {
	// Prepare input JSON
	input := ExecInput{
		Event:    ctx.Event,
		RepoPath: ctx.RepoPath,
		Diff:     ctx.Diff,
		Files:    ctx.Files,
	}

	// Get artifact directory if available
	if ctx.Artifacts != nil {
		input.ArtifactDir = ctx.Artifacts.Dir()
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal input: %w", err)
	}

	// Build command arguments
	args := append(p.args,
		"--repo", ctx.RepoPath,
	)
	if input.ArtifactDir != "" {
		args = append(args, "--out", input.ArtifactDir)
	}

	// Create command with timeout
	parentCtx := ctx.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	execCtx, cancel := context.WithTimeout(parentCtx, p.timeout)
	defer cancel()

	cmd := exec.Command(p.command, args...)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Env = pluginEnv()

	// Set process group ID to isolate child processes
	setProcessGroup(cmd)

	if p.workingDir != "" {
		cmd.Dir = p.workingDir
	}

	// Set up pipes for streaming output
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Buffers to also capture full output for JSON parsing
	var stdoutBuf, stderrBuf bytes.Buffer

	// Start command
	fmt.Printf("   🔧 Executing: %s %v\n", p.command, args)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Wait for context cancellation or timeout to kill the process group
	go func() {
		<-execCtx.Done()
		if execCtx.Err() != nil {
			killProcessGroup(cmd)
		}
	}()

	// Stream output in goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout
	go func() {
		defer wg.Done()
		streamOutput(stdoutPipe, &stdoutBuf, ctx.LogWriter, false)
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		streamOutput(stderrPipe, &stderrBuf, ctx.LogWriter, true)
	}()

	// Wait for output streaming to complete
	wg.Wait()

	// Wait for command to finish
	cmdErr := cmd.Wait()
	if cmdErr != nil {
		if errors.Is(execCtx.Err(), context.Canceled) {
			return context.Canceled
		}
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
	}

	// Parse output and construct structured result
	var result *StructuredResult
	var output ExecOutput
	var actionResult map[string]interface{}
	var isJSON bool

	if stdoutBuf.Len() > 0 {
		// First try parsing as V1 ActionResult
		if err := json.Unmarshal(stdoutBuf.Bytes(), &actionResult); err == nil && actionResult["action_id"] != nil {
			isJSON = true

			// Map V1 ActionResult back to StructuredResult for compatibility
			status := "success"
			if s, ok := actionResult["status"].(string); ok {
				status = s
			}

			// Gate-level decision (deny/rejected/expired/revoked/error must block)
			decision := ""
			if d, ok := actionResult["decision"].(string); ok {
				decision = d
			}

			summaryMsg := "Plugin executed successfully"
			if sum, ok := actionResult["summary"].(map[string]interface{}); ok {
				if msg, ok := sum["message"].(string); ok {
					summaryMsg = msg
				}
			}

			result = &StructuredResult{
				Version:   ResultProtocolVersion,
				Status:    status,
				Summary:   summaryMsg,
				Timestamp: time.Now(),
			}

			if hintsRaw, ok := actionResult["hints"].([]interface{}); ok {
				for _, h := range hintsRaw {
					if hStr, ok := h.(string); ok {
						result.Hints = append(result.Hints, hStr)
					}
				}
			}

			if arts, ok := actionResult["artifacts"].([]interface{}); ok {
				for _, a := range arts {
					if am, ok := a.(map[string]interface{}); ok {
						if path, ok := am["path"].(string); ok {
							result.Artifacts = append(result.Artifacts, path)
						}
					}
				}
			}

			// Fail-closed: gate-level decisions force failure even when status says success.
			if isDenyDecision(decision) {
				result.Status = "failed"
				result.Error = &ResultError{
					Type:    decisionErrorType(decision),
					Message: summaryMsg,
				}
			} else if status != "success" && status != "warning" {
				result.Error = &ResultError{
					Type:    "plugin_error",
					Message: summaryMsg,
				}
			}

		} else if err := json.Unmarshal(stdoutBuf.Bytes(), &output); err == nil {
			// Fallback to legacy ExecOutput format
			isJSON = true
			// Valid JSON output
			result = &StructuredResult{
				Version:    ResultProtocolVersion,
				Status:     output.Status,
				Summary:    output.Summary,
				FailedStep: output.FailedStep,
				Artifacts:  output.Artifacts,
				Hints:      output.Hints,
				Details:    output.Details,
				Timestamp:  time.Now(),
			}
			if output.Model != "" || output.Tokens > 0 || output.Duration > 0 {
				result.Metrics = &ResultMetrics{
					DurationMs: output.Duration,
					ModelUsed:  output.Model,
					TokensUsed: output.Tokens,
				}
			}

			// Fail-closed: gate-level decisions force failure even when status says success.
			if isDenyDecision(output.Decision) {
				result.Status = "failed"
				result.Error = &ResultError{
					Type:    decisionErrorType(output.Decision),
					Message: result.Summary,
				}
				if result.Error.Message == "" {
					result.Error.Message = "plugin decision: " + output.Decision
				}
				if result.Summary == "" {
					result.Summary = result.Error.Message
				}
			} else if output.Status == "error" || output.Error != "" {
				result.Error = &ResultError{
					Type:    "plugin_error",
					Message: output.Error,
				}
				if result.Summary == "" {
					result.Summary = output.Error
				}
			} else if result.Summary == "" {
				result.Summary = "Plugin executed successfully"
			}
		} else {
			// Old-format output whose "summary" is an object (not a string) fails the
			// strict ExecOutput unmarshal above. Extract fields leniently from a generic map.
			var raw map[string]interface{}
			if err := json.Unmarshal(stdoutBuf.Bytes(), &raw); err == nil {
				isJSON = true
				result = parseLegacyObjectResult(raw)
			} else {
				// Not JSON output, just log it
				fmt.Printf("   📝 Output: %s\n", stdoutBuf.String())
			}
		}
	}

	if !isJSON {
		// Generate structured result from command status
		if cmdErr != nil {
			errStr := stderrBuf.String()
			if errStr == "" {
				errStr = stdoutBuf.String()
			}

			// Try to interpret the failure
			analysis := interpreter.Analyze(errStr)

			summary := "Command execution failed"
			failedStep := p.name
			var hints []string

			if analysis != nil {
				summary = analysis.Summary
				if analysis.Cause != "" {
					summary += ": " + analysis.Cause
				}
				hints = analysis.Hints
			}

			result = NewFailureResult(summary, failedStep, cmdErr)
			result.Error.Raw = errStr
			if len(hints) > 0 {
				result.Hints = hints
			}
		} else {
			result = NewSuccessResult("Plugin executed successfully")
		}
	}

	// Calculate total duration if not provided
	if result.Metrics == nil {
		result.Metrics = &ResultMetrics{}
	}
	// TODO: calculate duration if missing, maybe we can pass start time from worker, or track it here.
	// We'll let worker track it.

	// Write result.json if artifacts writer is available
	if ctx.Artifacts != nil && result != nil {
		_ = ctx.Artifacts.WriteJSON("result.json", result)
	}

	if cmdErr != nil {
		return fmt.Errorf("command failed: %w\nstderr: %s", cmdErr, stderrBuf.String())
	}
	if isFailureResult(result) {
		errMsg := "plugin execution failed"
		if result.Error != nil && result.Error.Message != "" {
			errMsg = result.Error.Message
		}
		return fmt.Errorf("plugin error: %s", errMsg)
	}

	if isJSON {
		if len(output.Artifacts) > 0 {
			fmt.Printf("   📦 Created %d artifacts\n", len(output.Artifacts))
		}
		if output.Model != "" {
			fmt.Printf("   🤖 Model: %s, Tokens: %d\n", output.Model, output.Tokens)
		}
	}

	return nil
}

// streamOutput reads from a pipe line-by-line, writes to buffer, and calls callback
func streamOutput(pipe io.ReadCloser, buf *bytes.Buffer, callback LogLineCallback, isError bool) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteString("\n")

		// Call streaming callback if provided
		if callback != nil {
			callback(line, isError)
		}

		// Also print to console
		if isError {
			fmt.Printf("   ⚠️  %s\n", line)
		}
	}
}

// isFailureResult reports whether a parsed plugin result means failure.
// Accepts "error" and "failure" (legacy ExecOutput vocabulary) as well as
// "failed" — the V1 ActionResult mapping used by plugin run.py wrappers
// (to_action_result). Missing "failed" here caused FALSE PASSES in
// production: pytest collection errors were reported as done jobs
// (ASSURANCE Phase C gate incident, 2026-08-22).
func isFailureResult(result *StructuredResult) bool {
	if result == nil {
		return false
	}
	switch result.Status {
	case "error", "failure", "failed":
		return true
	default:
		return false
	}
}

// denyDecisionValues are gate-level decision values that must force a plugin
// failure regardless of the reported status (fail-closed).
var denyDecisionValues = map[string]bool{
	"deny":     true,
	"rejected": true,
	"expired":  true,
	"revoked":  true,
	"error":    true,
}

// isDenyDecision reports whether a decision value must block the plugin.
func isDenyDecision(decision string) bool {
	if decision == "" {
		return false
	}
	return denyDecisionValues[strings.ToLower(strings.TrimSpace(decision))]
}

// decisionErrorType returns the ResultError.Type for a gate-level decision.
func decisionErrorType(decision string) string {
	if strings.ToLower(strings.TrimSpace(decision)) == "error" {
		return "plugin_error"
	}
	return "gate_denied"
}

// parseLegacyObjectResult extracts a StructuredResult from an old-format JSON
// object whose "summary" is an object (not a string), which would otherwise
// fail the strict ExecOutput unmarshal. It tolerates:
//   - status: string
//   - summary: object with a "message" string (or plain string)
//   - artifacts: []string or []{path: string}
//   - hints: []string
//   - decision: string (fail-closed mapping)
func parseLegacyObjectResult(raw map[string]interface{}) *StructuredResult {
	result := &StructuredResult{
		Version:   ResultProtocolVersion,
		Status:    "success",
		Timestamp: time.Now(),
	}

	if s, ok := raw["status"].(string); ok && s != "" {
		result.Status = s
	}

	result.Summary = extractSummary(raw["summary"])

	if hints, ok := raw["hints"].([]interface{}); ok {
		for _, h := range hints {
			if hs, ok := h.(string); ok {
				result.Hints = append(result.Hints, hs)
			}
		}
	}

	result.Artifacts = extractArtifacts(raw["artifacts"])

	if decision, ok := raw["decision"].(string); ok {
		if isDenyDecision(decision) {
			result.Status = "failed"
			result.Error = &ResultError{
				Type:    decisionErrorType(decision),
				Message: result.Summary,
			}
			if result.Error.Message == "" {
				result.Error.Message = "plugin decision: " + decision
			}
			if result.Summary == "" {
				result.Summary = result.Error.Message
			}
		}
	}

	if result.Error == nil && (result.Status == "error" || result.Status == "failure" || result.Status == "failed") {
		result.Error = &ResultError{
			Type:    "plugin_error",
			Message: result.Summary,
		}
	}

	return result
}

// extractSummary returns a human-readable summary from a legacy summary field,
// which may be a plain string or an object carrying a "message" string.
func extractSummary(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case map[string]interface{}:
		if msg, ok := s["message"].(string); ok {
			return msg
		}
	}
	return ""
}

// extractArtifacts extracts artifact paths from a legacy artifacts field,
// which may be []string or []{path: string}.
func extractArtifacts(v interface{}) []string {
	var paths []string
	switch arts := v.(type) {
	case []string:
		paths = append(paths, arts...)
	case []interface{}:
		for _, a := range arts {
			switch item := a.(type) {
			case string:
				paths = append(paths, item)
			case map[string]interface{}:
				if p, ok := item["path"].(string); ok {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}
