package event

import (
	"strings"
	"testing"
	"time"
)

// The record format is the whole shell protocol. It is written by shell
// builtins that cannot be unit-tested from Go, so this file tests the only
// half that can be: what Go must accept, and what it must refuse.

func TestRoundTrip(t *testing.T) {
	want := Event{
		Seq:      7,
		At:       time.UnixMilli(1_700_000_000_123),
		Duration: 250 * time.Millisecond,
		ExitCode: 128,
		Shell:    "zsh",
		Cwd:      "/home/someone/src",
		Tier:     TierT05,
		NotFound: "gti",
		Raw:      "gti status",
	}
	got := parseOne(t, FormatRecord(want), "s1")

	got.Session = ""
	if got != want {
		t.Errorf("round trip changed the event:\n got %+v\nwant %+v", got, want)
	}
}

// The raw command is the last field precisely so it can contain anything. If
// this ever stops holding, every hook needs an escaping scheme and the
// fork-free design goes with it.
func TestRawCommandNeedsNoEscaping(t *testing.T) {
	for name, raw := range map[string]string{
		"a newline":        "git commit -m 'first line\nsecond line'",
		"a tab":            "grep -P '\\t' file",
		"quotes":           `echo "it's \"quoted\""`,
		"a unit separator": "echo hello",
		"unicode":          "git commit -m 'ทดสอบ ✓'",
		"a lone backslash": `copy C:\tools\rm.exe D:\`,
	} {
		t.Run(name, func(t *testing.T) {
			got := parseOne(t, FormatRecord(Event{Raw: raw, Tier: TierT0}), "s")
			if got.Raw != raw {
				t.Errorf("raw = %q, want %q", got.Raw, raw)
			}
		})
	}
}

func TestParseRecordsSkipsWhatItCannotUse(t *testing.T) {
	good := FormatRecord(Event{Seq: 1, Raw: "ls", Tier: TierT0})
	other := FormatRecord(Event{Seq: 2, Raw: "pwd", Tier: TierT0})

	cases := map[string]struct {
		data string
		want int
	}{
		"two good records":     {good + other, 2},
		"a truncated trailer":  {good + "1\x1f2\x1fincomplete", 1},
		"an unknown version":   {good + "9\x1f1\x1f0\x1f0\x1f0\x1fsh\x1f/\x1fT0\x1f\x1fls\x1e", 1},
		"a blank line between": {good + "\n" + other, 2},
		"nothing at all":       {"", 0},
		"only whitespace":      {"  \n\t ", 0},
		"garbage":              {"not a record at all", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := len(ParseRecords(tc.data, "s")); got != tc.want {
				t.Errorf("parsed %d records, want %d", got, tc.want)
			}
		})
	}
}

// A record file is appended to by a live shell, so a partial write at the end
// is a normal transient state. Failing the whole read over it would make WUT
// stop working at exactly the moment a command is running.
func TestPartialTrailingRecordIsNotAnError(t *testing.T) {
	complete := FormatRecord(Event{Seq: 1, Raw: "ls", Tier: TierT0})
	events := ParseRecords(complete+"1\x1f2\x1f0", "s")
	if len(events) != 1 || events[0].Raw != "ls" {
		t.Fatalf("got %+v, want just the complete record", events)
	}
}

func TestFailedIgnoresInterrupt(t *testing.T) {
	cases := map[int]bool{0: false, 1: true, 127: true, 130: false, 255: true}
	for code, want := range cases {
		if got := (Event{ExitCode: code}).Failed(); got != want {
			t.Errorf("exit %d: Failed() = %v, want %v", code, got, want)
		}
	}
}

// WUT must never offer to correct itself. Without this the picker can suggest
// a fix for the `wut` invocation that opened the picker.
func TestCorrectableRefusesWutItself(t *testing.T) {
	cases := map[string]bool{
		"git psuh":                   true,
		"":                           false,
		"   ":                        false,
		"wut":                        false,
		"wut fix":                    false,
		"WUT":                        false,
		"/usr/local/bin/wut":         false,
		`"C:\Program Files\wut.exe"`: false, // quoted, as a shell would record it
		`C:\tools\wut.exe --help`:    false,
		"wutil something":            true, // a different program that starts the same
		"mywut":                      true,
	}
	for raw, want := range cases {
		got := Event{ExitCode: 1, Raw: raw}.Correctable()
		if got != want {
			t.Errorf("Correctable(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestFilterMatches(t *testing.T) {
	base := Event{
		Session: "s1", Cwd: "/src", ExitCode: 1, Raw: "git psuh",
		At: time.UnixMilli(1_000_000),
	}
	cases := map[string]struct {
		filter Filter
		want   bool
	}{
		"empty filter matches":     {Filter{}, true},
		"same session":             {Filter{Session: "s1"}, true},
		"different session":        {Filter{Session: "s2"}, false},
		"same cwd":                 {Filter{Cwd: "/src"}, true},
		"different cwd":            {Filter{Cwd: "/elsewhere"}, false},
		"failed only, and it did":  {Filter{FailedOnly: true}, true},
		"correctable":              {Filter{Correctable: true}, true},
		"since before the event":   {Filter{Since: time.UnixMilli(1)}, true},
		"since after the event":    {Filter{Since: time.UnixMilli(2_000_000)}, false},
		"every condition together": {Filter{Session: "s1", Cwd: "/src", FailedOnly: true}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.filter.Matches(base); got != tc.want {
				t.Errorf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

// The hook generator emits these bytes literally, as octal escapes. If they
// ever change, every installed rc block keeps writing the old ones.
func TestSeparatorsAreStable(t *testing.T) {
	if UnitSeparator() != "\x1f" {
		t.Errorf("unit separator = %q, want US (0x1F)", UnitSeparator())
	}
	if RecordSeparator() != "\x1e" {
		t.Errorf("record separator = %q, want RS (0x1E)", RecordSeparator())
	}
	// Neither may appear in ordinary terminal output, or a command that echoed
	// one would forge a record boundary.
	if strings.ContainsAny("abcXYZ0129 \t\n\r-_/\\|&;", UnitSeparator()+RecordSeparator()) {
		t.Error("a separator collides with a character commands actually print")
	}
}

func TestMissingTierDefaultsToT0(t *testing.T) {
	raw := "1\x1f1\x1f0\x1f0\x1f0\x1fsh\x1f/\x1f\x1f\x1fls"
	e, err := ParseRecord(raw, "s")
	if err != nil {
		t.Fatal(err)
	}
	if e.Tier != TierT0 {
		t.Errorf("tier = %q, want %q for a record that named none", e.Tier, TierT0)
	}
}

func TestShortRecordIsRefused(t *testing.T) {
	if _, err := ParseRecord("1\x1f2\x1f3", "s"); err == nil {
		t.Error("a record with three fields was accepted")
	}
}

func parseOne(t *testing.T, record, session string) Event {
	t.Helper()
	events := ParseRecords(record, session)
	if len(events) != 1 {
		t.Fatalf("parsed %d records from %q, want 1", len(events), record)
	}
	return events[0]
}
