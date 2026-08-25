package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Key is one configuration key: what it does, what it accepts, how to read it,
// and how to set it.
//
// There is one table, not three. `wut config show`, `wut config explain`, and
// `wut config set` all read these entries, so a key cannot be documented but
// unsettable, printed but undocumented, or settable but invisible — which is
// the state every configuration surface drifts into when the list of keys, the
// list of docs, and the printer are maintained separately.
type Key struct {
	Name    string
	What    string
	Values  string
	Default string

	// read renders the current value. apply parses and writes a new one.
	// Both are unexported so the table is the only way to reach a field by
	// name, and a caller cannot invent an accessor for a key that has no
	// documentation.
	read  func(Config) string
	apply func(*Config, string) error
}

// keys is the complete settable surface. Seventeen entries; the prototype had
// more than thirty, and the difference is entirely keys nobody could explain.
var keys = []Key{
	{
		Name: "capture.tier", Values: "off | T0 | T0.5 | T1", Default: "T0.5",
		What:  "How much the shell tells WUT about commands that ran.",
		read:  func(c Config) string { return string(c.Capture.Tier) },
		apply: func(c *Config, v string) error { c.Capture.Tier = Tier(canonicalTier(v)); return nil },
	},
	{
		Name: "capture.retention", Values: "a duration, e.g. 24h", Default: "24h",
		What:  "How long captured error output is kept before deletion.",
		read:  func(c Config) string { return c.Capture.Retention.String() },
		apply: setDuration(func(c *Config) *time.Duration { return &c.Capture.Retention }),
	},
	{
		Name: "capture.redact", Values: "comma-separated regexes", Default: "empty",
		What:  "Extra patterns to strip from captured output, on top of the built-in ones.",
		read:  func(c Config) string { return strings.Join(c.Capture.Redact, ",") },
		apply: func(c *Config, v string) error { c.Capture.Redact = splitList(v); return nil },
	},
	{
		Name: "knowledge.auto_sync", Values: "true | false", Default: "true",
		What:  "Refresh the tldr index on a schedule.",
		read:  func(c Config) string { return strconv.FormatBool(c.Knowledge.AutoSync) },
		apply: setBool(func(c *Config) *bool { return &c.Knowledge.AutoSync }),
	},
	{
		Name: "knowledge.sync_interval", Values: "a duration", Default: "168h",
		What:  "How often to refresh it.",
		read:  func(c Config) string { return c.Knowledge.SyncInterval.String() },
		apply: setDuration(func(c *Config) *time.Duration { return &c.Knowledge.SyncInterval }),
	},
	{
		Name: "model.tier1", Values: "auto | off | a path", Default: "auto",
		What:  "The embedding model used for natural-language search.",
		read:  func(c Config) string { return c.Model.Tier1 },
		apply: setString(func(c *Config) *string { return &c.Model.Tier1 }),
	},
	{
		Name: "model.tier2", Values: "off | auto | ollama | a path", Default: "off",
		What:  "The optional generative model used for wording.",
		read:  func(c Config) string { return c.Model.Tier2 },
		apply: setString(func(c *Config) *string { return &c.Model.Tier2 }),
	},
	{
		Name: "model.tier2_id", Values: "a model name", Default: "qwen2.5:0.5b-instruct",
		What:  "Which model to ask the tier 2 backend for.",
		read:  func(c Config) string { return c.Model.Tier2ID },
		apply: setString(func(c *Config) *string { return &c.Model.Tier2ID }),
	},
	{
		Name: "model.ollama", Values: "a URL", Default: "http://127.0.0.1:11434",
		What:  "Where a local Ollama is listening, when tier2 is ollama.",
		read:  func(c Config) string { return c.Model.Ollama },
		apply: setString(func(c *Config) *string { return &c.Model.Ollama }),
	},
	{
		Name: "model.max_rss", Values: "a size, e.g. 1400MB", Default: "1400MB",
		What:  "Refuse to load a model that would exceed this.",
		read:  func(c Config) string { return c.Model.MaxRSS },
		apply: setString(func(c *Config) *string { return &c.Model.MaxRSS }),
	},
	{
		Name: "daemon.autostart", Values: "true | false", Default: "false",
		What:  "Let the CLI start the background daemon on first use.",
		read:  func(c Config) string { return strconv.FormatBool(c.Daemon.Autostart) },
		apply: setBool(func(c *Config) *bool { return &c.Daemon.Autostart }),
	},
	{
		Name: "daemon.idle_timeout", Values: "a duration", Default: "30m",
		What:  "How long the daemon stays up with nothing to do.",
		read:  func(c Config) string { return c.Daemon.IdleTimeout.String() },
		apply: setDuration(func(c *Config) *time.Duration { return &c.Daemon.IdleTimeout }),
	},
	{
		Name: "ui.theme", Values: "auto | light | dark | none", Default: "auto",
		What:  "Colour scheme.",
		read:  func(c Config) string { return c.UI.Theme },
		apply: func(c *Config, v string) error { c.UI.Theme = strings.ToLower(v); return nil },
	},
	{
		Name: "ui.output", Values: "text | json", Default: "text",
		What:  "Default output format.",
		read:  func(c Config) string { return string(c.UI.Output) },
		apply: func(c *Config, v string) error { c.UI.Output = Output(strings.ToLower(v)); return nil },
	},
	{
		Name: "history.enabled", Values: "true | false", Default: "true",
		What:  "Keep a local log of commands the shell reported.",
		read:  func(c Config) string { return strconv.FormatBool(c.History.Enabled) },
		apply: setBool(func(c *Config) *bool { return &c.History.Enabled }),
	},
	{
		Name: "history.max_entries", Values: "a number", Default: "20000",
		What:  "How many events to keep.",
		read:  func(c Config) string { return strconv.Itoa(c.History.MaxEntries) },
		apply: setInt(func(c *Config) *int { return &c.History.MaxEntries }),
	},
	{
		Name: "shell.alias", Values: "a word, or empty", Default: "empty",
		What:  "An extra trigger word, if you want one shorter than `wut`.",
		read:  func(c Config) string { return c.Shell.Alias },
		apply: setString(func(c *Config) *string { return &c.Shell.Alias }),
	},
}

