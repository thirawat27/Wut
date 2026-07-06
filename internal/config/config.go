// Package config provides configuration management for WUT
package config

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the application
type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app"`
	Fuzzy    FuzzyConfig    `mapstructure:"fuzzy" yaml:"fuzzy"`
	UI       UIConfig       `mapstructure:"ui" yaml:"ui"`
	Database DatabaseConfig `mapstructure:"database" yaml:"database"`
	History  HistoryConfig  `mapstructure:"history" yaml:"history"`
	Context  ContextConfig  `mapstructure:"context" yaml:"context"`
	Shell    ShellConfig    `mapstructure:"shell" yaml:"shell"`
	Privacy  PrivacyConfig  `mapstructure:"privacy" yaml:"privacy"`
	Logging  LoggingConfig  `mapstructure:"logging" yaml:"logging"`
	TLDR     TLDRConfig     `mapstructure:"tldr" yaml:"tldr"`
}

// AppConfig holds application settings
type AppConfig struct {
	Name        string `mapstructure:"name" yaml:"name"`
	Version     string `mapstructure:"version" yaml:"version"`
	Debug       bool   `mapstructure:"debug" yaml:"debug"`
	Initialized bool   `mapstructure:"initialized" yaml:"initialized"`
}

// IsInitialized returns true when the user has completed `wut init`
func IsInitialized() bool {
	cfg := Get()
	return cfg.App.Initialized
}

// FuzzyConfig holds fuzzy matching settings
type FuzzyConfig struct {
	Enabled       bool    `mapstructure:"enabled" yaml:"enabled"`
	CaseSensitive bool    `mapstructure:"case_sensitive" yaml:"case_sensitive"`
	MaxDistance   int     `mapstructure:"max_distance" yaml:"max_distance"`
	Threshold     float64 `mapstructure:"threshold" yaml:"threshold"`
}

// UIConfig holds UI settings
type UIConfig struct {
	Theme              string            `mapstructure:"theme" yaml:"theme"`
	ShowConfidence     bool              `mapstructure:"show_confidence" yaml:"show_confidence"`
	ShowExplanations   bool              `mapstructure:"show_explanations" yaml:"show_explanations"`
	SyntaxHighlighting bool              `mapstructure:"syntax_highlighting" yaml:"syntax_highlighting"`
	Pagination         int               `mapstructure:"pagination" yaml:"pagination"`
	Colors             map[string]string `mapstructure:"colors" yaml:"colors"`
}

// DatabaseConfig holds database settings
type DatabaseConfig struct {
	Type           string `mapstructure:"type" yaml:"type"`
	Path           string `mapstructure:"path" yaml:"path"`
	MaxSize        int    `mapstructure:"max_size" yaml:"max_size"`
	BackupEnabled  bool   `mapstructure:"backup_enabled" yaml:"backup_enabled"`
	BackupInterval int    `mapstructure:"backup_interval" yaml:"backup_interval"`
}

// HistoryConfig holds history settings
type HistoryConfig struct {
	Enabled        bool `mapstructure:"enabled" yaml:"enabled"`
	MaxEntries     int  `mapstructure:"max_entries" yaml:"max_entries"`
	TrackFrequency bool `mapstructure:"track_frequency" yaml:"track_frequency"`
	TrackContext   bool `mapstructure:"track_context" yaml:"track_context"`
	TrackTiming    bool `mapstructure:"track_timing" yaml:"track_timing"`
}

// ContextConfig holds context analysis settings
type ContextConfig struct {
	Enabled           bool `mapstructure:"enabled" yaml:"enabled"`
	GitIntegration    bool `mapstructure:"git_integration" yaml:"git_integration"`
	ProjectDetection  bool `mapstructure:"project_detection" yaml:"project_detection"`
	EnvironmentVars   bool `mapstructure:"environment_vars" yaml:"environment_vars"`
	DirectoryAnalysis bool `mapstructure:"directory_analysis" yaml:"directory_analysis"`
}

// ShellConfig holds shell integration settings
type ShellConfig struct {
	Enabled bool            `mapstructure:"enabled" yaml:"enabled"`
	Hooks   map[string]bool `mapstructure:"hooks" yaml:"hooks"`
}

