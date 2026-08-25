// Package userdata holds the only things in WUT the user actually authored:
// saved commands and aliases.
//
// Everything else WUT stores is derived — the index rebuilds, the event log is
// a recording, the config has defaults. This is the one store where losing a
// file loses something the user cannot get back, so it is plain YAML in a
// known location rather than a binary format, and it is written atomically.
//
// Being plain YAML is deliberate: it can live in a dotfiles repository, be
// diffed, and be edited by hand. A tool that locks a user's own list inside a
// format only it can read has taken something from them.
package userdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thirawat27/wut/internal/port"
	"gopkg.in/yaml.v3"
)

// Saved and Alias are the port's types. Aliasing rather than redefining keeps
// the YAML shape and the wire shape from drifting apart silently.
type (
	Saved = port.SavedCommand
	Alias = port.UserAlias
)

// file is the on-disk shape.
type file struct {
	Version int     `yaml:"version"`
	Saved   []Saved `yaml:"saved,omitempty"`
	Aliases []Alias `yaml:"aliases,omitempty"`
}

var _ port.UserData = (*Store)(nil)

const schemaVersion = 1

// Store reads and writes the user's own data.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a store rooted at the config directory, next to config.yaml
// rather than in the state directory — this is configuration in the sense that
// matters: the user wrote it, and `wut purge` must not touch it.
func New(configDir string) *Store {
	return &Store{path: filepath.Join(configDir, "saved.yaml")}
}

// Path is where the file lives, for `wut save --path`.
func (s *Store) Path() string { return s.path }

func (s *Store) load() (file, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file{Version: schemaVersion}, nil
	}
	if err != nil {
		return file{}, err
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return file{}, fmt.Errorf("%s: %w", s.path, err)
	}
	if f.Version == 0 {
		f.Version = schemaVersion
	}
	return f, nil
}

func (s *Store) save(f file) error {
	f.Version = schemaVersion
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	header := "# Commands and aliases you saved with wut.\n" +
		"# Safe to edit by hand, and safe to keep in a dotfiles repository.\n" +
		"# `wut purge` does not touch this file: you wrote it.\n\n"
	return writeAtomic(s.path, append([]byte(header), data...))
}

// Add saves a command. Saving the same command twice updates it rather than
// duplicating it — a list with three copies of the same line is a list nobody
// scrolls through twice.
func (s *Store) Add(cmd, note string, tags []string) (Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return Saved{}, errors.New("nothing to save")
	}
	f, err := s.load()
	if err != nil {
		return Saved{}, err
	}
	entry := Saved{Command: cmd, Note: note, Tags: tags, Added: time.Now().UTC()}
	for i, existing := range f.Saved {
		if existing.Command == cmd {
			entry.Added = existing.Added
			f.Saved[i] = entry
			return entry, s.save(f)
		}
	}
	f.Saved = append(f.Saved, entry)
	return entry, s.save(f)
}

// Remove deletes a saved command by exact text or by index as listed.
func (s *Store) Remove(match string) (Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.load()
	if err != nil {
		return Saved{}, err
	}
	for i, entry := range f.Saved {
		if entry.Command == match {
			f.Saved = append(f.Saved[:i], f.Saved[i+1:]...)
			return entry, s.save(f)
		}
	}
	return Saved{}, fmt.Errorf("nothing saved matching %q", match)
}

// List returns saved commands, newest first, optionally filtered.
func (s *Store) List(filter string) ([]Saved, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Saved, 0, len(f.Saved))
	needle := strings.ToLower(strings.TrimSpace(filter))
	for _, entry := range f.Saved {
		if needle != "" && !matches(entry, needle) {
			continue
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Added.After(out[j].Added) })
	return out, nil
}

func matches(e Saved, needle string) bool {
	if strings.Contains(strings.ToLower(e.Command), needle) ||
		strings.Contains(strings.ToLower(e.Note), needle) {
		return true
	}
	for _, t := range e.Tags {
		if strings.Contains(strings.ToLower(t), needle) {
			return true
		}
	}
	return false
}

// SetAlias defines or replaces an alias.
func (s *Store) SetAlias(name, cmd, note string) (Alias, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	cmd = strings.TrimSpace(cmd)
	if err := validAliasName(name); err != nil {
		return Alias{}, err
	}
	if cmd == "" {
		return Alias{}, errors.New("an alias needs a command to expand to")
	}
	f, err := s.load()
	if err != nil {
		return Alias{}, err
	}
	entry := Alias{Name: name, Command: cmd, Note: note, Added: time.Now().UTC()}
	for i, existing := range f.Aliases {
		if existing.Name == name {
			entry.Added = existing.Added
			f.Aliases[i] = entry
			return entry, s.save(f)
		}
	}
	f.Aliases = append(f.Aliases, entry)
	return entry, s.save(f)
}

// RemoveAlias deletes one.
func (s *Store) RemoveAlias(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.load()
	if err != nil {
		return err
	}
	for i, a := range f.Aliases {
		if a.Name == name {
			f.Aliases = append(f.Aliases[:i], f.Aliases[i+1:]...)
			return s.save(f)
		}
	}
	return fmt.Errorf("no alias called %q", name)
}

// Aliases returns them sorted by name.
func (s *Store) Aliases() ([]Alias, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := append([]Alias(nil), f.Aliases...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// reservedAliasNames would shadow something the user needs more than an alias.
var reservedAliasNames = map[string]bool{
	"wut": true, "cd": true, "ls": true, "rm": true, "cp": true, "mv": true,
	"sudo": true, "git": true, "exit": true, "kill": true, "export": true,
}

// validAliasName rejects names that would break more than they help.
//
// An alias is shell text the user will run, so a name containing a space or a
// shell operator is not merely odd — it produces a definition that does
// something other than what they wrote.
func validAliasName(name string) error {
	switch {
	case name == "":
		return errors.New("an alias needs a name")
	case reservedAliasNames[name]:
		return fmt.Errorf("%q would shadow a command you need; pick another name", name)
	case strings.ContainsAny(name, " \t\n'\"|&;<>()$`\\/"):
		return fmt.Errorf("%q contains characters a shell would interpret", name)
	case len(name) > 32:
		return errors.New("that name is too long to be a shortcut")
	}
	return nil
}

// ShellDefinitions renders the aliases as definitions for a shell.
//
// WUT does not write these into a startup file itself. They are printed for
// the user to place where they want, because silently adding aliases to
// someone's shell is exactly the kind of surprise this tool exists to avoid.
func (s *Store) ShellDefinitions(shell string) (string, error) {
	aliases, err := s.Aliases()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, a := range aliases {
		switch shell {
		case "fish":
			fmt.Fprintf(&b, "alias %s %s\n", a.Name, singleQuote(a.Command))
		case "pwsh", "powershell":
			fmt.Fprintf(&b, "function %s { %s @args }\n", a.Name, a.Command)
		case "nu":
			fmt.Fprintf(&b, "alias %s = %s\n", a.Name, a.Command)
		default:
			fmt.Fprintf(&b, "alias %s=%s\n", a.Name, singleQuote(a.Command))
		}
	}
	return b.String(), nil
}

func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wut-saved-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(name, path)
}