// Keys returns every configuration key, sorted, with the accessors stripped.
// Callers get something they can display and nothing they can mutate through.
func Keys() []Key {
	out := make([]Key, 0, len(keys))
	for _, k := range keys {
		k.read, k.apply = nil, nil
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// KeysMatching returns the keys whose name starts with the given prefix. An
// empty prefix returns all of them.
func KeysMatching(prefix string) []Key {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return Keys()
	}
	var out []Key
	for _, k := range Keys() {
		if strings.HasPrefix(k.Name, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// Setting is one key and its current value.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Settings renders the whole configuration as key/value pairs, in table order
// so related keys stay together on screen rather than being scattered
// alphabetically.
func (c Config) Settings() []Setting {
	out := make([]Setting, 0, len(keys))
	for _, k := range keys {
		out = append(out, Setting{Key: k.Name, Value: k.read(c)})
	}
	return out
}

// Get returns one key's current value.
func (c Config) Get(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, k := range keys {
		if k.Name == name {
			return k.read(c), true
		}
	}
	return "", false
}

// Set applies one key and returns the new configuration.
//
// It validates before returning, so an invalid value is refused here rather
// than written to the file and rejected on the next load — which would leave
// the user with a WUT that will not start until they edit YAML by hand.
func Set(c Config, name, value string) (Config, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, k := range keys {
		if k.Name != name {
			continue
		}
		// Apply to a copy. On failure the caller gets the configuration it
		// handed in, untouched — returning a half-applied one alongside an
		// error is how a rejected value ends up in memory anyway, and then in
		// the file the next time anything saves.
		updated := c
		if err := k.apply(&updated, strings.TrimSpace(value)); err != nil {
			return c, fmt.Errorf("%s: %w", name, err)
		}
		if err := updated.Validate(); err != nil {
			return c, err
		}
		return updated, nil
	}
	return c, &UnknownKeyError{Key: name, Near: nearestKeys(name)}
}

// UnknownKeyError names the key that was not recognised, and the closest ones
// that were. "unknown key" with no suggestion is the least useful thing a
// configuration system can say to someone who just made a typo.
type UnknownKeyError struct {
	Key  string
	Near []string
}

func (e *UnknownKeyError) Error() string {
	if len(e.Near) == 0 {
		return fmt.Sprintf("no such configuration key: %s", e.Key)
	}
	return fmt.Sprintf("no such configuration key: %s (did you mean %s?)", e.Key, strings.Join(e.Near, " or "))
}

// nearestKeys returns keys sharing a section with the unknown one, then keys
// containing it as a substring.
func nearestKeys(name string) []string {
	section, _, _ := strings.Cut(name, ".")
	var out []string
	for _, k := range keys {
		ksec, _, _ := strings.Cut(k.Name, ".")
		if ksec == section || strings.Contains(k.Name, name) {
			out = append(out, k.Name)
		}
	}
	sort.Strings(out)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// canonicalTier accepts the friendly spellings people actually type.
//
// "on" and "off" are what a person reaches for when they want capture to stop
// or start, and refusing them in favour of "T0.5" is pedantry with a manual
// page attached.
func canonicalTier(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "default":
		return string(TierT05)
	case "off", "none", "no":
		return string(TierOff)
	case "t0":
		return string(TierT0)
	case "t0.5", "t05":
		return string(TierT05)
	case "t1":
		return string(TierT1)
	}
	return v
}

func setString(field func(*Config) *string) func(*Config, string) error {
	return func(c *Config, v string) error { *field(c) = v; return nil }
}

func setBool(field func(*Config) *bool) func(*Config, string) error {
	return func(c *Config, v string) error {
		b, err := strconv.ParseBool(strings.ToLower(v))
		if err != nil {
			return fmt.Errorf("%q is not true or false", v)
		}
		*field(c) = b
		return nil
	}
}

func setInt(field func(*Config) *int) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%q is not a number", v)
		}
		*field(c) = n
		return nil
	}
}

func setDuration(field func(*Config) *time.Duration) func(*Config, string) error {
	return func(c *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%q is not a duration; try 24h, 30m, or 168h", v)
		}
		*field(c) = d
		return nil
	}
}

// splitList parses a comma-separated value, dropping empties so a trailing
// comma is not a pattern that matches everything.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
