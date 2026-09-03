// Copyright (c) 2025 JoeGlenn1213
// ActionD Web Server

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/interpreter"
	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/pubsub"
	"github.com/JoeGlenn1213/actiond/internal/store"
)

func init() {
	mustAddExtensionType(".css", "text/css")
	mustAddExtensionType(".js", "application/javascript")
	mustAddExtensionType(".mjs", "application/javascript")
	mustAddExtensionType(".svg", "image/svg+xml")
}

// Server serves the ActionD Web Console and API
type Server struct {
	addr              string
	store             store.Store
	plugins           []plugin.Plugin
	catalog           []plugin.Plugin
	staticDir         string
	pluginState       map[string]bool // enabled/disabled state
	pubsub            *pubsub.PubSub
	configManager     *ConfigManager
	reloadFunc        func() []plugin.Plugin // Callback to reload plugins
	profileChangeFunc func(string)           // Callback when profile changes via API
	cancelFunc        func(string) bool
	retryFunc         func(string) (string, error)
}

// New creates a new server
func New(addr string, store store.Store, plugins []plugin.Plugin, staticDir string) *Server {
	if addr == "" {
		// Loopback-only by default: the API is unauthenticated and can run
		// arbitrary exec plugins, so it must not be reachable from the LAN.
		// Override with ACTIOND_BIND (or an explicit addr) if ever needed.
		addr = "127.0.0.1:3000"
	}
	// Default enabled
	state := make(map[string]bool)
	for _, p := range plugins {
		state[p.Name()] = true
	}

	if staticDir == "" {
		staticDir = ""
	}

	return &Server{
		addr:          addr,
		store:         store,
		plugins:       plugins,
		catalog:       append([]plugin.Plugin(nil), plugins...),
		staticDir:     staticDir,
		pluginState:   state,
		configManager: NewConfigManager(),
	}
}

// SetPubSub sets the PubSub instance (call this after creating worker)
func (s *Server) SetPubSub(ps *pubsub.PubSub) {
	s.pubsub = ps
}

// SetReloadFunc sets the plugin reload callback function
func (s *Server) SetReloadFunc(fn func() []plugin.Plugin) {
	s.reloadFunc = fn
}

// SetCancelFunc sets the job cancellation callback.
func (s *Server) SetCancelFunc(fn func(string) bool) {
	s.cancelFunc = fn
}

// SetRetryFunc sets the job retry callback.
func (s *Server) SetRetryFunc(fn func(string) (string, error)) {
	s.retryFunc = fn
}

// SetPluginCatalog updates the full set of known plugins, including disabled ones.
func (s *Server) SetPluginCatalog(plugins []plugin.Plugin) {
	s.catalog = append([]plugin.Plugin(nil), plugins...)
}

// IsPluginEnabled checks if a plugin is enabled (used by Dispatcher)
func (s *Server) IsPluginEnabled(name string) bool {
	return s.configManager.IsPluginEnabled(name)
}

