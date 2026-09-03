// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Model Context Protocol integration

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/interpreter"
	"github.com/JoeGlenn1213/actiond/internal/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ActionDClient interface for communicating with ActionD server
type ActionDClient interface {
	GetPlugins() ([]PluginInfo, error)
	GetActions(limit int) ([]ActionInfo, error)
	GetActionsByEventID(eventID string) ([]ActionInfo, error)
	GetAction(id string) (*ActionDetail, error)
	ReloadPlugins() (*ReloadResult, error)
	GetStatus() (*StatusInfo, error)
	GetLogs(limit int) ([]LogEntry, error)
	CancelAction(id string) error
	RetryAction(id string) error
	SetPluginEnabled(name string, enabled bool) error
	GetProfile() (string, error)
	SetProfile(profile string) error
	CleanupActions(days int, all bool) (*CleanupResult, error)
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp string `json:"timestamp,omitempty"`
	Level     string `json:"level,omitempty"`
	Message   string `json:"message"`
}

// PluginInfo represents plugin information
type PluginInfo struct {
	Name       string   `json:"name"`
	Triggers   []string `json:"triggers"`
	Languages  []string `json:"languages"`
	Filter     string   `json:"filter,omitempty"`
	RepoFilter string   `json:"repoFilter,omitempty"`
	Enabled    bool     `json:"enabled"`
	Type       string   `json:"type"`
	IsCustom   bool     `json:"isCustom"`
	Command    string   `json:"command,omitempty"`
	WorkingDir string   `json:"workingDir,omitempty"`
}

