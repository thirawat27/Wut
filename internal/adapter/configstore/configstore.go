// Package configstore reads and writes the configuration file.
//
// Three properties, each fixing something the prototype got wrong:
//
//   - The same struct is read and written, so a save cannot bake an
//     environment-only value into the user's file.
//   - Unknown keys are an error naming the key, not a silent no-op. A typo in
//     a config file that changes nothing and says nothing is the worst
//     possible outcome for the person who typed it.
//   - Writes are atomic: temp file, fsync, rename. A crash mid-write leaves
//     the old file, never a truncated one.
package configstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/platform/paths"
	"github.com/thirawat27/wut/internal/port"
	"gopkg.in/yaml.v3"
)

var _ port.ConfigWriter = (*Store)(nil)

// Store reads and writes one configuration file.
type Store struct {
	file string
}

// New returns a store for the standard location.
func New(dirs paths.Dirs) *Store { return &Store{file: dirs.ConfigFile()} }

// Path is the file this store reads and writes.
func (s *Store) Path() string { return s.file }

// Load returns the configuration: defaults, overlaid by the file if one
// exists, overlaid by the environment.
//
// A missing file is not an error. It is the normal state before first run, and
// treating it as a failure would mean WUT could not answer anything until it
// had been configured, which is backwards.
func (s *Store) Load() (config.Config, error) {
	cfg := config.Default()

	data, err := os.ReadFile(s.file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Nothing to overlay.
	case err != nil:
		return cfg, fmt.Errorf("read %s: %w", s.file, err)
	default:
		if err := decodeStrict(data, &cfg); err != nil {
			return config.Default(), fmt.Errorf("%s: %w", s.file, err)
		}
	}

	// The environment is read here, in the adapter, and handed to the pure
	// core as a function.
	cfg, err = cfg.ApplyEnv(os.LookupEnv)
	if err != nil {
		return config.Default(), err
	}
	return cfg, nil
}

// decodeStrict fails on any key the struct does not declare, and reports the
// line it was on.
func decodeStrict(data []byte, cfg *config.Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		// An empty file decodes to io.EOF. That is a valid config, not a
		// parse failure, and reporting it as one would break `wut config edit`
		// the moment someone cleared the file to start over.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return cfg.Validate()
}

// Save writes the configuration atomically.
func (s *Store) Save(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# WUT configuration.\n" +
		"# Every key here is documented by: wut config explain <key>\n" +
		"# Precedence is defaults -> this file -> WUT_* environment -> flags.\n\n"
	return writeFileAtomic(s.file, append([]byte(header), data...), 0o600)
}

// writeFileAtomic writes via a temp file in the same directory, fsyncs it, and
// renames over the target.
//
// The Windows branch is not optional: rename over an existing file fails there
// with ERROR_ACCESS_DENIED often enough that a naive implementation loses the
// user's config on the first save.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wut-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// Remove first; a failure here is fine when the target does not exist.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(tmpName, path)
}

// Exists reports whether a config file has been written yet.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.file)
	return err == nil
}
