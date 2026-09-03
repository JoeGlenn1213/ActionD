// Copyright (c) 2025 JoeGlenn1213
// ActionD - Local AI Action Execution Engine
// Licensed under MIT

package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/app"
	"github.com/JoeGlenn1213/actiond/internal/mcp"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/server"
	"github.com/JoeGlenn1213/actiond/internal/version"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

var (
	// Version reads the single source of truth in internal/version.
	// The Makefile extracts the literal from there for -ldflags -X main.Version.
	Version   = version.Version
	BuildDate = "dev"
	GitCommit = "unknown"
)

var (
	repoRoot     string
	reposDir     string
	checkoutRoot string
	deepWikiPath string
	webDir       string
	daemonMode   bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "actiond",
		Short: "Local AI Action Execution Engine",
		Long: `ActionD is a local CI/CD engine for AI agents, integrating with LGH.

It listens to Git events from LGH and automatically triggers plugin actions
like linting, testing, building, and AI-powered documentation generation.

FEATURES:
  • Dynamic Plugin Discovery - Auto-discover plugins via manifest.json
  • MCP Integration - Built-in MCP server for AI assistants
  • Event-Driven - Responds to git.push, git.tag events
  • Web Console - Real-time monitoring at http://localhost:3000
  • Hot Reload - Reload plugins without restart

QUICK START:
  $ lgh serve -d           # Start LGH server (required)
  $ actiond start -d       # Start ActionD in background
  $ actiond status         # Check if running
  $ actiond log            # View server logs

PLUGIN MANAGEMENT:
  $ actiond mcp            # Start MCP server for AI
  $ curl -X POST localhost:3000/api/plugins/reload  # Hot reload

BUILT-IN PLUGINS:
  • go-lint, go-test-fast, go-build (Go)
  • java-quicktest, java-checkstyle (Java)
  • python-pytest (Python)
  • deepwiki (AI documentation)

For more information, visit: https://github.com/JoeGlenn1213/ActionD`,
		Run: func(cmd *cobra.Command, args []string) {
			// If no subcommand is provided, default to showing help
			if err := cmd.Help(); err != nil {
				fmt.Printf("failed to render help: %v\n", err)
			}
		},
	}

	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newStopCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newRestartCmd())
	rootCmd.AddCommand(newPluginsCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newLogCmd())
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newMCPCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newWaitCmd())
	rootCmd.AddCommand(newApproveCmd())
	rootCmd.AddCommand(newCleanupCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "start",
		Aliases: []string{"serve"},
		Short:   "Start the ActionD server",
		Run: func(cmd *cobra.Command, args []string) {
			pidFile := getPidFilePath()

			// Check if already running
			if isRunning(pidFile) {
				pid := readPid(pidFile)
				if pid != os.Getpid() {
					fmt.Printf("ActionD is already running (PID %d)\n", pid)
					return
				}
				// If pid == os.Getpid(), it means parent process registered us (daemon mode). Continue.
			}

			if daemonMode {
				// Daemon mode: spawn child process
				exe, err := os.Executable()
				if err != nil {
					fmt.Printf("❌ Failed to get executable: %v\n", err)
					os.Exit(1)
				}

				// Re-run command without -d flag
				newArgs := []string{"start"}
				// Pass through other flags
				if repoRoot != "" {
					newArgs = append(newArgs, "--repo-root", repoRoot)
				}
				if reposDir != "" {
					newArgs = append(newArgs, "--repos-dir", reposDir)
				}
				if checkoutRoot != "" {
					newArgs = append(newArgs, "--checkout-root", checkoutRoot)
				}
				if deepWikiPath != "" {
					newArgs = append(newArgs, "--deepwiki-path", deepWikiPath)
				}
				if webDir != "" {
					newArgs = append(newArgs, "--web-dir", webDir)
				}

				// Important: Don't pass -d again!

				cmd := exec.Command(exe, newArgs...)
				cmd.SysProcAttr = actiondSysProcAttr()

				// Redirect stdout/stderr to log file
				logFile, err := os.OpenFile(getLogFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err == nil {
					cmd.Stdout = logFile
					cmd.Stderr = logFile
				}

				if err := cmd.Start(); err != nil {
					fmt.Printf("❌ Failed to start daemon: %v\n", err)
					os.Exit(1)
				}

				fmt.Printf("🚀 ActionD started in background (PID %d)\n", cmd.Process.Pid)
				writePid(pidFile, cmd.Process.Pid)
				return
			}

			// Foreground mode
			// Write PID file
			writePid(pidFile, os.Getpid())
			defer removePath(pidFile)

			fmt.Println("🚀 ActionD - Local AI Action Execution Engine")
			fmt.Printf("   Version: %s\n", Version)

			// Rotate log if needed before starting
			logPath := getLogFilePath()
			rotateLogIfNeeded(logPath)

			err := app.Run(app.Config{
				RepoRoot:     repoRoot,
				ReposDir:     reposDir,
				CheckoutRoot: checkoutRoot,
				DeepWikiPath: deepWikiPath,
				WebDir:       webDir,
			})
			if err != nil {
				fmt.Printf("❌ Application error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&repoRoot, "repo-root", "", "Root directory where repositories are located")
	cmd.Flags().StringVar(&reposDir, "repos-dir", "", "LGH bare repository root for isolated checkouts (default: ~/.localgithub/repos)")
	cmd.Flags().StringVar(&checkoutRoot, "checkout-root", "", "Root for isolated checkouts (default: ~/.localgithub/checkouts)")
	cmd.Flags().StringVar(&deepWikiPath, "deepwiki-path", "", "Optional path to DeepWiki MCP directory")
	cmd.Flags().StringVar(&webDir, "web-dir", "", "Optional path to exported ActionD-Web assets (auto-detected when omitted)")
	cmd.Flags().BoolVarP(&daemonMode, "daemon", "d", false, "Run in background")

	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the ActionD server",
		Run: func(cmd *cobra.Command, args []string) {
			pidFile := getPidFilePath()
			pid := readPid(pidFile)
			if pid == 0 {
				fmt.Println("ActionD is not running (PID file not found)")
				return
			}

			// Check if process exists
			process, err := os.FindProcess(pid)
			if err != nil {
				fmt.Println("ActionD process not found")
				cleanPidFile(pidFile)
				return
			}

			// Check if process is actually running
			if err := process.Signal(syscall.Signal(0)); err != nil {
				fmt.Println("ActionD is not running (process dead)")
				cleanPidFile(pidFile)
				return
			}

			// Send SIGTERM
			fmt.Printf("Stopping ActionD (PID %d)...\n", pid)
			if err := process.Signal(syscall.SIGTERM); err != nil {
				fmt.Printf("Failed to stop process: %v\n", err)
				return
			}

			// Wait for process to exit (with timeout)
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				if err := process.Signal(syscall.Signal(0)); err != nil {
					// Process has exited
					cleanPidFile(pidFile)
					fmt.Println("✅ Stopped.")
					return
				}
			}

			// Force kill if still running
			fmt.Println("Process didn't stop gracefully, force killing...")
			if err := process.Kill(); err != nil {
				fmt.Printf("Failed to kill process: %v\n", err)
			}
			cleanPidFile(pidFile)
			fmt.Println("✅ Force stopped.")
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check ActionD status",
		Long: `Check ActionD server status and display configuration information.

Shows:
  • Server running state and PID
  • Directory paths (data, plugins, web assets)
  • Connection status to LGH
  • Web assets availability`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("📊 ActionD Status")
			fmt.Println("═══════════════════════════════════════════")

			// Server status
			pidFile := getPidFilePath()
			if isRunning(pidFile) {
				pid := readPid(pidFile)
				fmt.Printf("  Status:  ✅ Running (PID %d)\n", pid)
			} else {
				fmt.Println("  Status:  ⏹️  Stopped")
			}

			// Version
			fmt.Printf("  Version: %s\n", Version)

			fmt.Println()
			fmt.Println("📁 Directories")
			fmt.Println("───────────────────────────────────────────")

			// Show directories from DefaultDirs
			dirs := app.DefaultDirs()
			for name, path := range dirs {
				if name == "base" {
					continue
				}
				var status string
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					status = "✅"
				} else {
					status = "❌"
				}
				fmt.Printf("  %s %-12s %s\n", status, name, path)
			}

			fmt.Println()
			fmt.Println("🔗 Connections")
			fmt.Println("───────────────────────────────────────────")

			// LGH connection
			sockPath := app.DetectLGHSocketPath()
			if sockPath != "" {
				conn, err := net.Dial("unix", sockPath)
				if err == nil {
					_ = conn.Close()
					fmt.Printf("  ✅ LGH          %s\n", sockPath)
				} else {
					fmt.Printf("  ⚠️  LGH Socket exists but cannot connect\n")
				}
			} else {
				fmt.Println("  ❌ LGH          Not running (run 'lgh serve -d')")
			}

			// Web assets
			webDir := app.DetectDefaultWebDir()
			if webDir != "" {
				fmt.Printf("  ✅ Web Assets   %s\n", webDir)
			} else {
				fmt.Println("  ⚠️  Web Assets   Not found")
			}

			// Plugin dirs
			pluginDirs := app.DetectPluginDirs()
			if len(pluginDirs) > 0 {
				fmt.Printf("  ✅ Plugins      %d directory(s)\n", len(pluginDirs))
			} else {
				fmt.Println("  ⚠️  Plugins      No directories found")
			}

			fmt.Println()
			fmt.Println("🌐 Network")
			fmt.Println("───────────────────────────────────────────")
			fmt.Println("  • API Server:   http://localhost:3000")
			fmt.Println("  • Web Console:  http://localhost:3000")
		},
	}
}

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the ActionD server",
		Long: `Restart the ActionD server.

This is equivalent to running 'actiond stop' followed by 'actiond start -d'.
The server will be restarted in daemon mode.`,
		Run: func(cmd *cobra.Command, args []string) {
			pidFile := getPidFilePath()

			// Stop if running
			if isRunning(pidFile) {
				pid := readPid(pidFile)
				fmt.Printf("Stopping ActionD (PID %d)...\n", pid)

				process, err := os.FindProcess(pid)
				if err == nil {
					_ = process.Signal(syscall.SIGTERM)
					// Wait for exit
					for i := 0; i < 10; i++ {
						time.Sleep(500 * time.Millisecond)
						if err := process.Signal(syscall.Signal(0)); err != nil {
							break
						}
					}
				}
				cleanPidFile(pidFile)
				fmt.Println("✅ Stopped.")
			} else {
				fmt.Println("ActionD is not running")
			}

			// Start in daemon mode
			fmt.Println("Starting ActionD...")

			exe, err := os.Executable()
			if err != nil {
				fmt.Printf("❌ Failed to get executable: %v\n", err)
				os.Exit(1)
			}

			newArgs := []string{"start", "-d"}
			if repoRoot != "" {
				newArgs = append(newArgs, "--repo-root", repoRoot)
			}
			if reposDir != "" {
				newArgs = append(newArgs, "--repos-dir", reposDir)
			}
			if checkoutRoot != "" {
				newArgs = append(newArgs, "--checkout-root", checkoutRoot)
			}
			if deepWikiPath != "" {
				newArgs = append(newArgs, "--deepwiki-path", deepWikiPath)
			}
			if webDir != "" {
				newArgs = append(newArgs, "--web-dir", webDir)
			}

			startCmd := exec.Command(exe, newArgs...)
			startCmd.SysProcAttr = actiondSysProcAttr()

			logFile, err := os.OpenFile(getLogFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				startCmd.Stdout = logFile
				startCmd.Stderr = logFile
			}

			if err := startCmd.Start(); err != nil {
				fmt.Printf("❌ Failed to start: %v\n", err)
				os.Exit(1)
			}

			writePid(pidFile, startCmd.Process.Pid)
			fmt.Printf("✅ ActionD restarted (PID %d)\n", startCmd.Process.Pid)
		},
	}

	cmd.Flags().StringVar(&repoRoot, "repo-root", "", "Root directory where repositories are located")
	cmd.Flags().StringVar(&reposDir, "repos-dir", "", "LGH bare repository root for isolated checkouts (default: ~/.localgithub/repos)")
	cmd.Flags().StringVar(&checkoutRoot, "checkout-root", "", "Root for isolated checkouts (default: ~/.localgithub/checkouts)")
	cmd.Flags().StringVar(&deepWikiPath, "deepwiki-path", "", "Optional path to DeepWiki MCP directory")
	cmd.Flags().StringVar(&webDir, "web-dir", "", "Optional path to exported ActionD-Web assets")

	return cmd
}