// Start API server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/actions", s.handleActions)
	mux.HandleFunc("/api/actions/cleanup", s.handleActionsCleanup)
	mux.HandleFunc("/api/actions/clear-all", s.handleActionsClearAll)
	mux.HandleFunc("/api/actions/by-event/", s.handleActionsByEvent) // Query by event_id
	mux.HandleFunc("/api/actions/", s.handleActionRoute)             // Handles /id, /id/stream, /id/artifacts
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/reload", s.handlePluginsReload) // Hot reload endpoint
	mux.HandleFunc("/api/plugins/", s.handlePluginRoute)         // Handles CRUD operations
	mux.HandleFunc("/api/profile", s.handleProfile)              // Get/Set execution profile
	mux.HandleFunc("/api/health", s.handleHealth)                // Liveness probe for the console
	mux.HandleFunc("/api/lgh/", s.handleLghRoute)                // LGH proxy: ping + commit status

	// Static Files with SPA fallback for Next.js dynamic routes
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := r.URL.Path

		if s.staticDir == "" {
			http.Error(w, "ActionD web assets not configured. Start with --web-dir to enable the console.", http.StatusServiceUnavailable)
			return
		}

		// Check if the requested file exists
		filePath := filepath.Join(s.staticDir, path)
		if path == "/" {
			filePath = filepath.Join(s.staticDir, "index.html")
		}

		// If path doesn't exist, resolve a fallback candidate:
		// 1. the legacy /actions/{id} placeholder (kept for older exports)
		// 2. {path}/index.html and {path}.html (static-export layouts)
		// 3. the root index.html for extensionless page routes (generic SPA
		//    fallback — lets /events/{id} and future client routes deep-link)
		if _, err := os.Stat(filePath); os.IsNotExist(err) && !strings.Contains(path, "..") {
			// Legacy: /actions/{id} -> /actions/placeholder.html
			if strings.HasPrefix(path, "/actions/") && path != "/actions/" {
				fallbackPath := filepath.Join(s.staticDir, "actions", "placeholder.html")
				if _, err := os.Stat(fallbackPath); err == nil {
					http.ServeFile(w, r, fallbackPath)
					fmt.Printf("   🌐 Web: %s %s (SPA fallback, %v)\n", r.Method, path, time.Since(start))
					return
				}
			}
			for _, candidate := range []string{
				filepath.Join(s.staticDir, path, "index.html"),
				filepath.Join(s.staticDir, path+".html"),
			} {
				if _, err := os.Stat(candidate); err == nil {
					http.ServeFile(w, r, candidate)
					fmt.Printf("   🌐 Web: %s %s (html fallback, %v)\n", r.Method, path, time.Since(start))
					return
				}
			}
			// Generic SPA fallback: extensionless GET accepting HTML gets the
			// app shell; the client router resolves (or 404s) the route.
			if r.Method == http.MethodGet &&
				filepath.Ext(path) == "" &&
				strings.Contains(r.Header.Get("Accept"), "text/html") {
				indexPath := filepath.Join(s.staticDir, "index.html")
				if _, err := os.Stat(indexPath); err == nil {
					http.ServeFile(w, r, indexPath)
					fmt.Printf("   🌐 Web: %s %s (SPA index fallback, %v)\n", r.Method, path, time.Since(start))
					return
				}
			}
		}

		// Default: serve static files
		if info, err := os.Stat(filePath); err == nil && info.IsDir() && path != "/" {
			// Directory request: only serve it when it has an index page —
			// otherwise Go's FileServer would render a directory listing
			// (e.g. /_next/ listing build assets).
			if _, err := os.Stat(filepath.Join(filePath, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		fs := http.FileServer(http.Dir(s.staticDir))
		fs.ServeHTTP(w, r)
		fmt.Printf("   🌐 Web: %s %s (%v)\n", r.Method, r.URL.Path, time.Since(start))
	}))

	if s.staticDir != "" {
		fmt.Printf("🌍 Web Console running at http://%s\n", s.addr)
	} else {
		fmt.Printf("🌐 API server running at http://%s (web console disabled)\n", s.addr)
	}
	return http.ListenAndServe(s.addr, s.secureWrap(mux))
}

// allowedOrigins is the CORS allowlist: the console itself (same origin),
// the local dev server, plus anything configured via ACTIOND_CORS_ORIGIN
// (comma-separated). Everything else — including public websites — is denied.
func allowedOrigins() map[string]bool {
	origins := map[string]bool{
		"http://localhost:3000": true,
		"http://127.0.0.1:3000": true,
		"http://localhost:3001": true,
		"http://127.0.0.1:3001": true,
	}
	if extra := os.Getenv("ACTIOND_CORS_ORIGIN"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins[o] = true
			}
		}
	}
	return origins
}

// apiToken returns the optional shared-secret from ACTIOND_TOKEN. When set,
// every /api/* request must present it (Authorization: Bearer <t>,
// X-ActionD-Token: <t> or ?token=<t> for SSE EventSource, which cannot set
// headers). Static console assets stay open — they contain no secrets.
// Unset (default): no auth, loopback trust model as before.
var apiToken = os.Getenv("ACTIOND_TOKEN")

func requestAuthorized(r *http.Request) bool {
	if apiToken == "" {
		return true
	}
	if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer ")) == apiToken
	}
	if r.Header.Get("X-ActionD-Token") == apiToken {
		return true
	}
	// EventSource cannot send custom headers.
	if r.URL.Query().Get("token") == apiToken {
		return true
	}
	return false
}

