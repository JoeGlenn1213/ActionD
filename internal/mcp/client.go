// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - HTTP Client for ActionD API

package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/version"
)

// HTTPClient implements ActionDClient using HTTP
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// bearerTransport injects the ACTIOND_TOKEN shared secret into every
// request, mirroring the server's optional auth (see internal/server
// secureWrap). No-op when the env var is unset.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" {
		clone := req.Clone(req.Context())
		clone.Header.Set("Authorization", "Bearer "+t.token)
		req = clone
	}
	return t.base.RoundTrip(req)
}

// NewHTTPClient creates a new HTTP client for ActionD API
func NewHTTPClient(baseURL string) *HTTPClient {
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	return &HTTPClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &bearerTransport{
				token: os.Getenv("ACTIOND_TOKEN"),
				base:  http.DefaultTransport,
			},
		},
	}
}

// GetPlugins fetches the list of plugins from ActionD
func (c *HTTPClient) GetPlugins() ([]PluginInfo, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/plugins")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch plugins: %w", err)
	}
	defer closeBody(resp.Body, "get plugins")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var plugins []PluginInfo
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return plugins, nil
}

// GetActions fetches the list of actions from ActionD
func (c *HTTPClient) GetActions(limit int) ([]ActionInfo, error) {
	url := fmt.Sprintf("%s/api/actions?limit=%d", c.baseURL, limit)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch actions: %w", err)
	}
	defer closeBody(resp.Body, "get actions")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var jobs []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var actions []ActionInfo
	for _, job := range jobs {
		action := ActionInfo{
			ID:         getStringVal(job, "id"),
			Repo:       getStringVal(job, "repo"),
			PluginName: getStringVal(job, "plugin_name"),
			Status:     getStringVal(job, "status"),
			CreatedAt:  getStringVal(job, "created_at"),
			DurationMs: getInt64Val(job, "duration_ms"),
		}

		// NOTE: commit_sha is present in the job payload but ActionInfo does not
		// yet carry a CommitSHA field, so it is intentionally not mapped here.

		actions = append(actions, action)
	}

	return actions, nil
}

// GetActionsByEventID fetches actions triggered by a specific LGH event
func (c *HTTPClient) GetActionsByEventID(eventID string) ([]ActionInfo, error) {
	url := fmt.Sprintf("%s/api/actions/by-event/%s", c.baseURL, eventID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch actions by event: %w", err)
	}
	defer closeBody(resp.Body, "get actions by event")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var jobs []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var actions []ActionInfo
	for _, job := range jobs {
		action := ActionInfo{
			ID:         getStringVal(job, "id"),
			Repo:       getStringVal(job, "repo"),
			PluginName: getStringVal(job, "plugin_name"),
			Status:     getStringVal(job, "status"),
			CreatedAt:  getStringVal(job, "created_at"),
			DurationMs: getInt64Val(job, "duration_ms"),
		}
		actions = append(actions, action)
	}

	return actions, nil
}

// GetAction fetches a single action by ID
func (c *HTTPClient) GetAction(id string) (*ActionDetail, error) {
	url := fmt.Sprintf("%s/api/actions/%s", c.baseURL, id)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch action: %w", err)
	}
	defer closeBody(resp.Body, "get action")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var job map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	detail := &ActionDetail{
		ID:            getStringVal(job, "id"),
		Repo:          getStringVal(job, "repo"),
		PluginName:    getStringVal(job, "plugin_name"),
		Status:        getStringVal(job, "status"),
		Progress:      getStringVal(job, "progress"),
		CreatedAt:     getStringVal(job, "created_at"),
		StartedAt:     getStringVal(job, "started_at"),
		EndedAt:       getStringVal(job, "ended_at"),
		DurationMs:    getInt64Val(job, "duration_ms"),
		Profile:       getStringVal(job, "profile"),
		PluginVersion: getStringVal(job, "plugin_version"),
		Intent:        getStringVal(job, "intent"),
	}

	if commit, ok := job["commit"].(map[string]interface{}); ok {
		detail.Commit = commit
	}

	return detail, nil
}

// ReloadPlugins triggers a plugin reload
func (c *HTTPClient) ReloadPlugins() (*ReloadResult, error) {
	resp, err := c.httpClient.Post(c.baseURL+"/api/plugins/reload", "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to reload plugins: %w", err)
	}
	defer closeBody(resp.Body, "reload plugins")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result ReloadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetStatus fetches ActionD server status