var coreGoPlugins = []string{"go-lint", "go-test-fast", "go-build"}
var builtinPluginNames = []string{
	"echo",
	"deepwiki",
	"go-lint",
	"go-test-fast",
	"go-build",
	"java-quicktest",
	"java-checkstyle",
}

func knownPluginNames() (map[string]struct{}, error) {
	names := make(map[string]struct{})
	for _, name := range builtinPluginNames {
		names[name] = struct{}{}
	}

	for _, dir := range app.DetectPluginDirs() {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || info.Name() != "manifest.json" {
				return nil
			}

			manifest, err := plugin.ParseManifest(path)
			if err != nil {
				return nil
			}
			if err := manifest.Validate(); err != nil {
				return nil
			}
			names[manifest.Name] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	for name, cfg := range server.NewConfigManager().GetAllPlugins() {
		if server.IsCustomPlugin(cfg) {
			names[name] = struct{}{}
		}
	}

	return names, nil
}

func validatePluginNames(names []string) error {
	known, err := knownPluginNames()
	if err != nil {
		return err
	}

	var unknown []string
	for _, name := range names {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	sort.Strings(unknown)
	return fmt.Errorf("unknown plugin(s): %s", strings.Join(unknown, ", "))
}

func setPluginsEnabled(names []string, enabled bool) (string, error) {
	cm := server.NewConfigManager()
	if err := validatePluginNames(names); err != nil {
		return cm.Path(), err
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return cm.Path(), fmt.Errorf("plugin name is required")
		}
		if err := cm.SetPluginEnabled(name, enabled); err != nil {
			return cm.Path(), err
		}
	}
	if err := cm.Save(); err != nil {
		return cm.Path(), err
	}
	return cm.Path(), nil
}

func disabledPlugins(cm *server.ConfigManager, names []string) []string {
	var disabled []string
	for _, name := range names {
		if !cm.IsPluginEnabled(name) {
			disabled = append(disabled, name)
		}
	}
	return disabled
}

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage plugin configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "enable [plugin...]",
		Short: "Enable one or more plugins in config.json",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path, err := setPluginsEnabled(args, true)
			if err != nil {
				fmt.Printf("Failed to enable plugins: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Enabled %d plugin(s) in %s\n", len(args), path)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "disable [plugin...]",
		Short: "Disable one or more plugins in config.json",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path, err := setPluginsEnabled(args, false)
			if err != nil {
				fmt.Printf("Failed to disable plugins: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Disabled %d plugin(s) in %s\n", len(args), path)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restore-go",
		Short: "Enable the core Go validation plugins",
		Run: func(cmd *cobra.Command, args []string) {
			path, err := setPluginsEnabled(coreGoPlugins, true)
			if err != nil {
				fmt.Printf("Failed to restore Go plugins: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Enabled core Go plugins in %s: %s\n", path, strings.Join(coreGoPlugins, ", "))
		},
	})

	cmd.AddCommand(newPluginCreateCmd())

	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all available plugins",
		Long: `List all available plugins grouped by language.

Plugins are grouped into categories:
  • go         - Go language plugins
  • python     - Python language plugins
  • java       - Java language plugins
  • web        - Web/TypeScript/JavaScript plugins
  • ci-cd      - CI/CD and deployment plugins
  • security   - Security and compliance plugins
  • utility    - Other utilities

To enable/disable plugins, use:
  $ actiond plugins enable <plugin>
  $ actiond plugins disable <plugin>`,
		Run: func(cmd *cobra.Command, args []string) {
			configManager := server.NewConfigManager()

			type pluginInfo struct {
				name      string
				version   string
				enabled   bool
				languages []string
				triggers  []string
				builtin   bool
			}

			var allPlugins []pluginInfo
			seen := make(map[string]bool)

			// Scan all plugin directories
			for _, dir := range app.DetectPluginDirs() {
				_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() || info.Name() != "manifest.json" {
						return nil
					}

					manifest, err := plugin.ParseManifest(path)
					if err != nil {
						return nil
					}
					if err := manifest.Validate(); err != nil {
						return nil
					}

					// Skip deepwiki
					if manifest.Name == "deepwiki" {
						return nil
					}

					allPlugins = append(allPlugins, pluginInfo{
						name:      manifest.Name,
						version:   manifest.Version,
						enabled:   configManager.IsPluginEnabled(manifest.Name),
						languages: manifest.Languages,
						triggers:  manifest.Triggers,
						builtin:   true,
					})
					seen[manifest.Name] = true
					return nil
				})
			}

			// Categorize plugins by language
			type categorySet map[string]bool
			categories := map[string][]pluginInfo{
				"Go":       {},
				"Python":   {},
				"Java":     {},
				"Web":      {},
				"CI/CD":    {},
				"Security": {},
				"Utility":  {},
			}
			categorySeen := map[string]categorySet{
				"Go":       {},
				"Python":   {},
				"Java":     {},
				"Web":      {},
				"CI/CD":    {},
				"Security": {},
				"Utility":  {},
			}

			for _, p := range allPlugins {
				categorized := false
				addedCategories := make(map[string]bool)

				for _, lang := range p.languages {
					switch strings.ToLower(lang) {
					case "go":
						if !categorySeen["Go"][p.name] {
							categories["Go"] = append(categories["Go"], p)
							categorySeen["Go"][p.name] = true
						}
						addedCategories["Go"] = true
						categorized = true
					case "python":
						if !categorySeen["Python"][p.name] {
							categories["Python"] = append(categories["Python"], p)
							categorySeen["Python"][p.name] = true
						}
						addedCategories["Python"] = true
						categorized = true
					case "java":
						if !categorySeen["Java"][p.name] {
							categories["Java"] = append(categories["Java"], p)
							categorySeen["Java"][p.name] = true
						}
						addedCategories["Java"] = true
						categorized = true
					case "typescript", "javascript", "web", "node", "nextjs":
						if !categorySeen["Web"][p.name] {
							categories["Web"] = append(categories["Web"], p)
							categorySeen["Web"][p.name] = true
						}
						addedCategories["Web"] = true
						categorized = true
					}
				}

				// Categorize by name patterns
				name := strings.ToLower(p.name)
				if strings.Contains(name, "security") || strings.Contains(name, "audit") {
					if !categorySeen["Security"][p.name] {
						categories["Security"] = append(categories["Security"], p)
						categorySeen["Security"][p.name] = true
					}
					categorized = true
				}
				if strings.Contains(name, "deploy") || strings.Contains(name, "release") || strings.Contains(name, "container") {
					if !categorySeen["CI/CD"][p.name] {
						categories["CI/CD"] = append(categories["CI/CD"], p)
						categorySeen["CI/CD"][p.name] = true
					}
					categorized = true
				}

				if !categorized && len(p.languages) == 0 || p.languages[0] == "*" {
					if !categorySeen["Utility"][p.name] {
						categories["Utility"] = append(categories["Utility"], p)
						categorySeen["Utility"][p.name] = true
					}
				}
			}

			// Print header
			fmt.Println("📦 Available Plugins")
			fmt.Println("═══════════════════════════════════════════════════════════")
			fmt.Println()

			total := 0
			for category, plugins := range categories {
				if len(plugins) == 0 {
					continue
				}
				total += len(plugins)
				fmt.Printf("━━━ %s ━━━\n", category)
				fmt.Printf("%-20s %-10s %-8s %s\n", "NAME", "VERSION", "STATUS", "DESCRIPTION")
				fmt.Println(strings.Repeat("─", 60))

				sort.Slice(plugins, func(i, j int) bool {
					return plugins[i].name < plugins[j].name
				})

				for _, p := range plugins {
					status := "✅"
					if !p.enabled {
						status = "⏸️"
					}
					trigs := ""
					if len(p.triggers) > 0 {
						trigs = p.triggers[0]
					}
					fmt.Printf("%-20s %-10s %-8s %s\n", p.name, p.version, status, trigs)
				}
				fmt.Println()
			}

			fmt.Printf("Total: %d plugins\n", total)
			fmt.Println("Legend: ✅ enabled ⏸️ disabled")
			fmt.Println("Use: actiond plugins enable/disable <name>")
		},
	}
}