// secureWrap applies the API security policy around the whole mux:
//
//   - X-Content-Type-Options: nosniff on every response;
//   - CORS restricted to the allowlist (was: Access-Control-Allow-Origin: *,
//     which let any website read API data and pass preflights);
//   - CSRF defense for writes: browsers always attach an Origin header to
//     cross-site requests, so a write with a non-allowlisted Origin is
//     rejected. Origin-less requests are non-browser tools (curl, MCP) and
//     pass. This also covers "simple" fire-and-forget POSTs that never
//     trigger a CORS preflight (e.g. clear-all).
func (s *Server) secureWrap(next *http.ServeMux) http.Handler {
	origins := allowedOrigins()

	originAllowed := func(origin string) bool {
		return origins[origin]
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Optional shared-secret auth for API routes (static assets exempt).
		if apiToken != "" && strings.HasPrefix(r.URL.Path, "/api/") && !requestAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		origin := r.Header.Get("Origin")
		allowed := origin != "" && originAllowed(origin)

		// CORS preflight
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if !allowed {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-ActionD-Token")
			w.Header().Add("Vary", "Origin")
			w.WriteHeader(http.StatusOK)
			return
		}

		// Echo the allowlist for allowed cross-origin reads (dev server).
		// Same-origin browser requests and curl (no Origin) need no ACAO.
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		// Write-method CSRF gate (see comment above).
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if origin != "" && !allowed {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// GET /api/actions - List actions
// POST /api/actions/{id}/approve - Approve a pending job
func (s *Server) handleActionApprove(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := s.store.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Determine artifact directory from ActionJob logic
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Server error: no home dir", http.StatusInternalServerError)
		return
	}
	baseDir := filepath.Join(home, ".localgithub", "actions")
	pattern := filepath.Join(baseDir, "*_"+jobID)

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		http.Error(w, "Job artifact directory not found", http.StatusNotFound)
		return
	}

	artifactDir := matches[0]
	statusFile := filepath.Join(artifactDir, "approval-status.json")

	// Create or update the approval-status.json file
	approvalData := map[string]interface{}{
		"status":      "approved",
		"approved_by": "ActionD CLI/API",
		"approved_at": time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(approvalData, "", "  ")
	if err != nil {
		http.Error(w, "Failed to marshal approval data", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(statusFile, data, 0644); err != nil {
		http.Error(w, "Failed to write approval file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  "success",
		"message": "Job approved",
		"job_id":  jobID,
	})
}

// GET /api/actions - List actions
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		jobs, err := s.store.ListJobs(100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, jobs)
	case "OPTIONS":
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// actionBaseDir returns the artifact store root. Overridable via
// ACTIOND_ACTIONS_DIR for tests and custom deployments; defaults to
// ~/.localgithub/actions.
func actionBaseDir() string {
	if dir := os.Getenv("ACTIOND_ACTIONS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".localgithub", "actions")
	}
	return filepath.Join(home, ".localgithub", "actions")
}

// jobArtifactDirRe matches ActionD job artifact directory names:
// <timestamp>_<event>_<repo>_<job-uuid>
var jobArtifactDirRe = regexp.MustCompile(`^.+_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// POST /api/actions/cleanup - Delete old completed/failed actions
// Query params:
//   days - retention window in days (default 7; 0 = no age limit)
//   all  - "true" is an alias for days=0 (delete every terminal job)
// Only terminal jobs (done/failed/cancelled) are ever deleted; pending and
// running jobs are preserved regardless of age.
func (s *Server) handleActionsCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			days = n
		}
	}
	if r.URL.Query().Get("all") == "true" {
		days = 0
	}

	// With days=0 the cutoff is placed just ahead of "now" so every terminal
	// job falls below it while clock-skewed future timestamps stay safe.
	cutoff := time.Now().AddDate(0, 0, -days)
	if days == 0 {
		cutoff = time.Now().Add(time.Minute)
	}

	jobs, err := s.store.ListJobs(100000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	baseDir := actionBaseDir()

	known := make(map[string]bool, len(jobs))
	deletedJobs := 0
	deletedDirs := 0
	for _, j := range jobs {
		known[j.ID] = true
		if !j.CreatedAt.Before(cutoff) || !isTerminalJobStatus(j.Status) {
			continue
		}
		if matches, err := filepath.Glob(filepath.Join(baseDir, "*_"+j.ID)); err == nil {
			for _, dir := range matches {
				if err := os.RemoveAll(dir); err == nil {
					deletedDirs++
				}
			}
		}
		if err := s.store.DeleteJob(j.ID); err == nil {
			deletedJobs++
		}
	}

	// Sweep orphan artifact directories (no matching job in the DB) that are
	// older than the cutoff, so abandoned disk usage is reclaimed too.
	orphans := sweepOrphanArtifactDirs(baseDir, known, cutoff)
	deletedDirs += orphans

	writeJSON(w, map[string]interface{}{
		"status":         "success",
		"deleted_jobs":   deletedJobs,
		"deleted_dirs":   deletedDirs,
		"retention_days": days,
		"message":        fmt.Sprintf("Deleted %d jobs and %d artifact directories (retention: %d days)", deletedJobs, deletedDirs, days),
	})
}

// isTerminalJobStatus reports whether a job status is final and safe to clean.
func isTerminalJobStatus(status job.Status) bool {
	switch status {
	case job.StatusDone, job.StatusFailed, job.StatusCanceled:
		return true
	default:
		return false
	}
}

// sweepOrphanArtifactDirs deletes artifact directories under baseDir whose
// trailing job ID is absent from known and whose mtime predates cutoff.
// Only names matching jobArtifactDirRe are considered, so actiond.db,
// actiond.log and other runtime files are never touched.
func sweepOrphanArtifactDirs(baseDir string, known map[string]bool, cutoff time.Time) int {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 0
	}
	deleted := 0
	for _, e := range entries {
		if !e.IsDir() || !jobArtifactDirRe.MatchString(e.Name()) {
			continue
		}
		name := e.Name()
		id := name[strings.LastIndex(name, "_")+1:]
		if known[id] {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(baseDir, name)); err == nil {
			deleted++
		}
	}
	return deleted
}

// POST /api/actions/clear-all - Delete ALL actions (with confirmation)
func (s *Server) handleActionsClearAll(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all jobs first (to know how many we'll delete)
	jobs, err := s.store.ListJobs(10000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete artifact directories
	baseDir := actionBaseDir()
	deletedDirs := 0

	for _, job := range jobs {
		// Find and delete artifact directory for this job
		pattern := filepath.Join(baseDir, "*_"+job.ID)
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			for _, dir := range matches {
				if err := os.RemoveAll(dir); err == nil {
					deletedDirs++
				}
			}
		}
	}

	// Clear database using SQL (not file deletion) - requires type assertion
	type Clearer interface {
		ClearAll() (int64, error)
	}
	if clearer, ok := s.store.(Clearer); ok {
		if _, err := clearer.ClearAll(); err != nil {
			fmt.Printf("Warning: Failed to clear database: %v\n", err)
		}
	}

	writeJSON(w, map[string]interface{}{
		"status":       "success",
		"jobs_deleted": len(jobs),
		"dirs_deleted": deletedDirs,
		"message":      "All actions cleared successfully!",
	})
}

// GET /api/actions/by-event/{event_id} - List jobs triggered by a specific event
func (s *Server) handleActionsByEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	eventID := strings.TrimPrefix(r.URL.Path, "/api/actions/by-event/")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	// Try SQLite store first (has ListJobsByEventID)
	type EventIDQuerier interface {
		ListJobsByEventID(eventID string) ([]*job.ActionJob, error)
	}
	if querier, ok := s.store.(EventIDQuerier); ok {
		jobs, err := querier.ListJobsByEventID(eventID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, jobs)
		return
	}

	// Fallback: list all and filter (for memory store)
	jobs, err := s.store.ListJobs(500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var filtered []*job.ActionJob
	for _, j := range jobs {
		if j.EventID == eventID {
			filtered = append(filtered, j)
		}
	}
	writeJSON(w, filtered)
}

// Dispatcher for /api/actions/{id}/* routes
func (s *Server) handleActionRoute(w http.ResponseWriter, r *http.Request) {
	// Path: /api/actions/{id}/...
	path := strings.TrimPrefix(r.URL.Path, "/api/actions/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	jobID := parts[0]

	// 1. GET /api/actions/{id} -> Job Details
	if len(parts) == 1 {
		s.handleJobDetails(w, r, jobID)
		return
	}

	// 2. Sub-resources
	switch parts[1] {
	case "stream":
		s.handleActionStream(w, r, jobID)
	case "artifacts":
		if len(parts) >= 3 {
			s.handleArtifact(w, r, jobID, parts[2])
		} else {
			http.Error(w, "Artifact name required", http.StatusBadRequest)
		}
	case "cancel":
		s.handleActionCancel(w, r, jobID)
	case "retry":
		s.handleActionRetry(w, r, jobID)
	case "approve":
		s.handleActionApprove(w, r, jobID)
	case "diagnose":
		s.handleActionDiagnose(w, r, jobID)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// GET /api/actions/{id}
func (s *Server) handleJobDetails(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	job, err := s.store.GetJob(jobID)
	if err != nil {
		// Try to find by RunID if UUID not found (Future improvement)
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	writeJSON(w, job)
}

// POST /api/actions/{id}/cancel - Cancel a running job
func (s *Server) handleActionCancel(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	job, err := s.store.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Check if job can be cancelled
	if job.Status == "done" || job.Status == "failed" || job.Status == "cancelled" {
		writeJSON(w, map[string]interface{}{
			"status":  "error",
			"message": "Cannot cancel job with status: " + job.Status,
		})
		return
	}

	if s.cancelFunc == nil {
		http.Error(w, "Cancel not configured", http.StatusServiceUnavailable)
		return
	}

	if !s.cancelFunc(jobID) {
		http.Error(w, "Job is not cancellable", http.StatusConflict)
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  "success",
		"message": "Job cancelled",
		"job_id":  jobID,
	})
}

// POST /api/actions/{id}/retry - Retry a failed job
func (s *Server) handleActionRetry(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	job, err := s.store.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	if job.Status != "failed" && job.Status != "cancelled" {
		http.Error(w, "Only failed or cancelled jobs can be retried", http.StatusConflict)
		return
	}

	if s.retryFunc == nil {
		http.Error(w, "Retry not configured", http.StatusServiceUnavailable)
		return
	}

	newJobID, err := s.retryFunc(jobID)
	if err != nil {
		http.Error(w, "Failed to retry job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":       "success",
		"message":      "Job queued for retry",
		"job_id":       newJobID,
		"retried_from": jobID,
	})
}

// GET /api/actions/{id}/diagnose - Analyze a failed job and return structured fix suggestions
// This exposes the same logic as the actiond_diagnose MCP tool over plain HTTP,
// so CLI users can call it directly without going through the MCP server.
func (s *Server) handleActionDiagnose(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	j, err := s.store.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Build the error text to analyse. Use the structured error field from the
	// job record — this is already the cleaned stderr/stdout captured by the worker.
	errorText := j.Error
	if j.ErrorSummary != "" && !strings.Contains(errorText, j.ErrorSummary) {
		errorText = j.ErrorSummary + "\n" + errorText
	}

	analysis := interpreter.Analyze(errorText)

	// Extract file paths mentioned in the error (e.g. "main.go:42")
	relatedFiles := extractMentionedFiles(errorText)

	writeJSON(w, map[string]interface{}{
		"job_id":        jobID,
		"plugin":        j.PluginName,
		"repo":          j.Repo,
		"status":        j.Status,
		"duration_ms":   j.DurationMs,
		"error_summary": j.ErrorSummary,
		"analysis":      analysis,
		"related_files": relatedFiles,
	})
}

// extractMentionedFiles pulls file:line references out of error output.
// Mirrors the logic in internal/mcp/server.go extractRelatedFiles.
var fileRefPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[\w/\\.-]+\.go:\d+`),
	regexp.MustCompile(`[\w/\\.-]+\.py:\d+`),
	regexp.MustCompile(`[\w/\\.-]+\.ts:\d+`),
	regexp.MustCompile(`[\w/\\.-]+\.js:\d+`),
	regexp.MustCompile(`[\w/\\.-]+\.java:\d+`),
}

func extractMentionedFiles(text string) []string {
	seen := make(map[string]bool)
	var files []string
	for _, re := range fileRefPatterns {
		for _, m := range re.FindAllString(text, -1) {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	return files
}

// GET /api/actions/{id}/artifacts/{name}
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request, jobID, artifactName string) {
	// Basic security check
	if strings.Contains(artifactName, "..") || strings.Contains(artifactName, "/") || strings.Contains(artifactName, "\\") {
		http.Error(w, "Invalid artifact name", http.StatusBadRequest)
		return
	}

	_, err := s.store.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Find artifact directory
	// Search in ~/.localgithub/actions/*_{jobID}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("   📦 Artifact lookup error: %v\n", err)
		http.Error(w, "Server error: no home dir", http.StatusInternalServerError)
		return
	}
	baseDir := filepath.Join(home, ".localgithub", "actions")
	pattern := filepath.Join(baseDir, "*_"+jobID)

	fmt.Printf("   📦 Artifact request: job %s\n", jobID)

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		fmt.Printf("   📦 No artifact dir for job %s\n", jobID)
		http.Error(w, "Artifacts not found", http.StatusNotFound)
		return
	}

	// Use the first match
	artifactDir := matches[0]
	filePath := filepath.Join(artifactDir, artifactName)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "Artifact file not found", http.StatusNotFound)
		return
	}

	// Sandbox downloaded artifacts: reports are often HTML generated from
	// repo content, and without this they would run scripts in the console's
	// origin (unauthenticated API on localhost).
	w.Header().Set("Content-Security-Policy", "sandbox allow-downloads")
	http.ServeFile(w, r, filePath)
}