// ActionInfo represents action/job information
type ActionInfo struct {
	ID         string `json:"id"`
	Repo       string `json:"repo"`
	PluginName string `json:"plugin_name"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	DurationMs int64  `json:"duration_ms"`
}

// ActionDetail represents detailed action information
type ActionDetail struct {
	ID            string                 `json:"id"`
	Repo          string                 `json:"repo"`
	PluginName    string                 `json:"plugin_name"`
	Status        string                 `json:"status"`
	Progress      string                 `json:"progress"`
	CreatedAt     string                 `json:"created_at"`
	StartedAt     string                 `json:"started_at"`
	EndedAt       string                 `json:"ended_at"`
	DurationMs    int64                  `json:"duration_ms"`
	Profile       string                 `json:"profile,omitempty"`        // verdict tier provenance
	PluginVersion string                 `json:"plugin_version,omitempty"` // verifier provenance
	Intent        string                 `json:"intent,omitempty"`         // native contract v1
	Commit        map[string]interface{} `json:"commit"`
}

// ReloadResult represents plugin reload result
type ReloadResult struct {
	Status     string   `json:"status"`
	Count      int      `json:"count"`
	PluginList []string `json:"pluginList"`
}

// CleanupResult represents the outcome of an actions cleanup call.
type CleanupResult struct {
	Status        string `json:"status"`
	DeletedJobs   int    `json:"deleted_jobs"`
	DeletedDirs   int    `json:"deleted_dirs"`
	RetentionDays int    `json:"retention_days"`
	Message       string `json:"message"`
}

// StatusInfo represents ActionD server status
type StatusInfo struct {
	Running     bool   `json:"running"`
	Version     string `json:"version"`
	Uptime      string `json:"uptime"`
	PluginCount int    `json:"plugin_count"`
	ActionCount int    `json:"action_count"`
}

// NewServer creates and configures the ActionD MCP server
func NewServer(client ActionDClient, lifecycle LifecycleController, lghClient LGHClient) *server.MCPServer {
	s := server.NewMCPServer(
		"actiond",
		version.Version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, false),
	)

	// Register tools
	registerTools(s, client, lifecycle)
	registerWorkflowTools(s, client, lghClient)
	registerRunReportTool(s, client)
	registerHandoffTool(s, client)

	// Register resources
	registerResources(s, client)

	return s
}

// getArgsMap extracts arguments as a map from request
func getArgsMap(request mcp.CallToolRequest) map[string]interface{} {
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok {
		return args
	}
	return make(map[string]interface{})
}

// getInt gets an int argument
func getInt(args map[string]interface{}, key string) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return 0
}

// withInteger narrows a WithNumber-declared property to JSON Schema
// "integer" so models emit 3 instead of 3.0. mcp-go v0.43.2 has no
// WithInteger; replace this helper when the library gains one.
func withInteger(name string) mcp.ToolOption {
	return func(t *mcp.Tool) {
		if prop, ok := t.InputSchema.Properties[name].(map[string]any); ok {
			prop["type"] = "integer"
		}
	}
}

// getBool gets a bool argument
func getBool(args map[string]interface{}, key string) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return false
}

// registerTools registers all ActionD tools with the MCP server
func registerTools(s *server.MCPServer, client ActionDClient, lifecycle LifecycleController) {
	// actiond_status - Get ActionD server status
	s.AddTool(
		mcp.NewTool("actiond_status",
			mcp.WithDescription("Get ActionD server status including running state, version, and statistics"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleStatus(client, ctx, request)
		},
	)

	// actiond_plugins_list - List all plugins
	s.AddTool(
		mcp.NewTool("actiond_plugins_list",
			mcp.WithDescription("List all CI/CD plugins available in ActionD. Shows plugin name, triggers (git.push/git.tag), supported languages, and enabled status."),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handlePluginsList(client, ctx, request)
		},
	)

	// actiond_actions_list - List recent actions/jobs
	s.AddTool(
		mcp.NewTool("actiond_actions_list",
			mcp.WithDescription("List recent CI/CD actions/jobs executed by ActionD. Shows job ID, repository, plugin used, status (done/failed/running), and duration."),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of actions to return (default: 20)"),
			),
			withInteger("limit"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleActionsList(client, ctx, request)
		},
	)

	// actiond_action_get - Get action details
	s.AddTool(
		mcp.NewTool("actiond_action_get",
			mcp.WithDescription("Get detailed information about a specific CI/CD action/job, including commit info, progress, and execution times."),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("The action/job ID to retrieve"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleActionGet(client, ctx, request)
		},
	)

	// actiond_cleanup - Delete old terminal actions to reclaim disk space
	s.AddTool(
		mcp.NewTool("actiond_cleanup",
			mcp.WithDescription("Delete old completed/failed/cancelled actions and their artifact directories to reclaim disk space. Only terminal jobs are ever deleted; pending/running jobs are preserved. Default retention is 7 days; use days=0 or all=true to delete every terminal job."),
			mcp.WithNumber("days",
				mcp.Description("Retention window in days (default 7; 0 = all terminal jobs)"),
			),
			withInteger("days"),
			mcp.WithBoolean("all",
				mcp.Description("Delete all terminal jobs regardless of age"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleCleanup(client, ctx, request)
		},
	)

	// actiond_plugins_reload - Hot reload plugins
	s.AddTool(
		mcp.NewTool("actiond_plugins_reload",
			mcp.WithDescription("Hot reload plugins without restarting ActionD. Scans plugin directories for new manifest.json files and updates the plugin registry."),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handlePluginsReload(client, ctx, request)
		},
	)

	// actiond_log - View server logs
	s.AddTool(
		mcp.NewTool("actiond_log",
			mcp.WithDescription("View ActionD server runtime logs. Shows plugin execution results, errors, and system events."),
			mcp.WithNumber("limit",
				mcp.Description("Number of log entries to return (default: 20)"),
			),
			withInteger("limit"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleLog(client, ctx, request)
		},
	)

	// actiond_server_start - Start ActionD server
	s.AddTool(
		mcp.NewTool("actiond_server_start",
			mcp.WithDescription("Start ActionD server in daemon mode. Requires ACTIOND_MCP_ALLOW_LIFECYCLE=1 in MCP server environment."),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleServerStart(client, lifecycle, ctx, request)
		},
	)

	// actiond_server_stop - Stop ActionD server
	s.AddTool(
		mcp.NewTool("actiond_server_stop",
			mcp.WithDescription("Stop ActionD server. Requires ACTIOND_MCP_ALLOW_LIFECYCLE=1. Refuses when jobs are pending/running unless force=true."),
			mcp.WithBoolean("force",
				mcp.Description("Force stop even when jobs are pending/running"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleServerStop(client, lifecycle, ctx, request)
		},
	)

	// actiond_server_restart - Restart ActionD server
	s.AddTool(
		mcp.NewTool("actiond_server_restart",
			mcp.WithDescription("Restart ActionD server. Requires ACTIOND_MCP_ALLOW_LIFECYCLE=1. Refuses when jobs are pending/running unless force=true."),
			mcp.WithBoolean("force",
				mcp.Description("Force restart even when jobs are pending/running"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleServerRestart(client, lifecycle, ctx, request)
		},
	)

	// actiond_job_cancel - Cancel a running job
	s.AddTool(
		mcp.NewTool("actiond_job_cancel",
			mcp.WithDescription("Cancel a running CI/CD job. Only pending or running jobs can be cancelled."),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("Job ID to cancel"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleJobCancel(client, ctx, request)
		},
	)

	// actiond_job_retry - Retry a failed job
	s.AddTool(
		mcp.NewTool("actiond_job_retry",
			mcp.WithDescription("Retry a failed or cancelled CI/CD job. The job will be queued again for execution."),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("Job ID to retry"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleJobRetry(client, ctx, request)
		},
	)

	// actiond_plugin_enable - Enable a plugin
	s.AddTool(
		mcp.NewTool("actiond_plugin_enable",
			mcp.WithDescription(`Enable a CI/CD plugin for the current project.

When enabled, the plugin will be triggered by its configured events (git.push/git.tag).
Use this to selectively enable plugins based on project needs.`),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Plugin name to enable (e.g., 'go-lint', 'security_scan')"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handlePluginToggle(client, ctx, request, true)
		},
	)

	// actiond_plugin_disable - Disable a plugin
	s.AddTool(
		mcp.NewTool("actiond_plugin_disable",
			mcp.WithDescription(`Disable a CI/CD plugin for the current project.

When disabled, the plugin will NOT be triggered even if its event conditions are met.
Use this to skip unnecessary checks or speed up CI for specific scenarios.`),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Plugin name to disable (e.g., 'benchmark', 'coverage_report')"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handlePluginToggle(client, ctx, request, false)
		},
	)

	// actiond_plugins_recommend - Get plugin recommendations based on project
	s.AddTool(
		mcp.NewTool("actiond_plugins_recommend",
			mcp.WithDescription(`Intelligent plugin recommendations based on project characteristics.

Analyzes the project and suggests which plugins should be enabled/disabled:
- Language detection (Go, Python, Java, TypeScript, etc.)
- Project type (library, service, frontend, etc.)
- Recommended workflow (CI only, full CI/CD, etc.)

Returns a structured recommendation with reasoning.`),
			mcp.WithString("path",
				mcp.Description("Project path to analyze (defaults to current directory)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handlePluginsRecommend(client, ctx, request)
		},
	)

	// actiond_profile_get - Get current execution profile
	s.AddTool(
		mcp.NewTool("actiond_profile_get",
			mcp.WithDescription(`Get the current CI/CD execution profile.

Returns the active profile name and description:
- "fast": Minimal CI - core lint and test only (2-3 jobs per push)
- "full": Complete CI - adds security scan, coverage, formatting (6-10 jobs)
- "release": Full CI/CD - adds build, deploy, release notes (10-15 jobs)`),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleProfileGet(client, ctx, request)
		},
	)

	// actiond_profile_set - Set execution profile
	s.AddTool(
		mcp.NewTool("actiond_profile_set",
			mcp.WithDescription(`Set the CI/CD execution profile to control which plugins are triggered.

Profiles control the scope of CI on each push:
- "fast": Minimal CI - core lint and test only (recommended for development)
- "full": Complete CI - adds security scan, coverage, formatting
- "release": Full CI/CD - adds build, deploy, release notes

Use "fast" during active development for quick feedback.
Switch to "full" before merging or releasing.`),
			mcp.WithString("profile",
				mcp.Required(),
				mcp.Description(`Execution profile: "fast", "full", or "release"`),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleProfileSet(client, ctx, request)
		},
	)

	// actiond_diagnose - Diagnose failed CI jobs and provide fix suggestions
	s.AddTool(
		mcp.NewTool("actiond_diagnose",
			mcp.WithDescription(`Diagnose failed CI/CD jobs and provide actionable fix suggestions.

Analyzes recent failed jobs and their logs to:
- Identify the root cause category (build, test, lint, dependency, permission, etc.)
- Extract the specific error type and summary
- Provide concrete hints for fixing the issue
- Suggest relevant files that may need attention

This is the AI's primary tool for debugging CI failures - use it whenever a job fails
and you need to help the user understand what went wrong and how to fix it.`),
			mcp.WithString("job_id",
				mcp.Description("Specific job ID to diagnose (optional - if not provided, analyzes recent failures)"),
			),
			mcp.WithNumber("limit",
				mcp.Description("最多分析的失败任务数量（默认 5）"),
			),
			withInteger("limit"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleDiagnose(client, ctx, request)
		},
	)
}

// registerResources registers all ActionD resources with the MCP server
func registerResources(s *server.MCPServer, client ActionDClient) {
	// actiond://status - Server status
	s.AddResource(
		mcp.NewResource("actiond://status",
			"ActionD Status",
			mcp.WithResourceDescription("Current ActionD server status and statistics"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return handleResourceStatus(client, ctx, request)
		},
	)

	// actiond://plugins - Plugin list
	s.AddResource(
		mcp.NewResource("actiond://plugins",
			"ActionD Plugins",
			mcp.WithResourceDescription("List of all CI/CD plugins available in ActionD"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return handleResourcePlugins(client, ctx, request)
		},
	)

	// actiond://actions - Recent actions
	s.AddResource(
		mcp.NewResource("actiond://actions",
			"Recent Actions",
			mcp.WithResourceDescription("Recent CI/CD actions executed by ActionD"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return handleResourceActions(client, ctx, request)
		},
	)
}

// Tool Handlers

func handleStatus(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status, err := client.GetStatus()
	if err != nil {
		return mcp.NewToolResultError("Failed to get status: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(status, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handlePluginsList(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	plugins, err := client.GetPlugins()
	if err != nil {
		return mcp.NewToolResultError("Failed to list plugins: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(plugins, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleActionsList(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	limit := getInt(args, "limit")
	if limit == 0 {
		limit = 20
	}

	actions, err := client.GetActions(limit)
	if err != nil {
		return mcp.NewToolResultError("Failed to list actions: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(actions, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleActionGet(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	id, ok := args["id"].(string)
	if !ok {
		return mcp.NewToolResultError("Missing required parameter: id"), nil
	}

	action, err := client.GetAction(id)
	if err != nil {
		return mcp.NewToolResultError("Failed to get action: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(action, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// handleCleanup deletes old terminal actions via the server cleanup endpoint.
func handleCleanup(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	days := 7
	if v, ok := args["days"]; ok {
		switch n := v.(type) {
		case float64:
			days = int(n)
		case int:
			days = n
		}
	}
	all := false
	if v, ok := args["all"].(bool); ok {
		all = v
	}

	result, err := client.CleanupActions(days, all)
	if err != nil {
		return mcp.NewToolResultError("Failed to cleanup actions: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handlePluginsReload(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := client.ReloadPlugins()
	if err != nil {
		return mcp.NewToolResultError("Failed to reload plugins: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleLog(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	limit := getInt(args, "limit")
	if limit == 0 {
		limit = 20
	}

	logs, err := client.GetLogs(limit)
	if err != nil {
		return mcp.NewToolResultError("Failed to get logs: " + err.Error()), nil
	}

	data, _ := json.MarshalIndent(logs, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleJobCancel(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	id := getString(args, "id")
	if id == "" {
		return mcp.NewToolResultError("Missing required parameter: id"), nil
	}

	// Get job info first
	job, err := client.GetAction(id)
	if err != nil {
		return mcp.NewToolResultError("Job not found: " + err.Error()), nil
	}

	// Check if job can be cancelled
	if job.Status == "done" || job.Status == "failed" {
		return mcp.NewToolResultError("Cannot cancel job with status: " + job.Status), nil
	}

	if err := client.CancelAction(id); err != nil {
		return mcp.NewToolResultError("Failed to cancel job: " + err.Error()), nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"message": "Job cancelled",
		"job_id":  id,
		"plugin":  job.PluginName,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleJobRetry(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	id := getString(args, "id")
	if id == "" {
		return mcp.NewToolResultError("Missing required parameter: id"), nil
	}

	// Get job info first
	job, err := client.GetAction(id)
	if err != nil {
		return mcp.NewToolResultError("Job not found: " + err.Error()), nil
	}

	if err := client.RetryAction(id); err != nil {
		return mcp.NewToolResultError("Failed to retry job: " + err.Error()), nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"message": "Job queued for retry",
		"job_id":  id,
		"plugin":  job.PluginName,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handlePluginToggle(client ActionDClient, ctx context.Context, request mcp.CallToolRequest, enable bool) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	name := getString(args, "name")
	if name == "" {
		return mcp.NewToolResultError("Missing required parameter: name"), nil
	}

	if err := client.SetPluginEnabled(name, enable); err != nil {
		return mcp.NewToolResultError("Failed to toggle plugin: " + err.Error()), nil
	}

	action := "disabled"
	if enable {
		action = "enabled"
	}
	result := map[string]interface{}{
		"status":  "success",
		"name":    name,
		"enabled": enable,
		"message": fmt.Sprintf("Plugin %s %s", name, action),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleProfileGet(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	profile, err := client.GetProfile()
	if err != nil {
		return mcp.NewToolResultError("Failed to get profile: " + err.Error()), nil
	}

	descriptions := map[string]string{
		"fast":    "Minimal CI - core lint and test only (2-3 jobs per push)",
		"full":    "Complete CI - adds security scan, coverage, formatting (6-10 jobs)",
		"release": "Full CI/CD - adds build, deploy, release notes (10-15 jobs)",
	}

	result := map[string]interface{}{
		"profile":     profile,
		"description": descriptions[profile],
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleProfileSet(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	profile := getString(args, "profile")
	if profile == "" {
		return mcp.NewToolResultError("Missing required parameter: profile"), nil
	}

	// Validate profile
	validProfiles := map[string]bool{"fast": true, "full": true, "release": true}
	if !validProfiles[profile] {
		return mcp.NewToolResultError("Invalid profile. Must be one of: fast, full, release"), nil
	}

	if err := client.SetProfile(profile); err != nil {
		return mcp.NewToolResultError("Failed to set profile: " + err.Error()), nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"profile": profile,
		"message": fmt.Sprintf("Execution profile set to '%s'", profile),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handlePluginsRecommend(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)
	path := getString(args, "path")
	if path == "" {
		path = "."
	}

	// Get all plugins
	plugins, err := client.GetPlugins()
	if err != nil {
		return mcp.NewToolResultError("Failed to get plugins: " + err.Error()), nil
	}

	// Detect project language and characteristics
	projectInfo := detectProjectLanguage(path)

	// Build recommendations with detailed reasoning
	type Recommendation struct {
		Name       string  `json:"name"`
		Category   string  `json:"category"`
		Reason     string  `json:"reason"`
		Priority   int     `json:"priority"`
		Enabled    bool    `json:"enabled"`
		ShouldUse  bool    `json:"should_use"`
		Confidence float64 `json:"confidence"`
	}

	var recommendations []Recommendation

	// Plugin mapping: language -> plugins
	langPlugins := map[string][]struct {
		name       string
		cat        string
		priority   int
		reason     string
		confidence float64
	}{
		"go": {
			{"go-lint", "quality", 1, "Go projects should always have linting", 0.95},
			{"go-test-fast", "test", 2, "Fast tests catch bugs early", 0.9},
			{"go-build", "build", 3, "Verify builds succeed on every push", 0.85},
		},
		"python": {
			{"python-ruff", "quality", 1, "Ruff is 10-100x faster than other linters", 0.95},
			{"python-pytest", "test", 2, "pytest is the standard for Python testing", 0.9},
			{"python-build", "build", 3, "Verify package builds correctly", 0.8},
		},
		"java": {
			{"java-checkstyle", "quality", 1, "Enforce coding standards for Java", 0.9},
			{"java-quicktest", "test", 2, "Run affected tests only for speed", 0.9},
			{"java-build", "build", 3, "Verify compilation and packaging", 0.85},
		},
		"typescript": {
			{"web-lint", "quality", 1, "ESLint with TypeScript support", 0.95},
			{"web-test", "test", 2, "Jest or Vitest for unit tests", 0.9},
			{"web-build", "build", 3, "Verify TypeScript compiles", 0.9},
		},
		"javascript": {
			{"web-lint", "quality", 1, "ESLint for code quality", 0.9},
			{"web-test", "test", 2, "Unit tests with Jest/Mocha", 0.85},
			{"web-build", "build", 3, "Verify bundling works", 0.8},
		},
	}

	// Framework-specific plugins
	frameworkPlugins := map[string][]struct {
		name       string
		reason     string
		priority   int
		confidence float64
	}{
		"nextjs": {
			{"web-lint", "Next.js benefits from strict linting", 1, 0.9},
			{"web-test", "Next.js pages need test coverage", 2, 0.85},
		},
		"react": {
			{"web-lint", "React projects need JSX linting", 1, 0.9},
			{"web-test", "Component tests with React Testing Library", 2, 0.85},
		},
		"spring": {
			{"java-checkstyle", "Spring Boot benefits from style checks", 1, 0.85},
			{"java-quicktest", "Integration tests for Spring context", 2, 0.9},
		},
	}

	// Universal plugins (always recommended)
	universalPlugins := []struct {
		name       string
		cat        string
		priority   int
		reason     string
		confidence float64
	}{
		{"affected_scope", "analysis", 1, "Analyzes which files changed to determine impact", 0.95},
		{"env-check", "setup", 2, "Validates that required tools are installed", 0.9},
		{"security_scan", "security", 3, "Scans for common vulnerabilities (OWASP Top 10)", 0.85},
		{"policy_gate", "compliance", 4, "Enforces team policies (e.g., no TODO without issue)", 0.8},
		{"artifact_manifest", "artifact", 5, "Creates build manifest for traceability", 0.75},
	}

	// Add universal plugins
	for _, p := range universalPlugins {
		enabled := false
		shouldUse := true // Universal plugins are usually recommended
		for _, plugin := range plugins {
			if plugin.Name == p.name {
				enabled = plugin.Enabled
				break
			}
		}
		recommendations = append(recommendations, Recommendation{
			Name:       p.name,
			Category:   p.cat,
			Reason:     p.reason,
			Priority:   p.priority,
			Enabled:    enabled,
			ShouldUse:  shouldUse,
			Confidence: p.confidence,
		})
	}

	// Add language-specific plugins
	for _, lang := range projectInfo.Languages {
		if recs, ok := langPlugins[lang]; ok {
			for _, p := range recs {
				enabled := false
				for _, plugin := range plugins {
					if plugin.Name == p.name {
						enabled = plugin.Enabled
						break
					}
				}
				recommendations = append(recommendations, Recommendation{
					Name:       p.name,
					Category:   p.cat,
					Reason:     p.reason,
					Priority:   p.priority,
					Enabled:    enabled,
					ShouldUse:  true,
					Confidence: p.confidence,
				})
			}
		}
	}

	// Add framework-specific plugins
	for _, fw := range projectInfo.Frameworks {
		if recs, ok := frameworkPlugins[fw]; ok {
			for _, p := range recs {
				enabled := false
				// Check if already added
				alreadyAdded := false
				for _, r := range recommendations {
					if r.Name == p.name {
						alreadyAdded = true
						// Update confidence if higher
						if p.confidence > r.Confidence {
							r.Confidence = p.confidence
							r.Reason = p.reason
						}
						break
					}
				}
				if alreadyAdded {
					continue
				}
				for _, plugin := range plugins {
					if plugin.Name == p.name {
						enabled = plugin.Enabled
						break
					}
				}
				recommendations = append(recommendations, Recommendation{
					Name:       p.name,
					Category:   "quality",
					Reason:     p.reason,
					Priority:   p.priority,
					Enabled:    enabled,
					ShouldUse:  true,
					Confidence: p.confidence,
				})
			}
		}
	}

	// Sort by priority
	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].Priority < recommendations[j].Priority
	})

	// Build summary
	enabledCount := 0
	recommendedCount := 0
	suggestEnable := []string{}
	suggestDisable := []string{}

	for _, r := range recommendations {
		if r.Enabled {
			enabledCount++
		}
		if r.ShouldUse && !r.Enabled {
			suggestEnable = append(suggestEnable, r.Name)
			recommendedCount++
		}
		if !r.ShouldUse && r.Enabled {
			suggestDisable = append(suggestDisable, r.Name)
		}
	}

	// Build output
	output := map[string]interface{}{
		"project_analysis": map[string]interface{}{
			"detected_languages":   projectInfo.Languages,
			"detected_frameworks":  projectInfo.Frameworks,
			"project_type":         projectInfo.ProjectType,
			"features":             projectInfo.Features,
			"config_files_scanned": len(projectInfo.ConfigFiles),
		},
		"summary": map[string]interface{}{
			"total_plugins":       len(plugins),
			"recommended_plugins": len(recommendations),
			"currently_enabled":   enabledCount,
			"suggestions": map[string]interface{}{
				"enable":  suggestEnable,
				"disable": suggestDisable,
			},
		},
		"recommendations":     recommendations,
		"workflow_suggestion": buildWorkflowSuggestion(projectInfo),
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// buildWorkflowSuggestion suggests a CI/CD workflow based on project type
func buildWorkflowSuggestion(info *ProjectInfo) string {
	var suggestions []string

	projectType := info.ProjectType

	switch projectType {
	case "frontend":
		suggestions = append(suggestions,
			"Push-triggered: lint → test → build",
			"Consider adding: web-build for production bundle verification",
		)
	case "backend":
		suggestions = append(suggestions,
			"Push-triggered: lint → test → (build for tagged releases)",
			"Consider adding: security_scan for vulnerability checks",
		)
	case "fullstack":
		suggestions = append(suggestions,
			"Push-triggered: affected_scope → lint → test → build",
			"Separate frontend and backend workflows for clarity",
		)
	case "monorepo":
		suggestions = append(suggestions,
			"Push-triggered: affected_scope to determine affected packages",
			"Run only affected packages' tests (already supported by java-quicktest, go-test-fast)",
		)
	default:
		suggestions = append(suggestions,
			"Push-triggered: env-check → affected_scope → basic quality checks",
		)
	}

	// Add feature-based suggestions
	if info.Features["has_docker"] {
		suggestions = append(suggestions, "Consider adding: container-package for image building")
	}
	if info.Features["has_ci"] {
		suggestions = append(suggestions, "External CI detected - ensure ActionD complements, not duplicates")
	}
	if !info.Features["has_tests"] {
		suggestions = append(suggestions, "WARNING: No test files detected - consider adding tests")
	}

	return strings.Join(suggestions, "\n")
}

// ProjectInfo contains analyzed project characteristics
type ProjectInfo struct {
	Languages   []string        `json:"languages"`
	Frameworks  []string        `json:"frameworks"`
	ProjectType string          `json:"project_type"`
	Features    map[string]bool `json:"features"`
	ConfigFiles []string        `json:"config_files"`
}

// detectProjectLanguage analyzes project to detect languages and frameworks
func detectProjectLanguage(path string) *ProjectInfo {
	info := &ProjectInfo{
		Languages:   []string{},
		Frameworks:  []string{},
		Features:    make(map[string]bool),
		ConfigFiles: []string{},
	}

	// Language detection patterns
	langChecks := map[string][]string{
		"go":         {".go", "go.mod", "go.sum"},
		"python":     {"pyproject.toml", "setup.py", "requirements.txt", "Pipfile", ".py"},
		"java":       {"pom.xml", "build.gradle", ".java"},
		"typescript": {"package.json", "tsconfig.json", ".ts", ".tsx"},
		"javascript": {"package.json", ".js", ".jsx"},
		"rust":       {"Cargo.toml", ".rs"},
		"cpp":        {".cpp", ".hpp", ".h", "CMakeLists.txt"},
		"c":          {".c", ".h"},
		"ruby":       {"Gemfile", ".rb"},
		"php":        {".php"},
		"swift":      {".swift", "Package.swift"},
		"kotlin":     {".kt", "build.gradle.kts"},
	}

	// Framework detection
	frameworkChecks := map[string][]string{
		"react":   {"package.json:react"},
		"nextjs":  {"package.json:next"},
		"vue":     {"package.json:vue"},
		"nuxt":    {"package.json:nuxt"},
		"angular": {"package.json:@angular"},
		"svelte":  {"package.json:svelte"},
		"fastapi": {"pyproject.toml:fastapi", "requirements.txt:fastapi"},
		"django":  {"requirements.txt:django"},
		"flask":   {"requirements.txt:flask"},
		"gin":     {".go:github.com/gin-gonic"},
		"fiber":   {".go:github.com/gofiber"},
		"echo":    {".go:github.com/labstack/echo"},
		"chi":     {".go:github.com/go-chi/chi"},
		"spring":  {"pom.xml:spring-boot", "build.gradle:spring-boot"},
		"vite":    {"package.json:vite"},
		"webpack": {"package.json:webpack"},
	}

	// Walk directory (max depth 3 for performance)
	_ = filepath.Walk(path, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			rel, _ := filepath.Rel(path, walkPath)
			if strings.Count(rel, string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(path, walkPath)
		info.ConfigFiles = append(info.ConfigFiles, rel)

		name := fi.Name()

		// Language detection
		for lang, patterns := range langChecks {
			for _, pattern := range patterns {
				if pattern[0] == '.' {
					if strings.HasSuffix(name, pattern) {
						if !contains(info.Languages, lang) {
							info.Languages = append(info.Languages, lang)
						}
					}
				} else if name == pattern {
					if !contains(info.Languages, lang) {
						info.Languages = append(info.Languages, lang)
					}
				}
			}
		}

		// Framework detection from file content
		for fw, patterns := range frameworkChecks {
			for _, pattern := range patterns {
				if strings.Contains(pattern, ":") {
					file, match := split1(pattern, ":")
					if name == file {
						content, _ := os.ReadFile(walkPath)
						if strings.Contains(string(content), match) && !contains(info.Frameworks, fw) {
							info.Frameworks = append(info.Frameworks, fw)
						}
					}
				}
			}
		}

		// Feature detection
		featurePatterns := map[string][]string{
			"has_tests":    {"test", "tests", "_test.go", ".test.ts", ".spec.ts", "pytest.ini", "test_*.py"},
			"has_docker":   {"Dockerfile", "docker-compose.yml", ".dockerignore"},
			"has_makefile": {"Makefile", "makefile"},
			"has_ci":       {".github/workflows", ".gitlab-ci.yml", "Jenkinsfile"},
			"has_eslint":   {".eslintrc", ".eslintrc.js", ".eslintrc.json"},
			"has_prettier": {".prettierrc", ".prettierrc.js", ".prettierrc.json"},
			"has_tailwind": {"tailwind.config", "postcss.config.js"},
			"is_monorepo":  {"pnpm-workspace.yaml", "lerna.json", "nx.json"},
		}

		for feature, patterns := range featurePatterns {
			for _, pattern := range patterns {
				if strings.Contains(name, pattern) {
					info.Features[feature] = true
				}
			}
		}

		return nil
	})

	// Detect project type based on structure
	info.ProjectType = detectProjectType(path, info)

	return info
}

// detectProjectType determines the project type
func detectProjectType(path string, info *ProjectInfo) string {
	// Check for frontend frameworks
	for _, fw := range info.Frameworks {
		if contains([]string{"react", "vue", "angular", "svelte", "nextjs", "nuxt"}, fw) {
			return "frontend"
		}
	}

	// Check for backend frameworks
	for _, fw := range info.Frameworks {
		if contains([]string{"fastapi", "django", "flask", "gin", "fiber", "echo", "chi", "spring"}, fw) {
			return "backend"
		}
	}

	// Check config files
	hasWeb := contains(info.Languages, "typescript") || contains(info.Languages, "javascript")
	hasBackend := contains(info.Languages, "go") || contains(info.Languages, "java") || contains(info.Languages, "python")

	if hasWeb && hasBackend {
		return "fullstack"
	}
	if hasWeb {
		return "frontend"
	}
	if hasBackend {
		return "backend"
	}
	if info.Features["is_monorepo"] {
		return "monorepo"
	}

	// Check if it's a library/SDK
	if _, err := os.Stat(filepath.Join(path, "library.json")); err == nil {
		return "library"
	}

	return "unknown"
}

// contains checks if a string slice contains a value
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// split1 splits a string by the first occurrence of sep
func split1(s, sep string) (string, string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return s, ""
}

type Diagnosis struct {
	JobID        string                       `json:"job_id"`
	Plugin       string                       `json:"plugin"`
	Repo         string                       `json:"repo"`
	Status       string                       `json:"status"`
	Duration     int64                        `json:"duration_ms"`
	CreatedAt    string                       `json:"created_at"`
	Analysis     *interpreter.FailureAnalysis `json:"analysis"`
	RelatedFiles []string                     `json:"related_files,omitempty"`
}

type DiagnoseSummary struct {
	JobID         string   `json:"job_id"`
	Category      string   `json:"category,omitempty"`
	ErrorCode     string   `json:"error_code,omitempty"`
	RootCause     string   `json:"root_cause,omitempty"`
	FixSteps      []string `json:"fix_steps,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	Severity      string   `json:"severity,omitempty"`
	Confidence    float64  `json:"confidence"`
	EvidenceLines int      `json:"evidence_lines"`
}