// CheckLevel represents severity level for doctor checks
type CheckLevel int

const (
	CheckFatal   CheckLevel = iota // Critical - system won't work
	CheckWarning                   // Warning - may affect some features
	CheckInfo                      // Info - just for information
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system dependencies and configuration",
		Long: `Check the system environment, dependencies, and ActionD configuration.

Checks are categorized by severity:
  • FATAL   - Critical issues that prevent ActionD from working
  • WARNING - Issues that may affect some features
  • INFO    - Informational checks

Exit codes:
  0 - All checks passed
  1 - Fatal issues found
  2 - Only warnings found`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("🏥 ActionD Doctor")
			fmt.Printf("   Version: %s\n", Version)
			fmt.Println("═══════════════════════════════════════════════════════════")
			fmt.Println()

			var fatals, warnings int
			configManager := server.NewConfigManager()

			// Check function with level support
			check := func(category, name string, level CheckLevel, fn func() (string, error)) {
				detail, err := fn()

				if err != nil {
					switch level {
					case CheckFatal:
						fatals++
						fmt.Printf("  ❌ %-24s FATAL   %s\n", name, err)
					case CheckWarning:
						warnings++
						fmt.Printf("  ⚠️  %-24s WARN    %s\n", name, err)
					case CheckInfo:
						fmt.Printf("  ℹ️  %-24s INFO    %s\n", name, err)
					}
				} else {
					fmt.Printf("  ✅ %-24s OK      %s\n", name, detail)
				}
			}

			// === Section 1: System Environment ===
			fmt.Println("📦 System Environment")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("env", "OS/Arch", CheckInfo, func() (string, error) {
				return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH), nil
			})

			check("env", "Number of CPUs", CheckInfo, func() (string, error) {
				return fmt.Sprintf("%d", runtime.NumCPU()), nil
			})

			check("env", "Memory Available", CheckInfo, func() (string, error) {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				// Convert to MB
				sysMem := float64(m.Sys) / 1024 / 1024
				return fmt.Sprintf("%.2f MB", sysMem), nil
			})

			check("env", "Home Directory", CheckFatal, func() (string, error) {
				home, err := os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("cannot determine home directory: %v", err)
				}
				return home, nil
			})

			check("env", "Base Directory", CheckFatal, func() (string, error) {
				home, _ := os.UserHomeDir()
				base := filepath.Join(home, ".localgithub")
				if err := os.MkdirAll(base, 0755); err != nil {
					return "", fmt.Errorf("cannot create %s: %v", base, err)
				}
				return base, nil
			})

			fmt.Println()

			// === Section 2: Dependencies ===
			fmt.Println("🔧 Dependencies")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("dep", "Git", CheckFatal, func() (string, error) {
				path, err := exec.LookPath("git")
				if err != nil {
					return "", fmt.Errorf("not found (required)")
				}
				out, _ := exec.Command(path, "--version").Output()
				return fmt.Sprintf("%s (%s)", strings.TrimSpace(string(out)), path), nil
			})

			check("dep", "Python 3", CheckFatal, func() (string, error) {
				path, err := exec.LookPath("python3")
				if err != nil {
					return "", fmt.Errorf("not found (required for plugins)")
				}
				out, _ := exec.Command(path, "--version").Output()
				return fmt.Sprintf("%s (%s)", strings.TrimSpace(string(out)), path), nil
			})

			check("dep", "Python pip", CheckWarning, func() (string, error) {
				path, err := exec.LookPath("pip3")
				if err != nil {
					path, err = exec.LookPath("pip")
					if err != nil {
						return "", fmt.Errorf("not found (needed for python plugins)")
					}
				}
				out, _ := exec.Command(path, "--version").Output()
				firstLine := strings.Split(strings.TrimSpace(string(out)), " ")[1]
				return fmt.Sprintf("pip %s (%s)", firstLine, path), nil
			})

			check("dep", "Go", CheckWarning, func() (string, error) {
				path, err := exec.LookPath("go")
				if err != nil {
					return "", fmt.Errorf("not found (needed for Go plugins)")
				}
				out, _ := exec.Command(path, "version").Output()
				return fmt.Sprintf("%s (%s)", strings.TrimSpace(string(out)), path), nil
			})

			check("dep", "Node.js", CheckWarning, func() (string, error) {
				path, err := exec.LookPath("node")
				if err != nil {
					return "", fmt.Errorf("not found (needed for web plugins)")
				}
				out, _ := exec.Command(path, "--version").Output()
				ver := strings.TrimSpace(string(out))
				return fmt.Sprintf("%s (%s)", ver, path), nil
			})

			check("dep", "golangci-lint", CheckWarning, func() (string, error) {
				path, err := exec.LookPath("golangci-lint")
				if err != nil {
					return "", fmt.Errorf("not found (needed by go-lint plugin)")
				}
				out, _ := exec.Command(path, "version").Output()
				firstLine := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
				return fmt.Sprintf("%s (%s)", firstLine, path), nil
			})

			fmt.Println()

			// === Section 3: Services & Networking ===
			fmt.Println("🔌 Services & Networking")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("svc", "LGH Server", CheckFatal, func() (string, error) {
				// Check socket
				sockPath := app.DetectLGHSocketPath()
				if sockPath == "" {
					return "", fmt.Errorf("socket not found - run 'lgh serve -d'")
				}
				// Try to connect
				conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
				if err != nil {
					return "", fmt.Errorf("socket exists but cannot connect: %v", err)
				}
				_ = conn.Close()
				return fmt.Sprintf("running (%s)", sockPath), nil
			})

			check("svc", "ActionD Server", CheckInfo, func() (string, error) {
				pidFile := getPidFilePath()
				if !isRunning(pidFile) {
					return "stopped", nil
				}
				pid := readPid(pidFile)
				return fmt.Sprintf("running (PID %d)", pid), nil
			})

			check("net", "Port 3000 (Web/API)", CheckWarning, func() (string, error) {
				ln, err := net.Listen("tcp", ":3000")
				if err != nil {
					// Port is in use - check if it's ActionD
					if isRunning(getPidFilePath()) {
						return "in use by ActionD", nil
					}
					return "", fmt.Errorf("in use by another process")
				}
				_ = ln.Close()
				return "available", nil
			})

			check("net", "Port 8080 (LGH)", CheckInfo, func() (string, error) {
				ln, err := net.Listen("tcp", ":8080")
				if err != nil {
					return "in use (likely LGH)", nil
				}
				_ = ln.Close()
				return "available", nil
			})

			fmt.Println()

			// === Section 4: Directories ===
			fmt.Println("📁 Directories")
			fmt.Println("──────────────────────────────────────────────────────────────")

			home, _ := os.UserHomeDir()
			dirs := app.DefaultDirs()

			for name, path := range dirs {
				if name == "base" {
					continue
				}
				level := CheckWarning
				if name == "actions" {
					level = CheckFatal
				}
				checkDirName := fmt.Sprintf("%s/", name)
				check("dir", checkDirName, level, func() (string, error) {
					info, err := os.Stat(path)
					if err != nil {
						if os.IsNotExist(err) {
							// Try to create
							if mkdirErr := os.MkdirAll(path, 0755); mkdirErr != nil {
								return "", fmt.Errorf("cannot create: %v", mkdirErr)
							}
							return fmt.Sprintf("created: %s", path), nil
						}
						return "", fmt.Errorf("cannot access: %v", err)
					}
					if !info.IsDir() {
						return "", fmt.Errorf("not a directory")
					}
					return path, nil
				})
			}

			fmt.Println()

			// === Section 5: Storage ===
			fmt.Println("💾 Storage")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("store", "Actions DB", CheckFatal, func() (string, error) {
				dbPath := filepath.Join(home, ".localgithub", "actions", "actiond.db")
				// Try to create/test write
				dir := filepath.Dir(dbPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					return "", fmt.Errorf("cannot create directory: %v", err)
				}
				// Test write
				testFile := filepath.Join(dir, ".write_test")
				f, err := os.Create(testFile)
				if err != nil {
					return "", fmt.Errorf("cannot write to directory: %v", err)
				}
				_ = f.Close()
				_ = os.Remove(testFile)
				return fmt.Sprintf("writable (%s)", dbPath), nil
			})

			check("store", "Plugin Config", CheckInfo, func() (string, error) {
				path := configManager.Path()
				if _, err := os.Stat(path); os.IsNotExist(err) {
					return fmt.Sprintf("%s (will be created)", path), nil
				}
				return path, nil
			})

			fmt.Println()

			// === Section 6: Plugins ===
			fmt.Println("🔌 Plugins")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("plugin", "Plugin Dirs", CheckWarning, func() (string, error) {
				dirs := app.DetectPluginDirs()
				if len(dirs) == 0 {
					return "", fmt.Errorf("no plugin directories found\n      ⚠️  Missing plugins directory. Run this command to fix:\n      mkdir -p ~/.localgithub/plugins")
				}
				return fmt.Sprintf("%d found", len(dirs)), nil
			})

			check("plugin", "Core Go Plugins", CheckWarning, func() (string, error) {
				disabled := disabledPlugins(configManager, coreGoPlugins)
				if len(disabled) > 0 {
					return "", fmt.Errorf("disabled: %s (run: actiond plugins restore-go)", strings.Join(disabled, ", "))
				}
				return fmt.Sprintf("%s enabled", strings.Join(coreGoPlugins, ", ")), nil
			})

			fmt.Println()

			// === Section 7: Web Assets ===
			fmt.Println("🌐 Web Assets")
			fmt.Println("──────────────────────────────────────────────────────────────")

			check("web", "Static Files", CheckWarning, func() (string, error) {
				dir := app.DetectDefaultWebDir()
				if dir == "" {
					return "", fmt.Errorf("not found - API works but UI unavailable")
				}
				return dir, nil
			})

			fmt.Println()

			// === Summary ===
			fmt.Println("═══════════════════════════════════════════════════════════")
			fmt.Println("📊 Summary")
			fmt.Println("──────────────────────────────────────────────────────────────")

			if fatals > 0 {
				fmt.Printf("  ❌ Fatal:   %d issue(s)\n", fatals)
				fmt.Println()
				fmt.Println("⚠️  ActionD cannot start. Please fix the fatal issues above.")
				os.Exit(1)
			} else if warnings > 0 {
				fmt.Printf("  ⚠️  Warnings: %d issue(s)\n", warnings)
				fmt.Println()
				fmt.Println("✅ ActionD can start, but some features may not work.")
				fmt.Println("   Review the warnings above for details.")
				os.Exit(2)
			} else {
				fmt.Println("  ✅ All checks passed!")
				fmt.Println()
				fmt.Println("✨ All systems go! ActionD is ready to run.")
			}
		},
	}

	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ActionD v%s (%s) built %s\n", Version, GitCommit, BuildDate)
		},
	}
}