// GET /api/actions/{id}/stream
func (s *Server) handleActionStream(w http.ResponseWriter, r *http.Request, actionID string) {
	if s.pubsub == nil {
		http.Error(w, "Streaming not available", http.StatusServiceUnavailable)
		return
	}

	// Unknown job ids get a plain 404 instead of an indefinitely-hanging
	// SSE connection (idle-connection accumulation).
	j, err := s.store.GetJob(actionID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Flush headers
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	// Already-finished jobs have nothing to stream: emit a single done frame
	// so the client settles immediately, then close.
	if j.Status == job.StatusDone || j.Status == job.StatusFailed || j.Status == job.StatusCanceled {
		done := pubsub.ProgressMessage{
			JobID:     actionID,
			Timestamp: time.Now().UTC(),
			Line:      fmt.Sprintf("job already finished with status %s", j.Status),
			Done:      true,
		}
		data, _ := json.Marshal(done)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	// Subscribe to progress updates
	ch, cleanup := s.pubsub.Subscribe(actionID)
	defer cleanup()

	fmt.Printf("   📡 SSE: Client connected for job %s\n", actionID)

	// Stream messages until done or client disconnects
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return // Channel closed
			}

			// Send SSE event
			data, _ := json.Marshal(msg)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				fmt.Printf("   📡 SSE: Failed to write event for job %s: %v\n", actionID, err)
				return
			}
			flusher.Flush()

			// Close connection when job is done
			if msg.Done {
				fmt.Printf("   📡 SSE: Job %s completed, closing connection\n", actionID)
				return
			}

		case <-r.Context().Done():
			fmt.Printf("   📡 SSE: Client disconnected for job %s\n", actionID)
			return
		}
	}
}

