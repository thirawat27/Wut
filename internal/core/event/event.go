// Package event is what the shell tells WUT about a command that already ran,
// and the wire format it says it in.
//
// This is the ground truth the prototype never had. Without it, correcting a
// failed command means either re-running it — which is what made the
// prototype's `oops` push twice — or asking the user to pipe the error in by
// hand.
//
// The record format is unusual on purpose. The shell hook must write it with
// builtins only: spawning `wut record` after every command would cost a fork
// per prompt, which users notice and uninstall over. So fields are separated
// by 0x1F and records terminated by 0x1E, with the raw command last. That
// ordering means a command containing a newline needs no escaping at all,
// which is what makes a pure-printf hook possible in bash.
package event

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/core/cmdline"
)

// Separators. Chosen because no shell produces them and no command contains
// them, so neither ever has to be escaped.
const (
	unitSep   = '\x1f'
	recordSep = '\x1e'
)

// SchemaVersion is field 1 of every record. A record with an unknown version
// is skipped rather than treated as an error: an old shell hook in an rc file
// must not break a newer binary.
const SchemaVersion = 1

// CaptureTier records how much the shell was willing to tell us.
type CaptureTier string

const (
	TierNone CaptureTier = "none"
	TierT0   CaptureTier = "T0"
	TierT05  CaptureTier = "T0.5"
	TierT1   CaptureTier = "T1"
	TierT2   CaptureTier = "T2" // the user piped output in explicitly
)

// Event is one command the shell observed.
type Event struct {
	Session     string        `json:"session"`
	Seq         uint64        `json:"seq"`
	At          time.Time     `json:"at"`
	Duration    time.Duration `json:"duration"`
	ExitCode    int           `json:"exit_code"`
	Shell       string        `json:"shell"`
	Cwd         string        `json:"cwd"`
	Tier        CaptureTier   `json:"tier"`
	Raw         string        `json:"raw"`
	Stderr      string        `json:"stderr,omitempty"`
	StderrTrunc bool          `json:"stderr_truncated,omitempty"`
	// NotFound is set by the T0.5 command-not-found hook, which is a callback
	// the shell offers rather than anything WUT intercepts.
	NotFound string `json:"not_found,omitempty"`
}

// Failed reports a command that did not succeed. 130 is Ctrl-C, which is the
// user changing their mind rather than something to correct.
func (e Event) Failed() bool { return e.ExitCode != 0 && e.ExitCode != 130 }

// Correctable reports an event worth offering a correction for.
func (e Event) Correctable() bool {
	return e.Failed() && strings.TrimSpace(e.Raw) != "" && !isWutInvocation(e.Raw)
}

// isWutInvocation keeps WUT from trying to correct itself, which is what the
// `oops`-skipping-`oops` heuristics in the prototype's bash hook were working
// around. The session record carries the command WUT was asked about, so this
// check exists once, in Go, instead of once per shell in shell script.
func isWutInvocation(raw string) bool {
	// The program name is taken from the parser rather than from
	// strings.Fields, because a quoted path with a space in it — which is
	// where WUT installs on Windows by default — splits into "C:\Program and
	// defeats the check entirely.
	name := cmdline.Parse(raw).Program
	if name == "" {
		return false
	}
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	return name == "wut"
}

// Filter selects events.
type Filter struct {
	Session     string
	Cwd         string
	FailedOnly  bool
	Correctable bool
	Since       time.Time
	Limit       int
}

// Matches reports whether an event passes the filter.
func (f Filter) Matches(e Event) bool {
	switch {
	case f.Session != "" && e.Session != f.Session:
		return false
	case f.Cwd != "" && e.Cwd != f.Cwd:
		return false
	case f.FailedOnly && !e.Failed():
		return false
	case f.Correctable && !e.Correctable():
		return false
	case !f.Since.IsZero() && e.At.Before(f.Since):
		return false
	}
	return true
}

// FormatRecord renders an event in the wire format, for tests and for any
// caller that is not a shell builtin.
func FormatRecord(e Event) string {
	fields := []string{
		strconv.Itoa(SchemaVersion),
		strconv.FormatUint(e.Seq, 10),
		strconv.FormatInt(e.At.UnixMilli(), 10),
		strconv.FormatInt(e.Duration.Milliseconds(), 10),
		strconv.Itoa(e.ExitCode),
		e.Shell,
		e.Cwd,
		string(e.Tier),
		e.NotFound,
		e.Raw, // last: may contain anything, including newlines
	}
	return strings.Join(fields, string(unitSep)) + string(recordSep)
}

// fieldCount is how many fields version 1 has.
const fieldCount = 10

// ErrShortRecord reports a record with too few fields to be usable.
var ErrShortRecord = errors.New("record has too few fields")

// ParseRecords decodes every complete record in a buffer.
//
// A trailing partial record is ignored rather than reported: the shell may be
// mid-write, and a half-written record is a normal transient state, not
// corruption.
func ParseRecords(data string, session string) []Event {
	var out []Event
	for _, chunk := range strings.Split(data, string(recordSep)) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		e, err := ParseRecord(chunk, session)
		if err != nil {
			continue // unknown version or malformed: skip, never fail the read
		}
		out = append(out, e)
	}
	return out
}

// ParseRecord decodes one record body, without its terminator.
func ParseRecord(chunk, session string) (Event, error) {
	// A record may be preceded by whitespace left over from the previous
	// split; the fields themselves are exact.
	chunk = strings.TrimLeft(chunk, "\r\n")
	fields := strings.SplitN(chunk, string(unitSep), fieldCount)
	if len(fields) < fieldCount {
		return Event{}, ErrShortRecord
	}
	version, err := strconv.Atoi(fields[0])
	if err != nil {
		return Event{}, fmt.Errorf("bad version %q", fields[0])
	}
	if version != SchemaVersion {
		return Event{}, fmt.Errorf("unsupported record version %d", version)
	}
	e := Event{Session: session, Shell: fields[5], Cwd: fields[6], Tier: CaptureTier(fields[7]), NotFound: fields[8], Raw: fields[9]}
	e.Seq, _ = strconv.ParseUint(fields[1], 10, 64)
	if ms, err := strconv.ParseInt(fields[2], 10, 64); err == nil && ms > 0 {
		e.At = time.UnixMilli(ms)
	}
	if ms, err := strconv.ParseInt(fields[3], 10, 64); err == nil && ms >= 0 {
		e.Duration = time.Duration(ms) * time.Millisecond
	}
	e.ExitCode, _ = strconv.Atoi(fields[4])
	if e.Tier == "" {
		e.Tier = TierT0
	}
	return e, nil
}

// UnitSeparator and RecordSeparator are exported so the shell hook generator
// can emit the exact bytes without redefining them.
func UnitSeparator() string   { return string(unitSep) }
func RecordSeparator() string { return string(recordSep) }