var logLimit int
var logFollow bool
var logClean int
var logJob string
var logPlugin string

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "View and manage ActionD server logs",
		Long: `View and manage ActionD server runtime logs.

Shows recent log entries from the ActionD daemon. Use -f to follow logs in real-time.
Use --clean to remove old log backups.
Use --job <id> to filter logs by a specific job ID.
Use --plugin <name> to filter logs by a specific plugin name.`,
		Run: func(cmd *cobra.Command, args []string) {
			logFile := getLogFilePath()

			// Handle --clean flag
			if logClean > 0 {
				cleaned := cleanOldLogs(logFile, logClean)
				if cleaned > 0 {
					fmt.Printf("✅ Cleaned %.2f MB of old logs (older than %d days)\n", float64(cleaned)/(1024*1024), logClean)
				} else {
					fmt.Printf("No old logs to clean (retention: %d days)\n", logClean)
				}
				return
			}

			// Check if log file exists
			if _, err := os.Stat(logFile); os.IsNotExist(err) {
				fmt.Println("No log file found. ActionD may not have been started yet.")
				return
			}

			// Show log stats
			if info, err := os.Stat(logFile); err == nil {
				fmt.Printf("📄 Log file: %s (%.2f MB)\n\n", logFile, float64(info.Size())/(1024*1024))
			}

			if logFollow {
				// Follow mode: tail -f
				followLog(logFile)
			} else {
				// Show last N lines with optional filtering
				showLogTail(logFile, logLimit, logJob, logPlugin)
			}
		},
	}

	cmd.Flags().IntVarP(&logLimit, "limit", "n", 20, "Number of log entries to show")
	cmd.Flags().BoolVarP(&logFollow, "follow", "f", false, "Follow log output (tail -f)")
	cmd.Flags().IntVarP(&logClean, "clean", "c", 0, "Clean logs older than N days")
	cmd.Flags().StringVar(&logJob, "job", "", "Filter logs by job ID")
	cmd.Flags().StringVar(&logPlugin, "plugin", "", "Filter logs by plugin name")

	return cmd
}