// GET /api/plugins - List all plugins
// POST /api/plugins - Create new plugin
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.listPlugins(w, r)
	case "POST":
		s.createPlugin(w, r)
	case "OPTIONS":
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	type PluginInfo struct {
		Name       string   `json:"name"`
		Triggers   []string `json:"triggers"`
		Languages  []string `json:"languages,omitempty"`
		Filter     string   `json:"filter,omitempty"`
		RepoFilter string   `json:"repoFilter,omitempty"`
		Enabled    bool     `json:"enabled"`
		Type       string   `json:"type"`
		Command    string   `json:"command,omitempty"`
		Args       []string `json:"args,omitempty"`
		Timeout    string   `json:"timeout,omitempty"`
		WorkingDir string   `json:"workingDir,omitempty"`
		IsCustom   bool     `json:"isCustom"` // true if from config.json
	}

	displayPlugins := s.catalog
	if len(displayPlugins) == 0 {
		displayPlugins = s.plugins
	}

	res := make([]PluginInfo, 0, len(displayPlugins))
	seen := make(map[string]bool, len(displayPlugins))

	for _, p := range displayPlugins {
		pType := "go"
		filter := ""
		repoFilter := ""
		if ep, ok := p.(*plugin.ExecPlugin); ok {
			pType = "exec"
			filter = ep.RefFilter()
			repoFilter = ep.RepoFilter()
		}

		cfg, hasConfig := s.configManager.GetPlugin(p.Name())
		isCustom := hasConfig && IsCustomPlugin(cfg)

		res = append(res, PluginInfo{
			Name:       p.Name(),
			Triggers:   p.Triggers(),
			Languages:  p.Languages(),
			Filter:     filter,
			RepoFilter: repoFilter,
			Enabled:    s.configManager.IsPluginEnabled(p.Name()),
			Type:       pType,
			IsCustom:   isCustom,
		})
		seen[p.Name()] = true
	}

	userPlugins := s.configManager.GetAllPlugins()
	for name, cfg := range userPlugins {
		if seen[name] || !IsCustomPlugin(cfg) {
			continue
		}

		enabled := true
		if cfg.Enabled != nil {
			enabled = *cfg.Enabled
		}

		res = append(res, PluginInfo{
			Name:       name,
			Triggers:   cfg.Triggers,
			Languages:  cfg.Languages,
			Filter:     cfg.RefFilter,
			RepoFilter: cfg.RepoFilter,
			Enabled:    enabled,
			Type:       cfg.Type,
			Command:    cfg.Command,
			Args:       cfg.Args,
			Timeout:    cfg.Timeout,
			WorkingDir: cfg.WorkingDir,
			IsCustom:   true,
		})
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	writeJSON(w, res)
}

