package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/artifact"
	"github.com/JoeGlenn1213/actiond/internal/dispatcher"
	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/pubsub"
	"github.com/JoeGlenn1213/actiond/internal/repopath"
	"github.com/JoeGlenn1213/actiond/internal/server"
	"github.com/JoeGlenn1213/actiond/internal/store"
	"github.com/JoeGlenn1213/actiond/internal/worker"
	"github.com/JoeGlenn1213/actiond/plugins/echo"
)

type Config struct {
	RepoRoot     string
	ReposDir     string // LGH bare repo root for isolated checkouts (default: ~/.localgithub/repos)
	CheckoutRoot string // isolated checkout root (default: ~/.localgithub/checkouts)
	DeepWikiPath string
	WebDir       string
}

func Run(cfg Config) error {
	if cfg.RepoRoot == "" {
		cwd, _ := os.Getwd()
		// Try to find the root where demo-go-project or other repos are located.
		// If cwd is ActionD, repo root should be its parent.
		if filepath.Base(cwd) == "ActionD" {
			cfg.RepoRoot = filepath.Dir(cwd)
		} else {
			cfg.RepoRoot = cwd
		}
		fmt.Printf("ℹ️  No --repo-root provided, defaulting to %s\n", cfg.RepoRoot)
	} else {
		fmt.Printf("ℹ️  Repo root: %s\n", cfg.RepoRoot)
	}

	if cfg.WebDir == "" {
		cfg.WebDir = DetectDefaultWebDir()
		if cfg.WebDir != "" {
			fmt.Printf("ℹ️  Auto-detected web assets: %s\n", cfg.WebDir)
		} else {
			fmt.Println("⚠️  Web assets not found; API will still run, pass --web-dir to enable the console")
		}
	} else {
		fmt.Printf("ℹ️  Web assets: %s\n", cfg.WebDir)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⏹️  Shutting down...")
		cancel()
	}()

	// 1. Initialize EventSource (subscribe to LGH socket)
	source := event.NewSocketSource("")
	if err := source.Start(ctx); err != nil {
		return fmt.Errorf("failed to connect to LGH: %v (make sure 'lgh serve' is running)", err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			fmt.Printf("⚠️  Failed to close event source: %v\n", err)
		}
	}()
	fmt.Println("✅ Connected to LGH socket")

	// 2. Register plugins
	plugins, catalog, err := loadPlugins(cfg.DeepWikiPath)
	if err != nil {
		return fmt.Errorf("failed to load plugins: %v", err)
	}

	fmt.Printf("✅ Loaded %d plugins: ", len(plugins))
	for i, p := range plugins {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(p.Name())
	}
	fmt.Println()

	// 3. Initialize Dispatcher
	disp := dispatcher.New(plugins, cfg.RepoRoot)

	// Isolated checkouts: default to the LGH bare repo root and a checkout
	// dir under ~/.localgithub so plugin jobs never run against the
	// developer's live working tree.
	if cfg.ReposDir == "" {
		cfg.ReposDir = repopath.DefaultReposDir()
	}
	if cfg.CheckoutRoot == "" {
		cfg.CheckoutRoot = repopath.DefaultCheckoutRoot()
	}
	if cfg.ReposDir != "" {
		fmt.Printf("🧹 Isolated checkouts: %s -> %s\n", cfg.ReposDir, cfg.CheckoutRoot)
		disp.SetCheckoutDirs(cfg.ReposDir, cfg.CheckoutRoot)
	} else {
		fmt.Println("⚠️  No bare repo root found; plugin jobs fall back to the local working tree")
	}

	resolvePlugin := func(name string) plugin.Plugin {
		for _, p := range plugins {
			if p.Name() == name {
				return p
			}
		}
		return nil
	}
	var work *worker.Worker

	// 4. Initialize Artifact Store & Job Store
	artifactStore := artifact.NewStore("")
	if err := os.MkdirAll(artifactStore.Root(), 0755); err != nil {
		return fmt.Errorf("failed to create artifact store: %w", err)
	}

	dbPath := filepath.Join(artifactStore.Root(), "actiond.db")
	jobStore, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize SQLite store: %v", err)
	}
	fmt.Printf("✅ Artifact store: %s\n", "~/.localgithub/actions/")
	fmt.Printf("✅ Job database:   %s\n", dbPath)

	// 5. Initialize PubSub (for streaming logs)
	ps := pubsub.New()

	// 6. Start Web Server
	// Loopback-only by default (the API is unauthenticated); ACTIOND_BIND
	// can override, e.g. "0.0.0.0:3000" for deliberate LAN exposure.
	bindAddr := os.Getenv("ACTIOND_BIND")
	if bindAddr == "" {
		bindAddr = "127.0.0.1:3000"
	}
	srv := server.New(bindAddr, jobStore, plugins, cfg.WebDir)
	srv.SetPubSub(ps) // Inject PubSub
	srv.SetPluginCatalog(catalog)

	// Set up plugin reload callback
	srv.SetReloadFunc(func() []plugin.Plugin {
		newPlugins, newCatalog, err := loadPlugins(cfg.DeepWikiPath)
		if err != nil {
			fmt.Printf("⚠️  Plugin reload failed: %v\n", err)
			return nil
		}
		plugins = newPlugins
		srv.SetPluginCatalog(newCatalog)
		disp.SetPlugins(newPlugins)
		if work != nil {
			work.SetPluginResolver(resolvePlugin)
		}
		fmt.Printf("🔄 Reloaded %d plugins\n", len(newPlugins))
		return newPlugins
	})

	// Wire up plugin enabled check to dispatcher
	disp.SetEnabledChecker(srv.IsPluginEnabled)

	// Sync profile from config to dispatcher and set up callback
	disp.SetProfile(srv.GetProfile())
	srv.SetProfileChangeFunc(disp.SetProfile)

	go func() {
		if err := srv.Start(); err != nil {
			fmt.Printf("⚠️ Web server failed: %v\n", err)
		}
	}()

	// 7. Initialize Worker pool
	work = worker.NewWorker(50, artifactStore, jobStore, cfg.RepoRoot)
	work.SetCheckoutDirs(cfg.ReposDir, cfg.CheckoutRoot)
	work.SetPubSub(ps) // Inject PubSub
	work.SetPluginResolver(resolvePlugin)

	// Set up status callback to report CI results to LGH
	work.SetStatusCallback(func(repo, commitSHA, plugin, status, summary string) {
		// POST to LGH status API
		go func() {
			body := map[string]string{
				"plugin":  plugin,
				"status":  status,
				"summary": summary,
			}
			jsonBody, _ := json.Marshal(body)
			url := fmt.Sprintf("%s/api/repos/%s/commits/%s/status", server.LghBaseURL(), repo, commitSHA)
			resp, err := http.Post(url, "application/json", bytes.NewReader(jsonBody))
			if err != nil {
				fmt.Printf("⚠️  Failed to report status to LGH: %v\n", err)
				return
			}
			_ = resp.Body.Close()
		}()
	})

	work.Start()
	defer work.Stop()

	// Recover non-terminal jobs from before this restart (ASSURANCE §7
	// 前置债: pending 恢复). Runs after Start so the queue is ready.
	recoverPendingJobs(work, jobStore, resolvePlugin)

	// Replay lgh events that arrived while ActionD was down (ASSURANCE §7
	// 前置债: 事件缺口重放). Idempotency in the worker makes overlap safe.
	replayMissedEvents(jobStore, disp, work, "", cfg.RepoRoot)

	srv.SetCancelFunc(work.Cancel)
	srv.SetRetryFunc(work.Retry)
	fmt.Println("✅ Worker started")

	fmt.Println()
	fmt.Println("👀 Listening for events... (Ctrl+C to stop)")
	fmt.Println("─────────────────────────────────────────────")

	// 8. Main event loop
	for {
		select {
		case evt, ok := <-source.Events():
			if !ok {
				return nil // Source closed
			}
			fmt.Printf("📨 Received: %s [%s]\n", evt.Type, evt.Repo)
			dispatchEvent(disp, work, evt, cfg.RepoRoot)
		case <-ctx.Done():
			return nil
		}
	}
}

