// Package paths resolves the four directories WUT is allowed to write to.
//
// Every path is overridable by an environment variable, which is also how the
// test suite gets full isolation from a developer's real state.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Dirs is the complete set of locations WUT may touch. Anything outside these
// (plus a managed block in a shell rc file) is a boundary violation.
type Dirs struct {
	Config string
	Data   string
	State  string
	Cache  string
}

// Resolve returns the platform-correct directories, honouring WUT_*_DIR
// overrides. It does not create anything; see EnsureAll.
func Resolve() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	d := defaults(home)
	d.Config = envOr("WUT_CONFIG_DIR", d.Config)
	d.Data = envOr("WUT_DATA_DIR", d.Data)
	d.State = envOr("WUT_STATE_DIR", d.State)
	d.Cache = envOr("WUT_CACHE_DIR", d.Cache)
	return d, nil
}

func defaults(home string) Dirs {
	switch runtime.GOOS {
	case "windows":
		appData := envOr("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		local := envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		return Dirs{
			Config: filepath.Join(appData, "wut"),
			Data:   filepath.Join(local, "wut"),
			State:  filepath.Join(local, "wut", "state"),
			Cache:  filepath.Join(local, "wut", "cache"),
		}
	case "darwin":
		support := filepath.Join(home, "Library", "Application Support", "wut")
		return Dirs{
			Config: support,
			Data:   support,
			State:  support,
			Cache:  filepath.Join(home, "Library", "Caches", "wut"),
		}
	default:
		return Dirs{
			Config: filepath.Join(xdg("XDG_CONFIG_HOME", home, ".config"), "wut"),
			Data:   filepath.Join(xdg("XDG_DATA_HOME", home, ".local", "share"), "wut"),
			State:  filepath.Join(xdg("XDG_STATE_HOME", home, ".local", "state"), "wut"),
			Cache:  filepath.Join(xdg("XDG_CACHE_HOME", home, ".cache"), "wut"),
		}
	}
}

func xdg(key, home string, fallback ...string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ConfigFile is the single configuration file WUT reads and writes.
func (d Dirs) ConfigFile() string { return filepath.Join(d.Config, "config.yaml") }

// EnsureAll creates every directory, 0o700, and reports the first failure.
func (d Dirs) EnsureAll() error {
	for _, p := range []string{d.Config, d.Data, d.State, d.Cache} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return err
		}
	}
	return nil
}