func showLogTail(logFile string, limit int, jobID string, pluginName string) {
	file, err := os.Open(logFile)
	if err != nil {
		fmt.Printf("Failed to open log file: %v\n", err)
		return
	}
	defer closeWithLog(file, "log file")

	// Read lines into slice
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Apply filters if specified
		if jobID != "" && !strings.Contains(line, jobID) {
			continue
		}
		if pluginName != "" && !strings.Contains(line, pluginName) {
			continue
		}

		lines = append(lines, line)
	}

	// Show last N lines
	start := len(lines) - limit
	if start < 0 {
		start = 0
	}

	for i := start; i < len(lines); i++ {
		fmt.Println(lines[i])
	}
}

func followLog(logFile string) {
	// Use tail -f for simplicity
	cmd := exec.Command("tail", "-f", logFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("📋 Following %s (Ctrl+C to stop)...\n", logFile)
	fmt.Println("─────────────────────────────────────────────")

	if err := cmd.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

// Helpers

func getPidFilePath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".localgithub", "actions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("failed to create pid dir %s: %v\n", dir, err)
	}
	return filepath.Join(dir, "actiond.pid")
}

func getLogFilePath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".localgithub", "actions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("failed to create log dir %s: %v\n", dir, err)
	}
	return filepath.Join(dir, "actiond.log")
}