// dispatchEvent flattens changed_files and submits matched plugins to the
// worker. Shared by the live event loop and the startup replay path so both
// behave identically.
func dispatchEvent(disp *dispatcher.Dispatcher, work *worker.Worker, evt event.Event, repoRoot string) {
	intent := worker.IntentForEvent(repoRoot, evt)
	// Extract changed_files from payload if present
	if evt.Type == event.TypeGitPush && evt.Payload != nil {
		if cf, ok := evt.Payload["changed_files"].(map[string]interface{}); ok {
			var files []string
			for _, v := range cf {
				if fileSlice, ok := v.([]interface{}); ok {
					for _, f := range fileSlice {
						if fs, ok := f.(string); ok {
							files = append(files, fs)
						}
					}
				}
			}
			evt.ChangedFiles = files
			if len(files) > 0 {
				fmt.Printf("   📁 Changed files: %d\n", len(files))
			}
		}
	}

	matched := disp.Dispatch(evt)
	if len(matched) == 0 {
		fmt.Println("   (no plugins matched)")
		return
	}

	for _, p := range matched {
		fmt.Printf("   → Dispatching to: %s\n", p.Name())
		jobID := work.Submit(worker.Task{
			Plugin:  p,
			Event:   evt,
			Profile: disp.Profile(),
			Intent:  intent,
		})
		fmt.Printf("     queued job: %s\n", jobID)
	}
}