func (c *HTTPClient) GetStatus() (*StatusInfo, error) {
	plugins, err := c.GetPlugins()
	if err != nil {
		return &StatusInfo{Running: false}, nil
	}

	actions, _ := c.GetActions(1000)

	return &StatusInfo{
		Running:     true,
		Version:     version.Version,
		PluginCount: len(plugins),
		ActionCount: len(actions),
	}, nil
}

// GetLogs reads ActionD log file
func (c *HTTPClient) GetLogs(limit int) ([]LogEntry, error) {
	// Log file is stored locally, not via HTTP API
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	logPath := filepath.Join(home, ".localgithub", "actions", "actiond.log")

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return []LogEntry{{Message: "No log file found. ActionD may not have been started yet."}}, nil
	}

	// Read log file
	file, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer closeBody(file, "log file")

	// Read all lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Get last N lines
	start := len(lines) - limit
	if start < 0 {
		start = 0
	}

	// Convert to LogEntry
	var entries []LogEntry
	for i := start; i < len(lines); i++ {
		line := lines[i]
		entry := LogEntry{Message: line}

		// Try to extract level from common patterns
		if strings.Contains(line, "❌") || strings.Contains(line, "ERROR") {
			entry.Level = "ERROR"
		} else if strings.Contains(line, "⚠️") || strings.Contains(line, "WARN") {
			entry.Level = "WARN"
		} else if strings.Contains(line, "✅") || strings.Contains(line, "INFO") {
			entry.Level = "INFO"
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// CancelAction cancels a running action
func (c *HTTPClient) CancelAction(id string) error {
	resp, err := c.httpClient.Post(c.baseURL+"/api/actions/"+id+"/cancel", "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to cancel action: %w", err)
	}
	defer closeBody(resp.Body, "cancel action")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// RetryAction retries a failed action
func (c *HTTPClient) RetryAction(id string) error {
	resp, err := c.httpClient.Post(c.baseURL+"/api/actions/"+id+"/retry", "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to retry action: %w", err)
	}
	defer closeBody(resp.Body, "retry action")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ApproveAction approves a pending action
func (c *HTTPClient) ApproveAction(id string) error {
	resp, err := c.httpClient.Post(c.baseURL+"/api/actions/"+id+"/approve", "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to approve action: %w", err)
	}
	defer closeBody(resp.Body, "approve action")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetPluginEnabled enables or disables a plugin
func (c *HTTPClient) SetPluginEnabled(name string, enabled bool) error {
	body, _ := json.Marshal(map[string]bool{"enabled": enabled})
	resp, err := c.httpClient.Post(c.baseURL+"/api/plugins/"+name+"/toggle", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to toggle plugin: %w", err)
	}
	defer closeBody(resp.Body, "toggle plugin")

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetProfile returns the current execution profile
func (c *HTTPClient) GetProfile() (string, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/profile")
	if err != nil {
		return "", fmt.Errorf("failed to get profile: %w", err)
	}
	defer closeBody(resp.Body, "get profile")

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode profile: %w", err)
	}
	return result["profile"], nil
}

// SetProfile sets the execution profile
func (c *HTTPClient) SetProfile(profile string) error {
	body, _ := json.Marshal(map[string]string{"profile": profile})
	resp, err := c.httpClient.Post(c.baseURL+"/api/profile", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to set profile: %w", err)
	}
	defer closeBody(resp.Body, "set profile")

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// CleanupActions asks the ActionD server to delete terminal jobs older than
// the retention window (days; 0 = all terminal jobs) together with their
// artifact directories.
func (c *HTTPClient) CleanupActions(days int, all bool) (*CleanupResult, error) {
	url := fmt.Sprintf("%s/api/actions/cleanup?days=%d", c.baseURL, days)
	if all {
		url = c.baseURL + "/api/actions/cleanup?all=true"
	}
	resp, err := c.httpClient.Post(url, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to cleanup actions: %w", err)
	}
	defer closeBody(resp.Body, "cleanup actions")

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result CleanupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode cleanup response: %w", err)
	}
	return &result, nil
}

// Helper functions
func getStringVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func closeBody(closer io.Closer, label string) {
	if err := closer.Close(); err != nil {
		fmt.Printf("failed to close %s response body: %v\n", label, err)
	}
}

func getInt64Val(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// Ensure HTTPClient implements ActionDClient
var _ ActionDClient = (*HTTPClient)(nil)
