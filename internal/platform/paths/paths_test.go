package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These are the only directories WUT writes to. Getting them wrong scatters
// state across a machine in places nobody knows to look, and `wut purge`
// cannot delete what it cannot find.

func TestResolveProducesAbsoluteDirectories(t *testing.T) {
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"config": d.Config, "data": d.Data, "state": d.State,
	} {
		if dir == "" {
			t.Errorf("%s directory is empty", name)
			continue
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("%s directory %q is not absolute", name, dir)
		}
		if !strings.Contains(strings.ToLower(dir), "wut") {
			t.Errorf("%s directory %q does not mention wut; state would be unfindable", name, dir)
		}
	}
}

// The overrides exist so a test, a container, or a sandboxed run can put state
// somewhere else. Without them the shell matrix would edit the real home
// directory of whoever ran it.
func TestEnvironmentOverridesWin(t *testing.T) {
	base := t.TempDir()
	t.Setenv("WUT_CONFIG_DIR", filepath.Join(base, "cfg"))
	t.Setenv("WUT_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("WUT_STATE_DIR", filepath.Join(base, "state"))

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if d.Config != filepath.Join(base, "cfg") {
		t.Errorf("config = %q, want the override", d.Config)
	}
	if d.Data != filepath.Join(base, "data") {
		t.Errorf("data = %q, want the override", d.Data)
	}
	if d.State != filepath.Join(base, "state") {
		t.Errorf("state = %q, want the override", d.State)
	}
}

func TestConfigFileSitsInTheConfigDirectory(t *testing.T) {
	d := Dirs{Config: filepath.Join("some", "where")}
	got := d.ConfigFile()
	if filepath.Dir(got) != d.Config {
		t.Errorf("config file %q is not in %q", got, d.Config)
	}
	if filepath.Base(got) != "config.yaml" {
		t.Errorf("config file is named %q", filepath.Base(got))
	}
}

func TestEnsureAllCreatesWhatIsMissing(t *testing.T) {
	base := t.TempDir()
	d := Dirs{
		Config: filepath.Join(base, "c"),
		Data:   filepath.Join(base, "d"),
		State:  filepath.Join(base, "s"),
		Cache:  filepath.Join(base, "k"),
	}
	if err := d.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{d.Config, d.Data, d.State, d.Cache} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%q was not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", dir)
		}
	}
	// Twice must be fine: it runs on every command that writes anything.
	if err := d.EnsureAll(); err != nil {
		t.Errorf("EnsureAll is not idempotent: %v", err)
	}
}

// The platform conventions are the reason these are separate fields rather
// than one directory. Getting them wrong is not a crash, it is state in the
// wrong place forever.
func TestPlatformLayout(t *testing.T) {
	home := filepath.Join("home", "someone")
	d := defaults(home)

	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(d.Config, "Roaming") {
			t.Errorf("config = %q, want it under Roaming", d.Config)
		}
		if !strings.Contains(d.Data, "Local") {
			t.Errorf("data = %q, want it under Local", d.Data)
		}
	case "darwin":
		if !strings.Contains(d.Config, "Application Support") {
			t.Errorf("config = %q, want it under Application Support", d.Config)
		}
	default:
		if !strings.Contains(d.Config, ".config") {
			t.Errorf("config = %q, want it under .config", d.Config)
		}
		if !strings.Contains(d.Data, ".local") {
			t.Errorf("data = %q, want it under .local", d.Data)
		}
	}
}

func TestXDGVariablesAreHonouredOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG does not apply on this platform")
	}
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "xdgcfg"))

	d := defaults(filepath.Join("home", "someone"))
	if !strings.HasPrefix(d.Config, filepath.Join(base, "xdgcfg")) {
		t.Errorf("config = %q, want it under XDG_CONFIG_HOME", d.Config)
	}
}

func TestEnvOrFallsBack(t *testing.T) {
	t.Setenv("WUT_TEST_PATHS_VAR", "")
	if got := envOr("WUT_TEST_PATHS_VAR", "fallback"); got != "fallback" {
		t.Errorf("an empty variable gave %q, want the fallback", got)
	}
	t.Setenv("WUT_TEST_PATHS_VAR", "set")
	if got := envOr("WUT_TEST_PATHS_VAR", "fallback"); got != "set" {
		t.Errorf("a set variable gave %q", got)
	}
}