// handleDiagnose analyzes failed CI jobs and provides fix suggestions
func handleDiagnose(client ActionDClient, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgsMap(request)

	// Check if specific job_id is provided
	jobID, hasJobID := args["job_id"].(string)

	var diagnoses []Diagnosis

	if hasJobID && jobID != "" {
		// Diagnose specific job
		action, err := client.GetAction(jobID)
		if err != nil {
			return mcp.NewToolResultError("Failed to get job: " + err.Error()), nil
		}

		// Get logs and keep only entries referencing this job, so the
		// analysis is not polluted by unrelated jobs in the shared log.
		logs, _ := client.GetLogs(100)

		diagnosis := Diagnosis{
			JobID:     action.ID,
			Plugin:    action.PluginName,
			Repo:      action.Repo,
			Status:    action.Status,
			Duration:  action.DurationMs,
			CreatedAt: action.CreatedAt,
		}

		// Extract error output from this job's logs
		var errorOutput strings.Builder
		for _, log := range logs {
			if !strings.Contains(log.Message, action.ID) {
				continue
			}
			if log.Level == "error" || log.Level == "plugin" {
				errorOutput.WriteString(log.Message)
				errorOutput.WriteString("\n")
			}
		}

		// Analyze the error
		diagnosis.Analysis = interpreter.Analyze(errorOutput.String())

		// Extract related files from error output
		diagnosis.RelatedFiles = extractRelatedFiles(errorOutput.String())

		diagnoses = append(diagnoses, diagnosis)
	} else {
		// Get recent failed jobs
		limit := getInt(args, "limit")
		if limit == 0 {
			limit = 5
		}

		actions, err := client.GetActions(50)
		if err != nil {
			return mcp.NewToolResultError("Failed to get actions: " + err.Error()), nil
		}

		// Get logs for error extraction
		logs, _ := client.GetLogs(200)

		failedCount := 0
		for _, action := range actions {
			if strings.ToLower(action.Status) == "failed" && failedCount < limit {
				diagnosis := Diagnosis{
					JobID:     action.ID,
					Plugin:    action.PluginName,
					Repo:      action.Repo,
					Status:    action.Status,
					Duration:  action.DurationMs,
					CreatedAt: action.CreatedAt,
				}

				// Find logs for this job
				var errorOutput strings.Builder
				for _, log := range logs {
					if strings.Contains(log.Message, action.ID) {
						if log.Level == "error" || log.Level == "plugin" {
							errorOutput.WriteString(log.Message)
							errorOutput.WriteString("\n")
						}
					}
				}

				diagnosis.Analysis = interpreter.Analyze(errorOutput.String())
				diagnosis.RelatedFiles = extractRelatedFiles(errorOutput.String())

				diagnoses = append(diagnoses, diagnosis)
				failedCount++
			}
		}
	}

	// Build summary
	summary := buildDiagnosisSummary(diagnoses)

	var diagnoseSummary []DiagnoseSummary
	for _, diagnosis := range diagnoses {
		entry := DiagnoseSummary{JobID: diagnosis.JobID, Evidence: diagnosis.RelatedFiles}
		if diagnosis.Analysis != nil {
			entry.Category = diagnosis.Analysis.Category
			entry.ErrorCode = diagnosis.Analysis.Type
			entry.RootCause = diagnosis.Analysis.Cause
			entry.FixSteps = diagnosis.Analysis.Hints
			entry.Severity = diagnosis.Analysis.Severity
			entry.Confidence = diagnosis.Analysis.Confidence
			entry.EvidenceLines = diagnosis.Analysis.EvidenceLines
		} else {
			// No analyzable error output: report the gap explicitly instead
			// of silently dropping the job, so the AI can still act on it.
			entry.Category = "unknown"
			entry.ErrorCode = "no_error_output"
			entry.RootCause = "No error/plugin log lines found for this job"
			entry.FixSteps = []string{"Inspect the job logs manually via actiond_log"}
		}
		diagnoseSummary = append(diagnoseSummary, entry)
	}

	output := map[string]interface{}{
		"total_analyzed":   len(diagnoses),
		"diagnoses":        diagnoses,
		"summary":          summary,
		"diagnose_summary": diagnoseSummary,
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultStructured(output, string(data)), nil
}

// extractRelatedFiles extracts file paths from error output
func extractRelatedFiles(errorOutput string) []string {
	var files []string
	seen := make(map[string]bool)

	// Common patterns for file references in error output
	patterns := []string{
		`[\w/\\.-]+\.go:\d+`,                           // Go: file.go:123
		`[\w/\\.-]+\.py:\d+`,                           // Python: file.py:123
		`[\w/\\.-]+\.ts:\d+`,                           // TypeScript: file.ts:123
		`[\w/\\.-]+\.tsx:\d+`,                          // TSX: file.tsx:123
		`[\w/\\.-]+\.java:\d+`,                         // Java: file.java:123
		`[\w/\\.-]+\.js:\d+`,                           // JavaScript: file.js:123
		`error in /[\w/\\.-]+`,                         // Error in path
		`File "([\w/\\.-]+)"`,                          // Python file reference
		`/([\w/\\.-]+)/[\w/\\.-]+\.(go|py|ts|js|java)`, // Common source files
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllString(errorOutput, -1)
		for _, match := range matches {
			// Clean up the match
			file := strings.TrimSpace(match)
			file = strings.TrimPrefix(file, "error in ")
			file = strings.TrimPrefix(file, "File ")
			file = strings.Trim(file, `"`)

			if !seen[file] && len(file) > 3 && len(file) < 200 {
				seen[file] = true
				files = append(files, file)
			}
		}
	}

	// Limit to 10 files
	if len(files) > 10 {
		files = files[:10]
	}

	return files
}

// buildDiagnosisSummary creates a summary from multiple diagnoses
func buildDiagnosisSummary(diagnoses []Diagnosis) map[string]interface{} {
	categoryCount := make(map[string]int)
	pluginCount := make(map[string]int)
	severityCount := make(map[string]int)

	for _, d := range diagnoses {
		if d.Analysis != nil {
			categoryCount[d.Analysis.Category]++
			severityCount[d.Analysis.Severity]++

			// Check for critical issues
			if d.Analysis.Severity == "critical" {
				pluginCount[d.Plugin]++
			}
		}
	}

	// Find most common category
	var mostCommonCategory string
	maxCount := 0
	for cat, count := range categoryCount {
		if count > maxCount {
			maxCount = count
			mostCommonCategory = cat
		}
	}

	// Build actionable recommendations
	var recommendations []string
	if mostCommonCategory == "dependency" {
		recommendations = append(recommendations,
			"Run 'go mod tidy' / 'npm install' / 'pip install -r requirements.txt' to fix dependencies",
			"Check if package versions are compatible")
	}
	if mostCommonCategory == "build" {
		recommendations = append(recommendations,
			"Check for syntax errors in the failing file",
			"Run build command locally to see full error output")
	}
	if mostCommonCategory == "test" {
		recommendations = append(recommendations,
			"Run tests locally with verbose output to see specific failures",
			"Check if test data/fixtures are properly set up")
	}
	if mostCommonCategory == "lint" {
		recommendations = append(recommendations,
			"Run linter with auto-fix: 'golangci-lint run --fix' or 'npm run lint:fix'",
			"Review .golangci.yml or eslint config to adjust rules if needed")
	}

	return map[string]interface{}{
		"most_common_category":      mostCommonCategory,
		"category_breakdown":        categoryCount,
		"severity_breakdown":        severityCount,
		"plugins_needing_attention": pluginCount,
		"recommendations":           recommendations,
		"needs_immediate_attention": severityCount["critical"] > 0,
	}
}

func handleServerStart(client ActionDClient, lifecycle LifecycleController, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = client
	_ = request
	if err := validateLifecycleControl(lifecycle); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if lifecycle.IsRunning() {
		return lifecycleResult("start", false, true, "ActionD is already running", "", nil), nil
	}

	out, err := lifecycle.Start(ctx)
	if err != nil {
		return lifecycleResult("start", false, false, "Failed to start ActionD", out, err), nil
	}

	running := waitForRunningState(lifecycle, true, 5*time.Second)
	msg := "ActionD start command completed"
	if running {
		msg = "ActionD started"
	}
	return lifecycleResult("start", true, running, msg, out, nil), nil
}

func handleServerStop(client ActionDClient, lifecycle LifecycleController, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateLifecycleControl(lifecycle); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := getArgsMap(request)
	force := getBool(args, "force")

	if !lifecycle.IsRunning() {
		return lifecycleResult("stop", false, false, "ActionD is already stopped", "", nil), nil
	}

	if !force {
		active, err := getActiveActions(client)
		if err != nil {
			return mcp.NewToolResultError("Cannot safely stop ActionD: failed to inspect active jobs (" + err.Error() + "). Retry with force=true if you want to proceed."), nil
		}
		if len(active) > 0 {
			data, _ := json.MarshalIndent(active, "", "  ")
			return mcp.NewToolResultError("Refusing to stop: there are pending/running jobs. Retry with force=true.\n" + string(data)), nil
		}
	}

	out, err := lifecycle.Stop(ctx)
	if err != nil {
		return lifecycleResult("stop", false, true, "Failed to stop ActionD", out, err), nil
	}

	running := waitForRunningState(lifecycle, false, 5*time.Second)
	msg := "ActionD stop command completed"
	if !running {
		msg = "ActionD stopped"
	}
	return lifecycleResult("stop", true, running, msg, out, nil), nil
}

func handleServerRestart(client ActionDClient, lifecycle LifecycleController, ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateLifecycleControl(lifecycle); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := getArgsMap(request)
	force := getBool(args, "force")

	if lifecycle.IsRunning() && !force {
		active, err := getActiveActions(client)
		if err != nil {
			return mcp.NewToolResultError("Cannot safely restart ActionD: failed to inspect active jobs (" + err.Error() + "). Retry with force=true if you want to proceed."), nil
		}
		if len(active) > 0 {
			data, _ := json.MarshalIndent(active, "", "  ")
			return mcp.NewToolResultError("Refusing to restart: there are pending/running jobs. Retry with force=true.\n" + string(data)), nil
		}
	}

	stopOut := ""
	if lifecycle.IsRunning() {
		out, err := lifecycle.Stop(ctx)
		stopOut = out
		if err != nil {
			return lifecycleResult("restart", false, true, "Failed to stop ActionD during restart", stopOut, err), nil
		}
		waitForRunningState(lifecycle, false, 5*time.Second)
	}

	startOut, err := lifecycle.Start(ctx)
	if err != nil {
		combinedOut := strings.TrimSpace(strings.TrimSpace(stopOut) + "\n" + strings.TrimSpace(startOut))
		return lifecycleResult("restart", false, false, "Failed to start ActionD during restart", combinedOut, err), nil
	}

	running := waitForRunningState(lifecycle, true, 5*time.Second)
	combinedOut := strings.TrimSpace(strings.TrimSpace(stopOut) + "\n" + strings.TrimSpace(startOut))
	msg := "ActionD restart command completed"
	if running {
		msg = "ActionD restarted"
	}
	return lifecycleResult("restart", true, running, msg, combinedOut, nil), nil
}

func lifecycleResult(action string, changed bool, running bool, message string, output string, err error) *mcp.CallToolResult {
	result := map[string]interface{}{
		"action":  action,
		"changed": changed,
		"running": running,
		"message": message,
	}
	if strings.TrimSpace(output) != "" {
		result["output"] = output
	}
	if err != nil {
		result["error"] = err.Error()
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data))
}

