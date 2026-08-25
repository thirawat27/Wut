// Package config is the typed configuration, with no framework behind it.
//
// the prototype used a global viper singleton read by seventeen files, with
// reads going through viper and writes going through yaml.Marshal — an
// asymmetry that baked environment-only keys into the user's file. Here Config
// is a plain value that is passed in, the same struct is read and written, and
// unknown keys are an error rather than a silent no-op.
//
// This package is pure: it defines, defaults, and validates. Reading and
// writing the file is adapter/configstore.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tier is how much the shell is allowed to tell WUT.
type Tier string

const (
	TierOff Tier = "off"  // record nothing
	TierT0  Tier = "T0"   // argv, exit code, cwd, duration
	TierT05 Tier = "T0.5" // T0 plus command-not-found
	TierT1  Tier = "T1"   // T0.5 plus captured stderr
)

// Output is the default rendering.
type Output string

const (
	OutputText Output = "text"
	OutputJSON Output = "json"
)

// Config is the whole of WUT's configuration. Fifteen keys, each with a
// reason to exist; the prototype had more than thirty.
type Config struct {
	Capture   Capture   `yaml:"capture"`
	Knowledge Knowledge `yaml:"knowledge"`
	Model     Model     `yaml:"model"`
	Daemon    Daemon    `yaml:"daemon"`
	UI        UI        `yaml:"ui"`
	History   History   `yaml:"history"`
	Shell     Shell     `yaml:"shell"`
}

type Capture struct {
	Tier      Tier          `yaml:"tier"`
	Retention time.Duration `yaml:"retention"`
	Redact    []string      `yaml:"redact"`
}

type Knowledge struct {
	AutoSync     bool          `yaml:"auto_sync"`
	SyncInterval time.Duration `yaml:"sync_interval"`
}

type Model struct {
	Tier1   string `yaml:"tier1"`    // auto | off | <path>
	Tier2   string `yaml:"tier2"`    // off | auto | ollama | <path>
	MaxRSS  string `yaml:"max_rss"`  // human-readable, e.g. 1400MB
	Ollama  string `yaml:"ollama"`   // base URL when tier2 is "ollama"
	Tier2ID string `yaml:"tier2_id"` // model name for the chosen backend
}