// Log rotation constants
const (
	maxLogSize    = 50 * 1024 * 1024 // 50MB
	maxLogBackups = 3                // Keep 3 backup files
)

// rotateLogIfNeeded checks log size and rotates if necessary
func rotateLogIfNeeded(logPath string) {
	info, err := os.Stat(logPath)
	if err != nil {
		return // File doesn't exist yet
	}

	if info.Size() < maxLogSize {
		return // No rotation needed
	}

	// Delete oldest backup
	oldestBackup := fmt.Sprintf("%s.%d", logPath, maxLogBackups)
	_ = os.Remove(oldestBackup)

	// Rotate existing backups
	for i := maxLogBackups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", logPath, i)
		newPath := fmt.Sprintf("%s.%d", logPath, i+1)
		_ = os.Rename(oldPath, newPath)
	}

	// Rotate current log
	_ = os.Rename(logPath, logPath+".1")
}

// cleanOldLogs removes logs older than specified days
func cleanOldLogs(logPath string, days int) int64 {
	var totalCleaned int64

	// Clean backup logs older than specified days
	cutoff := time.Now().AddDate(0, 0, -days)

	for i := 1; i <= maxLogBackups+1; i++ {
		backupPath := fmt.Sprintf("%s.%d", logPath, i)
		if info, err := os.Stat(backupPath); err == nil {
			if info.ModTime().Before(cutoff) {
				size := info.Size()
				_ = os.Remove(backupPath)
				totalCleaned += size
			}
		}
	}

	return totalCleaned
}