func loadPlugins(deepWikiPath string) ([]plugin.Plugin, []plugin.Plugin, error) {
	pluginDirs := DetectPluginDirs()
	for _, dir := range pluginDirs {
		fmt.Printf("ℹ️  Plugin dir: %s\n", dir)
	}

	if len(pluginDirs) == 0 {
		fmt.Println("⚠️  No plugin directories found")
	}

	configManager := server.NewConfigManager()
	userConfig := configManager.GetAllPlugins()

	var catalog []plugin.Plugin
	seen := make(map[string]bool)

	// Build the full catalog first. Enabled filtering happens afterwards so the
	// web UI can still display disabled built-ins with full metadata.
	addPlugin := func(p plugin.Plugin) {
		name := p.Name()
		if seen[name] {
			return // Skip duplicates
		}

		if override, ok := userConfig[name]; ok {
			p = applyPluginOverride(p, override)
		}

		seen[name] = true
		catalog = append(catalog, p)
	}

	// 1. Add built-in Echo plugin (always first)
	catalog = append(catalog, &echo.EchoPlugin{})
	seen["echo"] = true

	// 2. Add deepwiki as built-in plugin (if path provided)
	if deepWikiPath != "" {
		deepwikiCfg := plugin.ExecPluginConfig{
			Name:       "deepwiki",
			Command:    "python3",
			Args:       []string{deepWikiPath + "/scripts/actiond_adapter.py"},
			Triggers:   []string{event.TypeGitTag},
			Timeout:    5 * time.Minute,
			WorkingDir: deepWikiPath,
		}
		addPlugin(plugin.NewExecPlugin(deepwikiCfg))
	}

	// 3. Discover plugins from directories using manifest.json
	fmt.Println("🔍 Scanning for plugins...")
	discovery := plugin.NewDiscovery(pluginDirs...)
	discoveredPlugins, err := discovery.ScanAll()
	if err != nil {
		fmt.Printf("⚠️  Plugin discovery error: %v\n", err)
	}

	for _, p := range discoveredPlugins {
		addPlugin(p)
	}

	// 4. Fallback: Register legacy hardcoded plugins if not found via manifest
	// (This ensures backward compatibility when manifest.json files are not present)
	if len(pluginDirs) > 0 {
		legacyPlugins := getLegacyPlugins(pluginDirs[0])
		for _, p := range legacyPlugins {
			if !seen[p.Name()] {
				addPlugin(p)
			}
		}
	}

	// 5. Load config-defined custom plugins into the runtime, not just the web UI.
	for name, cfg := range userConfig {
		if seen[name] || cfg.Command == "" {
			continue
		}
		customPlugin, err := pluginFromConfig(name, cfg)
		if err != nil {
			fmt.Printf("   ⚠️  Skipping custom plugin '%s': %v\n", name, err)
			continue
		}
		addPlugin(customPlugin)
	}

	active := make([]plugin.Plugin, 0, len(catalog))
	for _, p := range catalog {
		if override, ok := userConfig[p.Name()]; ok && override.Enabled != nil && !*override.Enabled {
			fmt.Printf("   ⛔ Plugin '%s' disabled by config\n", p.Name())
			continue
		}
		active = append(active, p)
	}

	return active, catalog, nil
}