// PrivacyConfig holds privacy settings
type PrivacyConfig struct {
	LocalOnly         bool `mapstructure:"local_only" yaml:"local_only"`
	EncryptData       bool `mapstructure:"encrypt_data" yaml:"encrypt_data"`
	AnonymizeCommands bool `mapstructure:"anonymize_commands" yaml:"anonymize_commands"`
	ShareAnalytics    bool `mapstructure:"share_analytics" yaml:"share_analytics"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level      string `mapstructure:"level" yaml:"level"`
	File       string `mapstructure:"file" yaml:"file"`
	MaxSize    int    `mapstructure:"max_size" yaml:"max_size"`
	MaxBackups int    `mapstructure:"max_backups" yaml:"max_backups"`
	MaxAge     int    `mapstructure:"max_age" yaml:"max_age"`
}

// TLDRConfig holds TLDR pages settings
type TLDRConfig struct {
	Enabled          bool   `mapstructure:"enabled" yaml:"enabled"`
	AutoSync         bool   `mapstructure:"auto_sync" yaml:"auto_sync"`
	AutoSyncInterval int    `mapstructure:"auto_sync_interval" yaml:"auto_sync_interval"` // days
	OfflineMode      bool   `mapstructure:"offline_mode" yaml:"offline_mode"`
	AutoDetectOnline bool   `mapstructure:"auto_detect_online" yaml:"auto_detect_online"`
	MaxCacheAge      int    `mapstructure:"max_cache_age" yaml:"max_cache_age"` // days
	DefaultPlatform  string `mapstructure:"default_platform" yaml:"default_platform"`
}

var (
	// globalConfig holds the global configuration instance
	globalConfig *Config
	// configMu guards concurrent access to globalConfig
	configMu sync.RWMutex
	// configPath is the path to the config file
	configPath string
)

// Load loads the configuration from file and environment variables
func Load(path string) (*Config, error) {
	if path == "" {
		path = getDefaultConfigPath()
	}
	configPath = path

	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	// Set default values
	setDefaults()

	// Read environment variables
	viper.SetEnvPrefix("WUT")
	viper.AutomaticEnv()

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Try to read config file, if it doesn't exist, create default
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok || os.IsNotExist(err) {
			// Config file not found, create default
			if err := createDefaultConfig(path); err != nil {
				return nil, fmt.Errorf("failed to create default config: %w", err)
			}
			// Read the newly created config
			if err := viper.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("failed to read created config: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	// Unmarshal config
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Expand paths
	expandPaths(&cfg)

	configMu.Lock()
	globalConfig = &cfg
	configMu.Unlock()
	return &cfg, nil
}

// Get returns the global configuration instance
func Get() *Config {
	configMu.RLock()
	cfg := globalConfig
	configMu.RUnlock()

	if cfg == nil {
		// Load default config if not already loaded
		loaded, err := Load("")
		if err != nil {
			// Return default config on error
			return &Config{}
		}
		return loaded
	}
	return cfg
}

// Set updates the global configuration
func Set(cfg *Config) {
	configMu.Lock()
	globalConfig = cfg
	configMu.Unlock()
}

// Save saves the current configuration to file
func Save() error {
	configMu.RLock()
	cfg := globalConfig
	path := GetConfigPath()
	configMu.RUnlock()

	if cfg == nil {
		return fmt.Errorf("no configuration to save")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// setDefaults sets default configuration values
func setDefaults() {
	viper.SetDefault("app.name", "wut")
	viper.SetDefault("app.version", "1.0.0")
	viper.SetDefault("app.debug", false)
	viper.SetDefault("app.initialized", false)

	viper.SetDefault("fuzzy.enabled", true)
	viper.SetDefault("fuzzy.case_sensitive", false)
	viper.SetDefault("fuzzy.max_distance", 3)
	viper.SetDefault("fuzzy.threshold", 0.6)

	viper.SetDefault("ui.theme", "auto")
	viper.SetDefault("ui.show_confidence", true)
	viper.SetDefault("ui.show_explanations", true)
	viper.SetDefault("ui.pagination", 10)

	viper.SetDefault("database.type", "bbolt")
	viper.SetDefault("database.path", getDefaultDatabasePath())
	viper.SetDefault("database.max_size", 100)

	viper.SetDefault("history.enabled", true)
	viper.SetDefault("history.max_entries", 10000)
	viper.SetDefault("shell.enabled", true)
	viper.SetDefault("shell.hooks.bash", true)
	viper.SetDefault("shell.hooks.zsh", true)
	viper.SetDefault("shell.hooks.fish", true)
	viper.SetDefault("shell.hooks.powershell", true)
	viper.SetDefault("shell.hooks.pwsh", true)
	viper.SetDefault("shell.hooks.cmd", true)
	viper.SetDefault("shell.hooks.nushell", true)
	viper.SetDefault("shell.hooks.xonsh", true)
	viper.SetDefault("shell.hooks.elvish", true)

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.file", getDefaultLogPath())

	// TLDR defaults
	viper.SetDefault("tldr.enabled", true)
	viper.SetDefault("tldr.auto_sync", true)
	viper.SetDefault("tldr.auto_sync_interval", 7) // 7 days
	viper.SetDefault("tldr.offline_mode", false)
	viper.SetDefault("tldr.auto_detect_online", true)
	viper.SetDefault("tldr.max_cache_age", 30) // 30 days
	viper.SetDefault("tldr.default_platform", "common")
}

// createDefaultConfig creates a default configuration file
func createDefaultConfig(path string) error {
	defaultConfig := `# WUT - Command Helper
# Default Configuration File

app:
  name: "wut"
  version: "1.0.0"
  debug: false
  initialized: false

fuzzy:
  enabled: true
  case_sensitive: false
  max_distance: 3
  threshold: 0.6

ui:
  theme: "auto"
  show_confidence: true
  show_explanations: true
  syntax_highlighting: true
  pagination: 10
  colors:
    primary: "#7C3AED"
    secondary: "#10B981"
    warning: "#F59E0B"
    error: "#EF4444"
    info: "#3B82F6"

database:
  type: "bbolt"
  path: "~/.config/wut/wut.db"
  max_size: 100
  backup_enabled: true
  backup_interval: 24

history:
  enabled: true
  max_entries: 10000
  track_frequency: true
  track_context: true
  track_timing: true

context:
  enabled: true
  git_integration: true
  project_detection: true
  environment_vars: true
  directory_analysis: true

shell:
  enabled: true
  hooks:
    bash: true
    zsh: true
    fish: true
    powershell: true
    pwsh: true
    cmd: true
    nushell: true
    xonsh: true
    elvish: true

privacy:
  local_only: true
  encrypt_data: true
  anonymize_commands: false
  share_analytics: false

logging:
  level: "info"
  file: "~/.config/wut/logs/wut.log"
  max_size: 10
  max_backups: 5
  max_age: 30

`

	return os.WriteFile(path, []byte(defaultConfig), 0644)
}

// expandPaths expands environment variables and home directory in paths
func expandPaths(cfg *Config) {
	homeDir, _ := os.UserHomeDir()

	// Expand database path
	if cfg.Database.Path != "" {
		cfg.Database.Path = ResolveDatabasePath(cfg.Database.Path)
	}

	// Expand log path
	if cfg.Logging.File != "" {
		cfg.Logging.File = expandPath(cfg.Logging.File, homeDir)
	}
}

// expandPath expands ~ and environment variables in a path
func expandPath(path, homeDir string) string {
	if len(path) > 0 && path[0] == '~' {
		path = filepath.Join(homeDir, path[1:])
	}
	return os.ExpandEnv(path)
}

// getDefaultConfigPath returns the default configuration file path
func getDefaultConfigPath() string {
	return filepath.Join(getDefaultAppDir(), "config.yaml")
}

// GetDataDir returns the data directory path
func GetDataDir() string {
	return filepath.Dir(GetDatabasePath())
}

// EnsureDirs ensures all necessary directories exist
func EnsureDirs() error {
	homeDir, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Dir(GetConfigPath()),
		filepath.Dir(GetDatabasePath()),
		filepath.Dir(GetTLDRDatabasePath()),
		filepath.Dir(expandPath(Get().Logging.File, homeDir)),
	}

	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// GetDatabasePath returns the canonical path to the primary WUT database file.
func GetDatabasePath() string {
	return ResolveDatabasePath(Get().Database.Path)
}

// GetTLDRDatabasePath returns the canonical path to the TLDR cache database file.
func GetTLDRDatabasePath() string {
	return filepath.Join(filepath.Dir(GetDatabasePath()), "tldr.db")
}

// ResolveDatabasePath normalizes a configured database path while preserving
// existing single-file database locations for backward compatibility.
func ResolveDatabasePath(path string) string {
	homeDir, _ := os.UserHomeDir()

	path = strings.TrimSpace(path)
	if path == "" {
		return getDefaultDatabasePath()
	}

	path = expandPath(path, homeDir)
	cleaned := filepath.Clean(path)

	if info, err := os.Stat(cleaned); err == nil {
		if info.IsDir() {
			return filepath.Join(cleaned, "wut.db")
		}
		return cleaned
	}

	switch strings.ToLower(filepath.Ext(cleaned)) {
	case ".db", ".bolt", ".bbolt":
		return cleaned
	default:
		return filepath.Join(cleaned, "wut.db")
	}
}

func getDefaultAppDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "wut"
	}

	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		return filepath.Join(xdgConfig, "wut")
	}

	return filepath.Join(homeDir, ".config", "wut")
}

func getDefaultDatabasePath() string {
	return filepath.Join(getDefaultAppDir(), "wut.db")
}

func getDefaultLogPath() string {
	return filepath.Join(getDefaultAppDir(), "logs", "wut.log")
}

// GetConfigPath returns the current configuration file path
func GetConfigPath() string {
	if configPath != "" {
		return configPath
	}
	return getDefaultConfigPath()
}

// Reset resets configuration to defaults
func Reset() error {
	path := GetConfigPath()

	// Remove existing config
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing config: %w", err)
	}

	// Reset viper
	viper.Reset()

	// Recreate default config
	setDefaults()

	// Create new config file
	if err := createDefaultConfig(path); err != nil {
		return fmt.Errorf("failed to create default config: %w", err)
	}

	// Reload config
	cfg, err := Load(path)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	globalConfig = cfg
	return nil
}

// Edit opens the config file in the default editor
func Edit() error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		// Try common editors
		editors := []string{"nano", "vim", "vi", "code", "notepad", "notepad++"}
		for _, ed := range editors {
			if _, err := exec.LookPath(ed); err == nil {
				editor = ed
				break
			}
		}
	}

	if editor == "" {
		return fmt.Errorf("no editor found. Set EDITOR environment variable")
	}

	path := GetConfigPath()

	// Ensure file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := createDefaultConfig(path); err != nil {
			return fmt.Errorf("failed to create config file: %w", err)
		}
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// Import imports configuration from a file
func Import(path string) error {
	// Read source file
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read import file: %w", err)
	}

	// Validate YAML
	var importedCfg Config
	if err := yaml.Unmarshal(data, &importedCfg); err != nil {
		return fmt.Errorf("invalid config file: %w", err)
	}

	// Backup current config
	currentPath := GetConfigPath()
	backupPath := currentPath + ".backup." + time.Now().Format("20060102-150405")
	if _, err := os.Stat(currentPath); err == nil {
		if err := copyFile(currentPath, backupPath); err != nil {
			return fmt.Errorf("failed to create backup: %w", err)
		}
	}

	// Copy new config
	if err := copyFile(path, currentPath); err != nil {
		// Restore backup on failure
		_ = copyFile(backupPath, currentPath)
		return fmt.Errorf("failed to import config: %w", err)
	}

	// Reload
	cfg, err := Load(currentPath)
	if err != nil {
		// Restore backup on failure
		_ = copyFile(backupPath, currentPath)
		return fmt.Errorf("failed to load imported config: %w", err)
	}

	globalConfig = cfg
	return nil
}

// Export exports configuration to a file
func Export(path string) error {
	return copyFile(GetConfigPath(), path)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}