func writePid(path string, pid int) {
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644); err != nil {
		fmt.Printf("failed to write pid file %s: %v\n", path, err)
	}
}

func readPid(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

func cleanPidFile(path string) {
	removePath(path)
}

func isRunning(pidFile string) bool {
	pid := readPid(pidFile)
	if pid == 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Send signal 0 to check if process exists
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

// MCP command for Model Context Protocol integration
var mcpAddr string

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start ActionD MCP server for AI integration",
		Long: `Start the Model Context Protocol (MCP) server for ActionD.

This allows AI assistants like Claude to interact with ActionD:
- Query plugin status and configurations
- List and inspect CI/CD actions/jobs
- Trigger plugin reloads

The MCP server uses stdio transport and should be configured in your
AI assistant's MCP settings.

Note:
Set ACTIOND_MCP_ALLOW_LIFECYCLE=1 before running this command if you want
MCP tools to be able to start/stop/restart ActionD.`,
		Run: func(cmd *cobra.Command, args []string) {
			// Create HTTP client for ActionD API
			client := mcp.NewHTTPClient(mcpAddr)
			exe, _ := os.Executable()
			lifecycle := mcp.NewLocalLifecycleController(exe)
			lghClient := mcp.NewLocalLGHClient()

			// Create MCP server
			mcpServer := mcp.NewServer(client, lifecycle, lghClient)

			// Write startup message to stderr
			fmt.Fprintln(os.Stderr, "🚀 ActionD MCP Server started (stdio mode)")
			fmt.Fprintln(os.Stderr, "Ready to accept JSON-RPC requests...")

			// Start the server with stdio transport
			if err := mcpserver.ServeStdio(mcpServer); err != nil {
				fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&mcpAddr, "addr", "http://localhost:3000", "ActionD API server address")

	return cmd
}

func closeWithLog(closer interface{ Close() error }, label string) {
	if err := closer.Close(); err != nil {
		fmt.Printf("failed to close %s: %v\n", label, err)
	}
}

func removePath(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Printf("failed to remove %s: %v\n", path, err)
	}
}

func newApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <job_id>",
		Short: "Approve a pending action job (e.g. waiting at approval-gate)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			jobID := args[0]
			client := mcp.NewHTTPClient("http://localhost:3000")
			err := client.ApproveAction(jobID)
			if err != nil {
				fmt.Printf("❌ Failed to approve job %s: %v\n", jobID, err)
				os.Exit(1)
			}
			fmt.Printf("✅ Job %s successfully approved.\n", jobID)
		},
	}
	return cmd
}