func pluginFromConfig(name string, cfg server.PluginConfig) (plugin.Plugin, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	timeout := 5 * time.Minute
	if strings.TrimSpace(cfg.Timeout) != "" {
		parsed, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q", cfg.Timeout)
		}
		timeout = parsed
	}

	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir != "" && !filepath.IsAbs(workingDir) {
		absWorkingDir, err := filepath.Abs(workingDir)
		if err != nil {
			return nil, fmt.Errorf("invalid workingDir %q", cfg.WorkingDir)
		}
		workingDir = absWorkingDir
	}

	return plugin.NewExecPlugin(plugin.ExecPluginConfig{
		Name:       name,
		Command:    cfg.Command,
		Args:       cfg.Args,
		Triggers:   cfg.Triggers,
		Languages:  cfg.Languages,
		Timeout:    timeout,
		WorkingDir: workingDir,
		RefFilter:  cfg.RefFilter,
		RepoFilter: cfg.RepoFilter,
	}), nil
}

func applyPluginOverride(p plugin.Plugin, cfg server.PluginConfig) plugin.Plugin {
	execPlugin, ok := p.(*plugin.ExecPlugin)
	if !ok {
		return p
	}

	base := execPlugin.Config()
	if len(cfg.Triggers) > 0 {
		base.Triggers = cfg.Triggers
	}
	if len(cfg.Languages) > 0 {
		base.Languages = cfg.Languages
	}
	if strings.TrimSpace(cfg.Command) != "" {
		base.Command = cfg.Command
	}
	if len(cfg.Args) > 0 {
		base.Args = cfg.Args
	}
	if strings.TrimSpace(cfg.Timeout) != "" {
		if parsed, err := time.ParseDuration(cfg.Timeout); err == nil {
			base.Timeout = parsed
		}
	}
	if strings.TrimSpace(cfg.WorkingDir) != "" {
		base.WorkingDir = cfg.WorkingDir
	}
	if strings.TrimSpace(cfg.RefFilter) != "" {
		base.RefFilter = cfg.RefFilter
	}
	if strings.TrimSpace(cfg.RepoFilter) != "" {
		base.RepoFilter = cfg.RepoFilter
	}

	// Rebuild directly from the merged config. A pluginFromConfig roundtrip
	// here would drop manifest-derived fields (Version — verifier provenance).
	return plugin.NewExecPlugin(base)
}