func (s *Server) createPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string   `json:"name"`
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		Triggers   []string `json:"triggers"`
		Languages  []string `json:"languages"`
		Timeout    string   `json:"timeout"`
		WorkingDir string   `json:"workingDir"`
		RefFilter  string   `json:"refFilter"`
		RepoFilter string   `json:"repoFilter"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Plugin name is required", http.StatusBadRequest)
		return
	}

	// Names become URL path segments and config keys: restrict to a safe
	// charset so every plugin stays addressable by the API (a name with "/"
	// could never be GET/PUT/DELETEd or toggled afterwards).
	if !pluginNamePattern.MatchString(req.Name) {
		http.Error(w, "Invalid plugin name: use 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit", http.StatusBadRequest)
		return
	}

	cfg := PluginConfig{
		Type:       "exec",
		Command:    req.Command,
		Args:       req.Args,
		Triggers:   req.Triggers,
		Languages:  req.Languages,
		Timeout:    req.Timeout,
		WorkingDir: req.WorkingDir,
		RefFilter:  req.RefFilter,
		RepoFilter: req.RepoFilter,
	}
	enabled := true
	cfg.Enabled = &enabled

	if err := s.configManager.AddPlugin(req.Name, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.configManager.Save(); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.reloadPluginsRuntime()

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "created", "name": req.Name})
}

// Dispatcher for /api/plugins/{name}/* routes
func (s *Server) handlePluginRoute(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	pluginName := parts[0]

	// Check for sub-routes
	if len(parts) > 1 {
		switch parts[1] {
		case "toggle":
			s.togglePlugin(w, r, pluginName)
			return
		}
	}

	// Handle main plugin routes
	switch r.Method {
	case "GET":
		s.getPlugin(w, r, pluginName)
	case "PUT":
		s.updatePlugin(w, r, pluginName)
	case "DELETE":
		s.deletePlugin(w, r, pluginName)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getPlugin(w http.ResponseWriter, r *http.Request, name string) {
	cfg, exists := s.configManager.GetPlugin(name)
	if !exists {
		// Check hardcoded plugins
		for _, p := range s.plugins {
			if p.Name() == name {
				writeJSON(w, map[string]interface{}{
					"name":      p.Name(),
					"triggers":  p.Triggers(),
					"languages": p.Languages(),
					"repoFilter": func() string {
						if ep, ok := p.(*plugin.ExecPlugin); ok {
							return ep.RepoFilter()
						}
						return ""
					}(),
					"filter": func() string {
						if ep, ok := p.(*plugin.ExecPlugin); ok {
							return ep.RefFilter()
						}
						return ""
					}(),
					"enabled":  s.configManager.IsPluginEnabled(name),
					"isCustom": false,
				})
				return
			}
		}
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}
	writeJSON(w, cfg)
}

func (s *Server) updatePlugin(w http.ResponseWriter, r *http.Request, name string) {
	var cfg PluginConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// For hardcoded plugins, we can only update enabled state and triggers
	isHardcoded := false
	for _, p := range s.plugins {
		if p.Name() == name {
			isHardcoded = true
			break
		}
	}

	if isHardcoded {
		// Merge with existing config
		existing, _ := s.configManager.GetPlugin(name)
		if cfg.Enabled != nil {
			existing.Enabled = cfg.Enabled
		}
		if len(cfg.Triggers) > 0 {
			existing.Triggers = cfg.Triggers
		}
		cfg = existing
	}

	if err := s.configManager.AddPlugin(name, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.configManager.Save(); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.reloadPluginsRuntime()

	writeJSON(w, map[string]string{"status": "updated", "name": name})
}

// pluginNamePattern bounds plugin names to URL/config-safe characters.
var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (s *Server) deletePlugin(w http.ResponseWriter, r *http.Request, name string) {
	// A plugin is deletable exactly when the user config *defines* it
	// (non-empty Command — created via this API or hand-edited config).
	// Built-ins that were merely toggled also appear in the config, but only
	// as an empty shell holding Enabled. Judging by s.plugins/s.catalog was
	// wrong: both also contain config-defined plugins after a reload, which
	// made API-created plugins API-undeletable.
	if cfg, exists := s.configManager.GetPlugin(name); !exists || cfg.Command == "" {
		http.Error(w, "Cannot delete built-in plugin. Use toggle to disable.", http.StatusBadRequest)
		return
	}

	if err := s.configManager.DeletePlugin(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.configManager.Save(); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.reloadPluginsRuntime()

	writeJSON(w, map[string]string{"status": "deleted", "name": name})
}

func (s *Server) togglePlugin(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.configManager.SetPluginEnabled(name, req.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.configManager.Save(); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.pluginState[name] = req.Enabled

	writeJSON(w, map[string]interface{}{"status": "toggled", "name": name, "enabled": req.Enabled})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	// CORS for dev
	if err := json.NewEncoder(w).Encode(data); err != nil {
		fmt.Printf("failed to encode JSON response: %v\n", err)
	}
}

func (s *Server) reloadPluginsRuntime() {
	if s.reloadFunc == nil {
		return
	}
	if newPlugins := s.reloadFunc(); newPlugins != nil {
		s.plugins = newPlugins
	}
}

// POST /api/plugins/reload - Hot reload plugins
func (s *Server) handlePluginsReload(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if reload function is available
	if s.reloadFunc == nil {
		http.Error(w, "Plugin reload not configured", http.StatusServiceUnavailable)
		return
	}

	// Reload plugins
	newPlugins := s.reloadFunc()
	s.plugins = newPlugins

	// Update plugin state
	for _, p := range newPlugins {
		if _, exists := s.pluginState[p.Name()]; !exists {
			s.pluginState[p.Name()] = true // New plugins enabled by default
		}
	}

	// Get plugin names
	names := make([]string, 0, len(newPlugins))
	for _, p := range newPlugins {
		names = append(names, p.Name())
	}

	writeJSON(w, map[string]interface{}{
		"status":     "success",
		"message":    "Plugins reloaded successfully",
		"count":      len(newPlugins),
		"pluginList": names,
	})
}

func mustAddExtensionType(ext, contentType string) {
	if err := mime.AddExtensionType(ext, contentType); err != nil {
		panic(fmt.Sprintf("failed to register MIME type %s: %v", ext, err))
	}
}

// handleProfile handles GET/POST /api/profile
// GET returns the current execution profile
// POST sets the execution profile (body: {"profile": "fast"|"full"|"release"})
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profile := s.configManager.GetProfile()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"profile": profile,
		})

	case http.MethodPost:
		var req struct {
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Profile = strings.ToLower(strings.TrimSpace(req.Profile))
		if req.Profile == "" {
			http.Error(w, "profile is required", http.StatusBadRequest)
			return
		}
		validProfiles := map[string]bool{"fast": true, "full": true, "release": true}
		if !validProfiles[req.Profile] {
			http.Error(w, "invalid profile, must be one of: fast, full, release", http.StatusBadRequest)
			return
		}

		if err := s.configManager.SetProfile(req.Profile); err != nil {
			http.Error(w, fmt.Sprintf("failed to save profile: %v", err), http.StatusInternalServerError)
			return
		}
		if err := s.configManager.Save(); err != nil {
			http.Error(w, fmt.Sprintf("failed to persist config: %v", err), http.StatusInternalServerError)
			return
		}

		// Notify dispatcher if callback is set
		if s.profileChangeFunc != nil {
			s.profileChangeFunc(req.Profile)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"profile": req.Profile,
			"status":  "updated",
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// LghBaseURL is the LGH daemon endpoint used by the console proxy routes
// here and by the worker's status callback (internal/app/app.go). Set
// ACTIOND_LGH_URL when LGH does not run on localhost:9418.
func LghBaseURL() string {
	if u := strings.TrimSuffix(os.Getenv("ACTIOND_LGH_URL"), "/"); u != "" {
		return u
	}
	return "http://localhost:9418"
}

var lghHTTPClient = &http.Client{Timeout: 3 * time.Second}

// handleHealth serves GET /api/health — a dedicated liveness probe for the
// web console (previously it had to poll /api/plugins as a ping).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "actiond",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

// handleLghRoute proxies LGH information to the console so the browser never
// needs to talk to LGH directly (avoids CORS and keeps one data source):
//
//	GET /api/lgh/ping                  -> probe LGH /health
//	GET /api/lgh/status/{repo}/{sha}   -> commit status report
func (s *Server) handleLghRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/lgh/")

	if path == "ping" {
		resp, err := lghHTTPClient.Get(LghBaseURL() + "/health")
		if err != nil {
			writeJSON(w, map[string]string{"status": "offline", "error": err.Error()})
			return
		}
		defer func() { _ = resp.Body.Close() }()
		writeJSON(w, map[string]string{"status": "online"})
		return
	}

	// /api/lgh/status/{repo}/{sha}
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[0] == "status" && parts[1] != "" && parts[2] != "" {
		repo, sha := parts[1], parts[2]
		// Guard the upstream URL path against traversal (URL decoding has
		// already happened by the time we see the parts).
		if strings.ContainsAny(repo, "./\\") || strings.ContainsAny(sha, "./\\") {
			http.Error(w, "invalid repo or sha", http.StatusBadRequest)
			return
		}
		upstream := fmt.Sprintf("%s/api/repos/%s/commits/%s/status", LghBaseURL(), repo, sha)
		resp, err := lghHTTPClient.Get(upstream)
		if err != nil {
			http.Error(w, "cannot reach LGH: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	http.Error(w, "unknown lgh route, expected /api/lgh/ping or /api/lgh/status/{repo}/{sha}", http.StatusNotFound)
}

// SetProfileChangeFunc sets a callback invoked when the profile changes via API.
func (s *Server) SetProfileChangeFunc(fn func(string)) {
	s.profileChangeFunc = fn
}

// GetProfile returns the current execution profile from config.
func (s *Server) GetProfile() string {
	return s.configManager.GetProfile()
}