var cleanupDays int
var cleanupAll bool

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete old completed/failed actions and their artifact directories",
		Long: `Delete terminal action jobs (done/failed/cancelled) older than --days
(default 7) together with their artifact directories under
~/.localgithub/actions, reclaiming disk space. Pending/running jobs are
never touched. Use --all to delete every terminal job regardless of age.

Requires a running ActionD server (talks to http://localhost:3000).`,
		Run: func(cmd *cobra.Command, args []string) {
			client := mcp.NewHTTPClient("http://localhost:3000")
			result, err := client.CleanupActions(cleanupDays, cleanupAll)
			if err != nil {
				fmt.Printf("❌ Cleanup failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ %s\n", result.Message)
			fmt.Printf("   jobs deleted: %d, artifact directories deleted: %d, retention: %d days\n",
				result.DeletedJobs, result.DeletedDirs, result.RetentionDays)
		},
	}

	cmd.Flags().IntVarP(&cleanupDays, "days", "d", 7, "Retention window in days (0 = all terminal jobs)")
	cmd.Flags().BoolVar(&cleanupAll, "all", false, "Delete all terminal jobs regardless of age")

	return cmd
}

var waitTimeout int

func newWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait <job_id>",
		Short: "Wait for a specific ActionD job to complete",
		Long: `Wait for a specific ActionD job to complete and return its final status.
This is useful for scripts and CI/CD pipelines that need to block until a job finishes.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			jobID := args[0]

			// We need an HTTP client to communicate with the running daemon
			// Using the same approach as MCP tools
			client := mcp.NewHTTPClient("http://localhost:3000")

			fmt.Printf("⏳ Waiting for job %s to complete (timeout: %ds)...\n", jobID, waitTimeout)

			deadline := time.Now().Add(time.Duration(waitTimeout) * time.Second)

			for time.Now().Before(deadline) {
				action, err := client.GetAction(jobID)
				if err != nil {
					fmt.Printf("❌ Error: Failed to get job status: %v\n", err)
					os.Exit(1)
				}

				if action.Status == "done" || action.Status == "failed" || action.Status == "error" || action.Status == "cancelled" {
					fmt.Printf("\n✨ Job completed with status: %s\n", action.Status)
					fmt.Printf("⏱️  Duration: %d ms\n", action.DurationMs)

					if action.Status != "done" {
						os.Exit(1)
					}
					os.Exit(0)
				}

				time.Sleep(1 * time.Second)
			}

			fmt.Printf("\n❌ Error: Timeout waiting for job %s after %d seconds\n", jobID, waitTimeout)
			os.Exit(1)
		},
	}

	cmd.Flags().IntVarP(&waitTimeout, "timeout", "t", 300, "Timeout in seconds")

	return cmd
}

// Setup command flags
var setupSkipDeps bool
var setupSkipWeb bool
var setupStart bool

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Initialize ActionD environment",
		Long: `Initialize ActionD environment with one command.

This command sets up:
  • Directory structure (~/.localgithub/*)
  • Dependency checks (Git, Python, Go, Node)
  • Plugin directories
  • Web assets detection
  • LGH connection verification

After running this command, you can start ActionD with 'actiond start -d'.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("🚀 ActionD Setup")
			fmt.Println("═══════════════════════════════════════════")
			fmt.Println()

			// Show system info
			info := app.SystemInfo()
			fmt.Printf("System: %s/%s\n", info["OS"], info["Arch"])
			fmt.Printf("Base:   %s\n", info["BaseDir"])
			fmt.Println()

			// Run setup
			result, err := app.RunSetup(app.SetupConfig{
				SkipDeps:    setupSkipDeps,
				SkipWeb:     setupSkipWeb,
				StartDaemon: false, // We'll handle start manually if flag is set
			})
			if err != nil {
				fmt.Printf("\n❌ Setup failed: %v\n", err)
				os.Exit(1)
			}

			// Summary
			fmt.Println()
			fmt.Println("═══════════════════════════════════════════")
			fmt.Println("📊 Setup Summary")
			fmt.Println("───────────────────────────────────────────")
			fmt.Printf("✅ Created %d directories\n", len(result.DirsCreated))
			if len(result.Warnings) > 0 {
				fmt.Printf("⚠️  Found %d warnings\n", len(result.Warnings))
			}
			if len(result.Errors) > 0 {
				fmt.Printf("❌ Found %d errors\n", len(result.Errors))
			}

			fmt.Println()
			if len(result.Errors) > 0 {
				fmt.Println("⚠️ Setup completed with errors. Please fix them before starting ActionD.")
			} else {
				fmt.Println("✨ Setup completed successfully!")
				fmt.Println("\nNext steps:")

				// Suggest LGH if not found
				hasLghWarning := false
				for _, w := range result.Warnings {
					if strings.Contains(w, "LGH") {
						hasLghWarning = true
						break
					}
				}

				if hasLghWarning {
					fmt.Println("  1. Initialize and start LGH:")
					fmt.Println("     $ lgh init")
					fmt.Println("     $ lgh serve -d")
					fmt.Println("  2. Start ActionD:")
					fmt.Println("     $ actiond start -d")
				} else {
					fmt.Println("  1. Start ActionD in background:")
					fmt.Println("     $ actiond start -d")
				}
				fmt.Println("  2. Check status:")
				fmt.Println("     $ actiond doctor")
				fmt.Println("  3. View dashboard:")
				fmt.Println("     $ open http://localhost:3000")
			}

			if setupStart && len(result.Errors) == 0 {
				fmt.Println("\nStarting ActionD in daemon mode...")
				// Execute actiond start -d
				exe, _ := os.Executable()
				startCmd := exec.Command(exe, "start", "-d")
				if err := startCmd.Run(); err != nil {
					fmt.Printf("❌ Failed to start ActionD: %v\n", err)
				} else {
					fmt.Println("✅ ActionD started successfully")
				}
			}
		},
	}

	cmd.Flags().BoolVar(&setupSkipDeps, "skip-deps", false, "Skip dependency checks")
	cmd.Flags().BoolVar(&setupSkipWeb, "skip-web", false, "Skip web assets detection")
	cmd.Flags().BoolVar(&setupStart, "start", false, "Start ActionD daemon after successful setup")

	return cmd
}
