// Package events stores what the shell reported.
//
// The store is an append-only JSON-lines file with a ring bound, not a
// database. That is a deliberate choice for this workload: writes are
// append-only and single-record, reads are almost always "the last failure in
// this session", and the whole file is smaller than a B-tree's page cache
// would be. A key-value store here would buy transactions nothing needs and
// cost a dependency, a file format, and a corruption-recovery story.
//
// Session record files — the ones the shell hook writes with builtins only —
// are ingested from here. See Ingest.
package events

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/port"
)

// Limits. Each one exists because the input is untrusted: the raw command
// comes from a human, and stderr comes from an arbitrary program.
const (
	maxRawBytes     = 8 << 10
	maxStderrBytes  = 16 << 10
	maxLineBytes    = 64 << 10
	sessionSweepAge = 7 * 24 * time.Hour
)

// Store is the event log.
type Store struct {
	mu sync.Mutex

	path     string
	sessions string
	cfg      config.Config
	redactor *Redactor
}

var _ port.EventStore = (*Store)(nil)

// New opens the store rooted at a state directory.
func New(stateDir string, cfg config.Config) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	sessions := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		return nil, err
	}
	red, err := NewRedactor(cfg.Capture.Redact)
	if err != nil {
		return nil, err
	}
	return &Store{
		path:     filepath.Join(stateDir, "events.jsonl"),
		sessions: sessions,
		cfg:      cfg,
		redactor: red,
	}, nil
}

// SessionsDir is where the shell hook writes its records.
func (s *Store) SessionsDir() string { return s.sessions }

// SessionFile is the record file for one session id.
func (s *Store) SessionFile(session string) string {
	return filepath.Join(s.sessions, sanitizeSession(session)+".rec")
}

