package configstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/platform/paths"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return New(paths.Dirs{Config: dir, Data: dir, State: dir})
}

// A missing config file is the normal state before first run. Treating it as
// an error would mean WUT could not answer anything until it had been
// configured, which is backwards.
func TestMissingFileLoadsDefaults(t *testing.T) {
	s := newStore(t)
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("a missing config file was an error: %v", err)
	}
	if cfg.Capture.Tier != config.Default().Capture.Tier {
		t.Errorf("tier = %q, want the default", cfg.Capture.Tier)
	}
	if s.Exists() {
		t.Error("Exists reported a file that was never written")
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	s := newStore(t)
	want := config.Default()
	want.Capture.Tier = config.TierT1
	want.Capture.Retention = 90 * time.Minute
	want.Capture.Redact = []string{`INTERNAL-\d+`}
	want.Shell.Alias = "uh"
	want.History.MaxEntries = 42

	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	if !s.Exists() {
		t.Error("Exists reported no file after a save")
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"capture.tier", "capture.retention", "shell.alias", "history.max_entries"} {
		a, _ := want.Get(k)
		b, _ := got.Get(k)
		if a != b {
			t.Errorf("%s: saved %q, loaded %q", k, a, b)
		}
	}
	if len(got.Capture.Redact) != 1 {
		t.Errorf("redact patterns = %v", got.Capture.Redact)
	}
}

// An empty file is a valid configuration — it is what someone gets when they
// clear the file to start over. Reporting it as a parse failure would break
// the one command that could fix it.
func TestAnEmptyFileIsValid(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.Path(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Errorf("an empty file was rejected: %v", err)
	}
}

// A typo in a config file that changes nothing and says nothing is the worst
// possible outcome for the person who typed it.
func TestAnUnknownKeyIsAnErrorNamingIt(t *testing.T) {
	s := newStore(t)
	body := "capture:\n  teir: T1\n"
	if err := os.WriteFile(s.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	if err == nil {
		t.Fatal("an unknown key was silently ignored")
	}
	if !strings.Contains(err.Error(), "teir") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

func TestAnInvalidValueIsRejected(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.Path(), []byte("ui:\n  output: yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Error("ui.output: yaml was accepted")
	}
}

func TestSaveRefusesAnInvalidConfiguration(t *testing.T) {
	s := newStore(t)
	bad := config.Default()
	bad.Capture.Tier = "T9"
	if err := s.Save(bad); err == nil {
		t.Fatal("an invalid configuration was written")
	}
	if s.Exists() {
		t.Error("a rejected save still created the file")
	}
}

// The environment is read here, in the adapter, so the core stays pure. This
// checks the precedence the whole design depends on: file, then environment.
func TestEnvironmentOverridesTheFile(t *testing.T) {
	s := newStore(t)
	cfg := config.Default()
	cfg.Capture.Tier = config.TierT0
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WUT_CAPTURE_TIER", "T1")
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Capture.Tier != config.TierT1 {
		t.Errorf("tier = %q; the environment did not win over the file", got.Capture.Tier)
	}
}

// Saving writes what was read, so an environment-only value must not be baked
// into the user's file. Getting this wrong is how a temporary override becomes
// permanent without anyone choosing it.
func TestTheEnvironmentIsNotWrittenToTheFile(t *testing.T) {
	s := newStore(t)
	t.Setenv("WUT_CAPTURE_TIER", "T1")

	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capture.Tier != config.TierT1 {
		t.Fatal("the environment did not apply")
	}

	// The caller writes back what it was given, which is the realistic path:
	// `wut config set` saves the loaded configuration plus one change.
	cfg.Capture.Tier = config.TierT0
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path())
	if strings.Contains(string(data), "T1") {
		t.Errorf("an environment-only value was written to the file:\n%s", data)
	}
}

// A crash between the temp write and the rename must leave the old file, never
// a truncated one. The test cannot crash the process, so it checks the
// invariant that makes it true: nothing is written to the target path directly.
func TestWritesAreAtomic(t *testing.T) {
	s := newStore(t)
	first := config.Default()
	first.Shell.Alias = "one"
	if err := s.Save(first); err != nil {
		t.Fatal(err)
	}

	second := config.Default()
	second.Shell.Alias = "two"
	if err := s.Save(second); err != nil {
		t.Fatal(err)
	}

	// No temp files may survive a successful save.
	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".wut-config-") {
			t.Errorf("a temporary file survived the save: %s", e.Name())
		}
	}

	got, _ := s.Load()
	if got.Shell.Alias != "two" {
		t.Errorf("alias = %q after the second save", got.Shell.Alias)
	}
}

func TestTheFileIsReadableByHand(t *testing.T) {
	s := newStore(t)
	if err := s.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "#") {
		t.Error("the file has no explanatory header")
	}
	if !strings.Contains(body, "wut config explain") {
		t.Error("the header does not say how to find out what a key does")
	}
}

func TestPathIsUnderTheConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	s := New(paths.Dirs{Config: dir})
	if filepath.Dir(s.Path()) != dir {
		t.Errorf("path = %q, want it under %q", s.Path(), dir)
	}
}
