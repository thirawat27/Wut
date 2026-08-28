package shell

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/port"
)

// Manager installs and removes the managed block.
//
// Every write is preceded by a backup and is idempotent: re-running replaces
// the block in place rather than appending a second one. Uninstall must leave
// the file byte-identical to what was there before install, which is asserted
// by test rather than assumed.
type Manager struct {
	Home   string
	Params Params
}

var _ port.ShellIntegration = (*Manager)(nil)

// New builds a manager.
func New(home string, p Params) *Manager { return &Manager{Home: home, Params: p} }

// Detect lists the shells on this machine and what each can deliver.
func (m *Manager) Detect() ([]port.DetectedShell, error) {
	var out []port.DetectedShell
	for _, d := range Detect(m.Home) {
		out = append(out, port.DetectedShell{
			Name:      d.Spec.Name,
			RCFile:    d.RCFile,
			Class:     string(d.Spec.Class),
			Tier:      d.Spec.Tier,
			Active:    d.Active,
			Installed: d.Installed,
			Legacy:    d.Legacy,
		})
	}
	return out, nil
}

// Render returns the block that would be written for a shell.
func (m *Manager) Render(name string) (string, error) {
	spec, ok := Lookup(name)
	if !ok {
		return "", fmt.Errorf("unknown shell %q", name)
	}
	return Render(spec, m.Params), nil
}

// Install writes or updates the managed block.
func (m *Manager) Install(req port.InstallRequest) (port.InstallReport, error) {
	return m.apply(req, false)
}

// Uninstall removes it.
func (m *Manager) Uninstall(req port.InstallRequest) (port.InstallReport, error) {
	return m.apply(req, true)
}

func (m *Manager) apply(req port.InstallRequest, remove bool) (port.InstallReport, error) {
	params := m.Params
	if req.Alias != "" {
		params.Alias = req.Alias
	}
	// The alias reaches generated shell code as a function name, which no
	// quoting can protect. Config validation already refuses a bad one, but
	// --alias arrives here without passing through it, and this is the last
	// point before a startup file is written.
	if err := config.ValidateAlias(params.Alias); err != nil {
		return port.InstallReport{}, fmt.Errorf("alias: %w", err)
	}

	targets := req.Shells
	if len(targets) == 0 {
		for _, d := range Detect(m.Home) {
			if d.Spec.Class == ClassManual {
				continue // nothing to write; there is no hook to install
			}
			targets = append(targets, d.Spec.Name)
		}
	}

	var rep port.InstallReport
	for _, name := range targets {
		rep.Changes = append(rep.Changes, m.applyOne(name, params, req.DryRun, remove))
	}
	return rep, nil
}

func (m *Manager) applyOne(name string, params Params, dryRun, remove bool) port.InstallChange {
	spec, ok := Lookup(name)
	if !ok {
		return port.InstallChange{Shell: name, Action: "skipped", Err: "unknown shell"}
	}
	change := port.InstallChange{Shell: spec.Name}

	if spec.Class == ClassManual {
		change.Action = "skipped"
		change.Message = "manual class: " + spec.Notes
		return change
	}

	rc, _ := rcPathFor(spec, m.Home)
	if rc == "" {
		change.Action = "skipped"
		change.Err = "no startup file for this shell on this platform"
		return change
	}
	change.RCFile = rc

	existing, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		change.Action = "skipped"
		change.Err = err.Error()
		return change
	}

	updated, err := editBlock(string(existing), spec, params, remove)
	if err != nil {
		change.Action = "skipped"
		change.Err = err.Error()
		return change
	}

	if updated == string(existing) {
		change.Action = "unchanged"
		return change
	}
	change.Diff = summariseDiff(string(existing), updated)

	if dryRun {
		change.Action = "would-" + verb(remove, len(existing) > 0 && strings.Contains(string(existing), blockBegin))
		return change
	}

	// Keep whatever mode the file already had. A startup file is the user's,
	// and quietly tightening a 0644 .bashrc to 0600 is a change nobody asked
	// for and nobody is told about.
	perm := os.FileMode(0o600)
	if info, err := os.Stat(rc); err == nil {
		perm = info.Mode().Perm()
	}

	if len(existing) > 0 {
		backup, err := writeBackup(rc, existing)
		if err != nil {
			change.Action = "skipped"
			change.Err = "backup failed: " + err.Error()
			return change
		}
		change.Backup = backup
	}

	if err := writeFileAtomic(rc, []byte(updated), perm); err != nil {
		change.Action = "skipped"
		change.Err = err.Error()
		// Put the original back: a half-written rc file can stop a shell from
		// starting, which is the worst outcome this package can produce.
		if change.Backup != "" {
			_ = os.WriteFile(rc, existing, perm)
		}
		return change
	}
	change.Action = verb(remove, strings.Contains(string(existing), blockBegin))
	return change
}