func validateLifecycleControl(lifecycle LifecycleController) error {
	if lifecycle == nil {
		return mcpLifecycleError("lifecycle controller is not configured for this ActionD MCP server")
	}
	if !isLifecycleAllowed() {
		return mcpLifecycleError("lifecycle control is disabled. Set ACTIOND_MCP_ALLOW_LIFECYCLE=1 before starting 'actiond mcp'")
	}
	return nil
}

func mcpLifecycleError(msg string) error {
	return &lifecycleError{message: msg}
}

type lifecycleError struct {
	message string
}

func (e *lifecycleError) Error() string {
	return e.message
}

func isLifecycleAllowed() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ACTIOND_MCP_ALLOW_LIFECYCLE")))
	return v == "1" || v == "true" || v == "yes"
}

func getActiveActions(client ActionDClient) ([]ActionInfo, error) {
	actions, err := client.GetActions(200)
	if err != nil {
		return nil, err
	}
	active := make([]ActionInfo, 0)
	for _, a := range actions {
		status := strings.TrimSpace(strings.ToLower(a.Status))
		if status == "pending" || status == "running" {
			active = append(active, a)
		}
	}
	return active, nil
}

func waitForRunningState(lifecycle LifecycleController, expected bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if lifecycle.IsRunning() == expected {
			return expected
		}
		time.Sleep(150 * time.Millisecond)
	}
	return lifecycle.IsRunning()
}

// Resource Handlers

func handleResourceStatus(client ActionDClient, ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	status, err := client.GetStatus()
	if err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(status, "", "  ")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "actiond://status",
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

func handleResourcePlugins(client ActionDClient, ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	plugins, err := client.GetPlugins()
	if err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(plugins, "", "  ")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "actiond://plugins",
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

func handleResourceActions(client ActionDClient, ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	actions, err := client.GetActions(50)
	if err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(actions, "", "  ")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "actiond://actions",
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}
