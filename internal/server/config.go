// Copyright (c) 2025 JoeGlenn1213
// ActionD Plugin Configuration Management

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const configPathEnvVar = "ACTIOND_CONFIG_PATH"

// PluginConfig represents a user-defined plugin configuration
type PluginConfig struct {
	Enabled    *bool    `json:"enabled,omitempty"`
	Type       string   `json:"type,omitempty"`       // "exec" or "builtin"
	Command    string   `json:"command,omitempty"`    // e.g. "python3"
	Args       []string `json:"args,omitempty"`       // e.g. ["run.py"]
	Triggers   []string `json:"triggers,omitempty"`   // e.g. ["git.push"]
	Languages  []string `json:"languages,omitempty"`  // e.g. ["python"], ["web"]
	Timeout    string   `json:"timeout,omitempty"`    // e.g. "5m"
	WorkingDir string   `json:"workingDir,omitempty"` // e.g. "plugins/my-plugin"
	RefFilter  string   `json:"refFilter,omitempty"`  // e.g. "refs/tags/*"
	RepoFilter string   `json:"repoFilter,omitempty"` // e.g. "demo-api.git"
}

// UserConfig represents the full config.json structure
type UserConfig struct {
	Plugins map[string]PluginConfig `json:"plugins"`
	Profile string                  `json:"profile,omitempty"` // Execution profile: "fast", "full", "release"
}

// ConfigManager handles reading/writing plugin configuration
type ConfigManager struct {
	path   string
	config UserConfig
	mu     sync.RWMutex
}

// DefaultConfigPath returns the default config.json location under ~/.localgithub.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".localgithub", "actions", "config.json")
	}
	return filepath.Join(home, ".localgithub", "actions", "config.json")
}

// ResolveConfigPath returns the active config path, allowing tests and tools to
// override the default location with ACTIOND_CONFIG_PATH.
func ResolveConfigPath() string {
	if path := strings.TrimSpace(os.Getenv(configPathEnvVar)); path != "" {
		return path
	}
	return DefaultConfigPath()
}

// NewConfigManager creates a new config manager.
func NewConfigManager() *ConfigManager {
	return NewConfigManagerWithPath("")
}

// NewConfigManagerWithPath creates a config manager for an explicit path. When
// path is empty, it falls back to ResolveConfigPath().
func NewConfigManagerWithPath(path string) *ConfigManager {
	if strings.TrimSpace(path) == "" {
		path = ResolveConfigPath()
	}

	cm := &ConfigManager{
		path: path,
		config: UserConfig{
			Plugins: make(map[string]PluginConfig),
		},
	}
	if err := cm.Load(); err != nil {
		fmt.Printf("warning: failed to load config %s: %v\n", path, err)
	}
	return cm
}

// Path returns the underlying config.json path.
func (cm *ConfigManager) Path() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.path
}

// Load reads config from disk
func (cm *ConfigManager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(cm.path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet, use empty config
			cm.config = UserConfig{Plugins: make(map[string]PluginConfig)}
			return nil
		}
		return err
	}

	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]PluginConfig)
	}
	cm.config = cfg
	return nil
}

// Save writes config to disk
func (cm *ConfigManager) Save() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Ensure directory exists
	dir := filepath.Dir(cm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cm.config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cm.path, data, 0644)
}

// GetPlugin returns a plugin config by name
func (cm *ConfigManager) GetPlugin(name string) (PluginConfig, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg, ok := cm.config.Plugins[name]
	return cfg, ok
}

// GetAllPlugins returns all plugin configs
func (cm *ConfigManager) GetAllPlugins() map[string]PluginConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy
	result := make(map[string]PluginConfig)
	for k, v := range cm.config.Plugins {
		result[k] = v
	}
	return result
}

// AddPlugin adds or updates a plugin configuration
func (cm *ConfigManager) AddPlugin(name string, cfg PluginConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if name == "" {
		return fmt.Errorf("plugin name is required")
	}

	cm.config.Plugins[name] = cfg
	return nil
}

// UpdatePlugin updates an existing plugin configuration
func (cm *ConfigManager) UpdatePlugin(name string, cfg PluginConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.config.Plugins[name]; !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	cm.config.Plugins[name] = cfg
	return nil
}

// DeletePlugin removes a plugin configuration
func (cm *ConfigManager) DeletePlugin(name string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.config.Plugins[name]; !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	delete(cm.config.Plugins, name)
	return nil
}

// SetPluginEnabled enables or disables a plugin
func (cm *ConfigManager) SetPluginEnabled(name string, enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cfg, exists := cm.config.Plugins[name]
	if !exists {
		// Create minimal config just for enabled state
		cfg = PluginConfig{}
	}
	cfg.Enabled = &enabled
	cm.config.Plugins[name] = cfg
	return nil
}

// IsPluginEnabled checks if a plugin is enabled (defaults to true if not set)
func (cm *ConfigManager) IsPluginEnabled(name string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cfg, exists := cm.config.Plugins[name]
	if !exists || cfg.Enabled == nil {
		return true // Default enabled
	}
	return *cfg.Enabled
}

// IsCustomPlugin reports whether a config entry defines a standalone custom plugin
// instead of only overriding metadata such as enabled state for a built-in plugin.
func IsCustomPlugin(cfg PluginConfig) bool {
	return cfg.Type != "" ||
		cfg.Command != "" ||
		len(cfg.Args) > 0 ||
		len(cfg.Triggers) > 0 ||
		len(cfg.Languages) > 0 ||
		cfg.Timeout != "" ||
		cfg.WorkingDir != "" ||
		cfg.RefFilter != "" ||
		cfg.RepoFilter != ""
}

// GetProfile returns the active execution profile. Defaults to "fast".
func (cm *ConfigManager) GetProfile() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.config.Profile == "" {
		return "fast"
	}
	return cm.config.Profile
}

// SetProfile updates the active execution profile.
func (cm *ConfigManager) SetProfile(profile string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.config.Profile = profile
	return nil
}
