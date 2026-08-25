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

	var updated string
	if remove {
		updated = removeBlock(string(existing), spec)
	} else {
		updated = upsertBlock(string(existing), spec, Render(spec, params))
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

	if len(existing) > 0 {
		backup, err := writeBackup(rc, existing)
		if err != nil {
			change.Action = "skipped"
			change.Err = "backup failed: " + err.Error()
			return change
		}
		change.Backup = backup
	}

	if err := writeFileAtomic(rc, []byte(updated), 0o600); err != nil {
		change.Action = "skipped"
		change.Err = err.Error()
		// Put the original back: a half-written rc file can stop a shell from
		// starting, which is the worst outcome this package can produce.
		if change.Backup != "" {
			_ = os.WriteFile(rc, existing, 0o600)
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

// upsertBlock replaces an existing managed block or appends a new one.
func upsertBlock(content string, spec Spec, block string) string {
	start, end, ok := findBlock(content, spec)
	if ok {
		return content[:start] + block + content[end:]
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n"
	}
	return content + block
}

// removeBlock deletes the managed block and the blank line that was added with
// it, so uninstall returns the file to exactly what it was.
func removeBlock(content string, spec Spec) string {
	start, end, ok := findBlock(content, spec)
	if !ok {
		return content
	}
	out := content[:start] + content[end:]
	// Collapse the separator blank line introduced at install time.
	out = strings.TrimRight(out, "\n")
	if out != "" {
		out += "\n"
	}
	return out
}

// findBlock locates the byte range of the managed block, markers included.
func findBlock(content string, spec Spec) (start, end int, ok bool) {
	beginMarker := spec.Comment + " " + blockBegin
	endMarker := spec.Comment + " " + blockEnd

	i := strings.Index(content, beginMarker)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(content[i:], endMarker)
	if j < 0 {
		// A begin with no end means someone edited the block by hand. Refuse
		// to guess where it stops rather than deleting the rest of their file.
		return 0, 0, false
	}
	end = i + j + len(endMarker)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return i, end, true
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