// sanitizeSession keeps a session id from escaping the sessions directory.
// The id comes from a shell variable, so it is user-controlled input even
// though the user is not an attacker.
func sanitizeSession(id string) string {
	if id == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// Append writes one event.
func (s *Store) Append(ctx context.Context, e event.Event) error {
	if !s.cfg.History.Enabled {
		return nil
	}
	e = s.sanitize(e)

	s.mu.Lock()
	defer s.mu.Unlock()

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := appendLine(s.path, line); err != nil {
		return err
	}
	// Trim only after the handle is closed. Trimming rewrites the file by
	// removing and renaming, and Windows refuses to remove a file that is
	// still open — so with the write handle held open by a defer, trimming
	// failed on every append and the log grew without bound on the one
	// platform where nobody looks at ~/.local/state.
	return s.trimLocked()
}

// appendLine writes one line and closes the file before returning, so no
// caller can hold a handle open across a rewrite.
func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// sanitize bounds and redacts an event before it touches disk.
func (s *Store) sanitize(e event.Event) event.Event {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if len(e.Raw) > maxRawBytes {
		e.Raw = e.Raw[:maxRawBytes]
	}
	if !s.cfg.CapturesOutput() {
		e.Stderr = ""
	}
	if len(e.Stderr) > maxStderrBytes {
		// Keep the tail: an error message ends with the part that explains it.
		e.Stderr = e.Stderr[len(e.Stderr)-maxStderrBytes:]
		e.StderrTrunc = true
	}
	e.Raw = s.redactor.Redact(e.Raw)
	e.Stderr = s.redactor.Redact(e.Stderr)
	return e
}

// Recent returns matching events, newest first.
func (s *Store) Recent(ctx context.Context, f event.Filter) ([]event.Event, error) {
	all, err := s.readAll()
	if err != nil {
		return nil, err
	}
	cutoff := s.expiryCutoff()

	var out []event.Event
	for i := len(all) - 1; i >= 0; i-- {
		e := expireOutput(all[i], cutoff)
		if !f.Matches(e) {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// Last returns the newest matching event.
func (s *Store) Last(ctx context.Context, f event.Filter) (event.Event, bool, error) {
	f.Limit = 1
	got, err := s.Recent(ctx, f)
	if err != nil || len(got) == 0 {
		return event.Event{}, false, err
	}
	return got[0], true, nil
}

// expiryCutoff is when captured output stops being readable.
//
// Retention is enforced on read as well as on write, so output is unavailable
// the moment it expires rather than whenever a sweep next happens to run.
func (s *Store) expiryCutoff() time.Time {
	if s.cfg.Capture.Retention <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-s.cfg.Capture.Retention)
}

func expireOutput(e event.Event, cutoff time.Time) event.Event {
	if !cutoff.IsZero() && e.At.Before(cutoff) {
		e.Stderr = ""
		e.StderrTrunc = false
	}
	return e
}

// Purge deletes every event and every session record.
//
// This is the whole of the privacy story in one command: not a settings page,
// not a per-item delete, one verb that empties everything WUT ever wrote.
func (s *Store) Purge(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, _ := s.readAllLocked()
	n := len(all)

	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	entries, err := os.ReadDir(s.sessions)
	if err != nil {
		return n, nil
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(s.sessions, e.Name()))
	}
	return n, nil
}

// Stats describes the log.
func (s *Store) Stats(ctx context.Context) (port.EventStats, error) {
	all, err := s.readAll()
	if err != nil {
		return port.EventStats{}, err
	}
	st := port.EventStats{
		Events:       len(all),
		CaptureTier:  string(s.cfg.Capture.Tier),
		RetentionHrs: s.cfg.Capture.Retention.Hours(),
	}
	for _, e := range all {
		if e.Stderr != "" {
			st.WithOutput++
		}
	}
	if len(all) > 0 {
		st.Oldest, st.Newest = all[0].At, all[len(all)-1].At
	}
	if info, err := os.Stat(s.path); err == nil {
		st.SizeBytes = info.Size()
	}
	if entries, err := os.ReadDir(s.sessions); err == nil {
		st.SessionsOpen = len(entries)
	}
	return st, nil
}

// readAll returns every event, oldest first.
func (s *Store) readAll() ([]event.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAllLocked()
}

func (s *Store) readAllLocked() ([]event.Event, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []event.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e event.Event
		if json.Unmarshal(line, &e) != nil {
			// One unreadable line must not lose the rest of the log. This is a
			// local file that a crash can truncate mid-write; skipping is the
			// only behaviour that keeps history usable afterwards.
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// trimLocked enforces the ring bound. Enforcing on write rather than by a
// sweeper means a machine that is never idle still stays bounded.
func (s *Store) trimLocked() error {
	max := s.cfg.History.MaxEntries
	if max <= 0 {
		return nil
	}
	all, err := s.readAllLocked()
	if err != nil || len(all) <= max+max/10 {
		// Rewrite only when meaningfully over, so a busy shell is not
		// rewriting the whole file on every command.
		return err
	}
	keep := all[len(all)-max:]
	return s.rewriteLocked(keep)
}

func (s *Store) rewriteLocked(keep []event.Event) error {
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".wut-events-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	w := bufio.NewWriter(tmp)
	for _, e := range keep {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		w.Write(line)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
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
	_ = os.Chmod(name, 0o600)
	_ = os.Remove(s.path) // Windows will not rename over an existing file
	return os.Rename(name, s.path)
}

// Ingest reads the session record files the shell wrote and appends anything
// new to the log.
//
// This is the other half of the zero-spawn design: the shell pays one printf,
// and the cost of turning those records into events is paid here, once, by
// whichever WUT invocation happens next.
func (s *Store) Ingest(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.sessions)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	lastSeq := s.lastSeqBySession()
	added := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rec") {
			continue
		}
		path := filepath.Join(s.sessions, entry.Name())
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > sessionSweepAge {
			_ = os.Remove(path)
			continue
		}
		session := strings.TrimSuffix(entry.Name(), ".rec")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, e := range event.ParseRecords(string(data), session) {
			if e.Seq <= lastSeq[session] {
				continue
			}
			if err := s.Append(ctx, e); err == nil {
				added++
				lastSeq[session] = e.Seq
			}
		}
	}
	return added, nil
}

// lastSeqBySession is how ingestion stays idempotent: records are re-read
// every time, and only sequence numbers past the high-water mark are kept.
func (s *Store) lastSeqBySession() map[string]uint64 {
	out := map[string]uint64{}
	all, err := s.readAll()
	if err != nil {
		return out
	}
	for _, e := range all {
		if e.Seq > out[e.Session] {
			out[e.Session] = e.Seq
		}
	}
	return out
}

// Sweep removes session files whose owning shell is gone.
func (s *Store) Sweep() error {
	entries, err := os.ReadDir(s.sessions)
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > sessionSweepAge {
			stale = append(stale, filepath.Join(s.sessions, e.Name()))
		}
	}
	sort.Strings(stale)
	for _, p := range stale {
		_ = os.Remove(p)
	}
	return nil
}

// Redactor strips secrets before anything is written.
type Redactor struct {
	patterns []*regexp.Regexp
}

// builtinRedactions are on by default. The list is aimed at what actually
// appears in terminal output — tokens echoed by a failing CLI, a key pasted
// into a command — rather than at being exhaustive.
var builtinRedactions = []string{
	`AKIA[0-9A-Z]{16}`,
	`gh[pousr]_[A-Za-z0-9]{20,}`,
	`sk-[A-Za-z0-9_-]{20,}`,
	`xox[baprs]-[A-Za-z0-9-]{10,}`,
	`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`,
	`-----BEGIN [A-Z ]*PRIVATE KEY-----`,
	`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{20,}`,
	// A leading run of word characters is allowed before the keyword, because
	// the shapes that actually appear in a shell are PGPASSWORD=,
	// MYSQL_PWD=, NPM_TOKEN=, and GITHUB_TOKEN=. Anchoring on a word boundary
	// matched none of them: there is no boundary inside PGPASSWORD.
	`(?i)[a-z0-9_]*(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)\s*[=:]\s*\S+`,
	`(?i)\bauthorization\s*:\s*\S+`,
	`(?i)://[^:/@\s]+:[^@\s]+@`,
}

// NewRedactor compiles the built-in patterns plus any the user added.
func NewRedactor(extra []string) (*Redactor, error) {
	r := &Redactor{}
	for _, p := range append(append([]string{}, builtinRedactions...), extra...) {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("capture.redact: %q: %w", p, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// Redact replaces every match with a marker.
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, "[redacted]")
	}
	return s
}