func verb(remove, had bool) string {
	switch {
	case remove:
		return "removed"
	case had:
		return "updated"
	default:
		return "installed"
	}
}

// ErrMalformedBlock reports a managed block that starts and never ends.
//
// It is returned rather than worked around. Guessing where a hand-edited block
// stops risks deleting the rest of someone's startup file, and appending a
// second block beside it leaves two copies of the hook installed — which was
// the old behaviour, and produced doubled records with no visible cause.
var ErrMalformedBlock = errors.New(
	"this file has a `" + blockBegin + "` marker with no matching `" + blockEnd + "`. " +
		"Repair or delete that block by hand, then run this again")

// blockState is what findBlock found.
type blockState int

const (
	blockAbsent    blockState = iota // no marker at all: append a new one
	blockFound                       // a complete block: replace it in place
	blockMalformed                   // a begin with no end: refuse to touch it
)

// editBlock returns the new contents of a startup file.
func editBlock(content string, spec Spec, params Params, remove bool) (string, error) {
	start, end, state := findBlock(content, spec)
	if state == blockMalformed {
		return "", ErrMalformedBlock
	}
	if remove {
		return removeBlock(content, start, end, state), nil
	}
	return upsertBlock(content, start, end, state, Render(spec, params)), nil
}

// upsertBlock replaces an existing managed block or appends a new one.
func upsertBlock(content string, start, end int, state blockState, block string) string {
	if state == blockFound {
		return content[:start] + block + content[end:]
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n" // the separator blank line, removed again on uninstall
	}
	return content + block
}

// removeBlock deletes the managed block and the blank line that was added with
// it.
//
// The separator is removed by inverting exactly what upsertBlock added — one
// newline, and only when the block was the last thing in the file. Trimming
// every trailing newline instead was wrong in a way the tests missed: it
// silently ate blank lines the user had put at the end of their own file.
func removeBlock(content string, start, end int, state blockState) string {
	if state != blockFound {
		return content
	}
	out := content[:start] + content[end:]
	if content[end:] == "" && strings.HasSuffix(out, "\n") {
		out = out[:len(out)-1]
	}
	return out
}

// findBlock locates the byte range of the managed block, markers included.
func findBlock(content string, spec Spec) (start, end int, state blockState) {
	beginMarker := spec.Comment + " " + blockBegin
	endMarker := spec.Comment + " " + blockEnd

	i := strings.Index(content, beginMarker)
	if i < 0 {
		return 0, 0, blockAbsent
	}
	j := strings.Index(content[i:], endMarker)
	if j < 0 {
		return 0, 0, blockMalformed
	}
	end = i + j + len(endMarker)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return i, end, blockFound
}

// writeBackup keeps a timestamped copy next to the original.
func writeBackup(path string, content []byte) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	backup := path + ".wut-backup-" + stamp
	if err := os.WriteFile(backup, content, 0o600); err != nil {
		return "", err
	}
	return backup, nil
}

// summariseDiff reports the line delta rather than a full patch, which keeps
// --dry-run readable while still saying exactly how much changes.
func summariseDiff(before, after string) string {
	b := bytes.Count([]byte(before), []byte("\n"))
	a := bytes.Count([]byte(after), []byte("\n"))
	switch {
	case a > b:
		return fmt.Sprintf("+%d lines", a-b)
	case b > a:
		return fmt.Sprintf("-%d lines", b-a)
	default:
		return "same line count, content changed"
	}
}

// writeFileAtomic writes through a temp file in the same directory.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wut-rc-*")
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
	_ = os.Chmod(name, perm)
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(name, path)
}