// getLegacyPlugins returns hardcoded plugin configurations for backward compatibility
// These are only used if the plugin directory exists but has no manifest.json
func getLegacyPlugins(pluginsDir string) []plugin.Plugin {
	// Check if plugins directory has manifest files
	// If it does, don't use legacy configs
	hasManifest := false
	if err := filepath.Walk(pluginsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "manifest.json" {
			hasManifest = true
		}
		return nil
	}); err != nil {
		fmt.Printf("⚠️  Failed to scan legacy plugin dir %s: %v\n", pluginsDir, err)
	}

	if hasManifest {
		return nil // Let discovery handle it
	}

	// Return legacy hardcoded plugins
	return []plugin.Plugin{
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:       "go-lint",
			Command:    "python3",
			Args:       []string{filepath.Join(pluginsDir, "go-lint/run.py")},
			Triggers:   []string{event.TypeGitPush},
			Languages:  []string{"go"},
			Timeout:    5 * time.Minute,
			WorkingDir: filepath.Join(pluginsDir, "go-lint"),
		}),
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:       "go-test-fast",
			Command:    "python3",
			Args:       []string{filepath.Join(pluginsDir, "go-test-fast/run.py")},
			Triggers:   []string{event.TypeGitPush},
			Languages:  []string{"go"},
			Timeout:    2 * time.Minute,
			WorkingDir: filepath.Join(pluginsDir, "go-test-fast"),
		}),
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:       "go-build",
			Command:    "python3",
			Args:       []string{filepath.Join(pluginsDir, "go-build/run.py")},
			Triggers:   []string{event.TypeGitTag},
			Languages:  []string{"go"},
			Timeout:    10 * time.Minute,
			WorkingDir: filepath.Join(pluginsDir, "go-build"),
		}),
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:       "java-quicktest",
			Command:    "python3",
			Args:       []string{filepath.Join(pluginsDir, "java-quicktest/run.py")},
			Triggers:   []string{event.TypeGitPush},
			Languages:  []string{"java"},
			Timeout:    10 * time.Minute,
			WorkingDir: filepath.Join(pluginsDir, "java-quicktest"),
		}),
		plugin.NewExecPlugin(plugin.ExecPluginConfig{
			Name:       "java-checkstyle",
			Command:    "python3",
			Args:       []string{filepath.Join(pluginsDir, "java-checkstyle/run.py")},
			Triggers:   []string{event.TypeGitPush},
			Languages:  []string{"java"},
			Timeout:    3 * time.Minute,
			WorkingDir: filepath.Join(pluginsDir, "java-checkstyle"),
		}),
	}
}

// recoverPendingJobs re-queues non-terminal jobs from the last 24h after a
// restart. Jobs whose plugin is no longer loaded or whose event snapshot is
// missing are marked cancelled with an honest error summary — never silently
// left pending (ASSURANCE §7 前置债).
func recoverPendingJobs(work *worker.Worker, jobStore store.Store, resolve func(string) plugin.Plugin) {
	since := time.Now().Add(-24 * time.Hour)

	// Resolve history zombies honestly: non-terminal jobs that predate the
	// recovery window are marked cancelled instead of pending forever in
	// the UI (ASSURANCE §7). Independent of the recoverable list — runs
	// even when there is nothing to re-queue.
	abandoned, err := jobStore.AbandonStaleJobs(since)
	if err != nil {
		fmt.Printf("⚠️  Failed to abandon stale jobs: %v\n", err)
	} else if abandoned > 0 {
		fmt.Printf("🗑  Abandoned %d stale job(s) (before recovery window) as cancelled\n", abandoned)
	}

	jobs, err := jobStore.ListRecoverableJobs(since)
	if err != nil {
		fmt.Printf("⚠️  Failed to list recoverable jobs: %v\n", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	fmt.Printf("🔄 Recovering %d pending job(s) from before restart...\n", len(jobs))

	for _, j := range jobs {
		p := resolve(j.PluginName)
		if p == nil {
			j.Status = job.StatusCanceled
			j.ErrorSummary = "plugin not loaded after restart"
			_ = jobStore.UpdateJob(j)
			fmt.Printf("   ⛔ %s: plugin %s not loaded, marked cancelled\n", j.ID, j.PluginName)
			continue
		}
		var evt event.Event
		if j.EventJSON == "" || json.Unmarshal([]byte(j.EventJSON), &evt) != nil {
			j.Status = job.StatusCanceled
			j.ErrorSummary = "no recoverable event snapshot"
			_ = jobStore.UpdateJob(j)
			fmt.Printf("   ⛔ %s: no event snapshot, marked cancelled\n", j.ID)
			continue
		}
		if work.Requeue(j, worker.Task{Plugin: p, Event: evt, Profile: j.Profile}) {
			fmt.Printf("   ↩️  requeued %s (%s)\n", j.ID, j.PluginName)
		}
	}
}