type Daemon struct {
	Autostart   bool          `yaml:"autostart"`
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

type UI struct {
	Theme  string `yaml:"theme"` // auto | light | dark | none
	Output Output `yaml:"output"`
}

type History struct {
	Enabled    bool `yaml:"enabled"`
	MaxEntries int  `yaml:"max_entries"`
}

type Shell struct {
	// Alias is an optional extra trigger word. It exists because `oops` was
	// removed in favour of bare `wut`: anyone who wants two keystrokes can
	// have them, without the product having to teach a second name.
	Alias string `yaml:"alias"`
}

// Default returns the configuration WUT ships with.
func Default() Config {
	return Config{
		Capture: Capture{
			Tier:      TierT05,
			Retention: 24 * time.Hour,
		},
		Knowledge: Knowledge{
			AutoSync:     true,
			SyncInterval: 7 * 24 * time.Hour,
		},
		Model: Model{
			Tier1:   "auto",
			Tier2:   "off",
			MaxRSS:  "1400MB",
			Ollama:  "http://127.0.0.1:11434",
			Tier2ID: "qwen2.5:0.5b-instruct",
		},
		Daemon: Daemon{
			Autostart:   false,
			IdleTimeout: 30 * time.Minute,
		},
		UI: UI{
			Theme:  "auto",
			Output: OutputText,
		},
		History: History{
			Enabled:    true,
			MaxEntries: 20000,
		},
	}
}

// Validate reports the first problem with a configuration, naming the key.
func (c Config) Validate() error {
	switch c.Capture.Tier {
	case TierOff, TierT0, TierT05, TierT1:
	default:
		return fmt.Errorf("capture.tier: %q is not one of off, T0, T0.5, T1", c.Capture.Tier)
	}
	if c.Capture.Retention < 0 {
		return fmt.Errorf("capture.retention: must not be negative")
	}
	switch c.UI.Output {
	case OutputText, OutputJSON:
	default:
		return fmt.Errorf("ui.output: %q is not one of text, json", c.UI.Output)
	}
	switch c.UI.Theme {
	case "auto", "light", "dark", "none":
	default:
		return fmt.Errorf("ui.theme: %q is not one of auto, light, dark, none", c.UI.Theme)
	}
	if c.History.MaxEntries < 0 {
		return fmt.Errorf("history.max_entries: must not be negative")
	}
	if c.Knowledge.SyncInterval < 0 {
		return fmt.Errorf("knowledge.sync_interval: must not be negative")
	}
	if c.Daemon.IdleTimeout < 0 {
		return fmt.Errorf("daemon.idle_timeout: must not be negative")
	}
	if _, err := ParseBytes(c.Model.MaxRSS); err != nil {
		return fmt.Errorf("model.max_rss: %w", err)
	}
	return nil
}

// CapturesOutput reports whether the configured tier records stderr.
func (c Config) CapturesOutput() bool { return c.Capture.Tier == TierT1 }

// CapturesEvents reports whether the shell records anything at all.
func (c Config) CapturesEvents() bool { return c.Capture.Tier != TierOff }

// Lookup is how the environment is read. It is a parameter rather than a
// direct call to os.LookupEnv because this package is pure: reading the
// process environment is I/O, and keeping it out is what lets every
// precedence rule below be tested with a map.
type Lookup func(key string) (string, bool)

// NoEnv is a Lookup that finds nothing, for callers with no environment to
// apply.
func NoEnv(string) (string, bool) { return "", false }

// ApplyEnv overlays WUT_* environment variables. Precedence is
// defaults -> file -> env -> flags, in that order and no other.
func (c Config) ApplyEnv(lookup Lookup) (Config, error) {
	if lookup == nil {
		lookup = NoEnv
	}
	if v, ok := lookup("WUT_CAPTURE_TIER"); ok {
		c.Capture.Tier = Tier(v)
	}
	if v, ok := lookup("WUT_OUTPUT"); ok {
		c.UI.Output = Output(strings.ToLower(v))
	}
	if v, ok := lookup("WUT_THEME"); ok {
		c.UI.Theme = strings.ToLower(v)
	}
	if v, ok := lookup("WUT_MODEL_TIER2"); ok {
		c.Model.Tier2 = v
	}
	if v, ok := lookup("WUT_OLLAMA_URL"); ok {
		c.Model.Ollama = v
	}
	if v, ok := lookup("WUT_DAEMON_AUTOSTART"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("WUT_DAEMON_AUTOSTART: %w", err)
		}
		c.Daemon.Autostart = b
	}
	if v, ok := lookup("WUT_HISTORY_ENABLED"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("WUT_HISTORY_ENABLED: %w", err)
		}
		c.History.Enabled = b
	}
	if _, ok := lookup("WUT_NO_DAEMON"); ok {
		c.Daemon.Autostart = false
	}
	return c, c.Validate()
}

// DaemonDisabled reports the kill switch. It takes a Lookup for the same
// reason ApplyEnv does, and is separate from Config because it must work
// before any configuration has been loaded.
func DaemonDisabled(lookup Lookup) bool {
	if lookup == nil {
		return false
	}
	v, ok := lookup("WUT_NO_DAEMON")
	return ok && v != "0" && v != "false"
}

// ParseBytes accepts 1400MB, 1.5GB, 900K, or a bare byte count.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "GB"), strings.HasSuffix(s, "G"):
		mult = 1 << 30
	case strings.HasSuffix(s, "MB"), strings.HasSuffix(s, "M"):
		mult = 1 << 20
	case strings.HasSuffix(s, "KB"), strings.HasSuffix(s, "K"):
		mult = 1 << 10
	}
	num := strings.TrimRight(s, "GMKB")
	f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	if f < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	return int64(f * float64(mult)), nil
}
