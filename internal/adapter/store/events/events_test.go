package events

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/event"
)

func newStore(t *testing.T, mutate func(*config.Config)) *Store {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Redaction is the load-bearing part of this package. T1 capture reads command
// output, output contains credentials, and a leak here is written to disk in
// plain text and kept.
func TestRedactionCatchesRealCredentialShapes(t *testing.T) {
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{
		"an AWS access key id":    "error: AKIAIOSFODNN7EXAMPLE is not authorized",
		"a GitHub token":          "remote: ghp_16CharsAndThenSomeMoreABCDEFGH rejected",
		"an OpenAI key":           "auth failed for sk-abcdefghijklmnopqrstuvwxyz012345",
		"a Slack token":           "xoxb-123456789012-abcdefghijkl failed",
		"a JWT":                   "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",
		"a private key header":    "-----BEGIN RSA PRIVATE KEY----- MIIEow",
		"a bearer header":         "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456",
		"a password assignment":   "PGPASSWORD=hunter2correcthorse psql",
		"an api key assignment":   "api_key: 9f8e7d6c5b4a3210deadbeef",
		"credentials in a url":    "fatal: could not read https://alice:s3cr3t@example.com/repo.git",
		"a secret with a colon":   "secret: topsecretvalue",
		"an access-key with dash": "access-key = AKIAIOSFODNN7EXAMPLE",
	}
	for name, text := range secrets {
		t.Run(name, func(t *testing.T) {
			got := r.Redact(text)
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("nothing was redacted from %q", got)
			}
			for _, leak := range []string{
				"AKIAIOSFODNN7EXAMPLE", "ghp_16CharsAndThenSomeMoreABCDEFGH",
				"sk-abcdefghijklmnopqrstuvwxyz012345", "xoxb-123456789012-abcdefghijkl",
				"hunter2correcthorse", "9f8e7d6c5b4a3210deadbeef", "s3cr3t",
				"topsecretvalue", "abcdefghijklmnopqrstuvwxyz123456",
			} {
				if strings.Contains(got, leak) {
					t.Errorf("%q survived redaction: %q", leak, got)
				}
			}
		})
	}
}

// Redaction must not eat ordinary output. A tool that mangles every error
// message gets its capture turned off, and then it protects nothing.
func TestRedactionLeavesOrdinaryOutputAlone(t *testing.T) {
	r, _ := NewRedactor(nil)
	for _, text := range []string{
		"fatal: not a git repository",
		"error: pathspec 'main' did not match any file(s) known to git",
		"npm ERR! code ENOENT",
		"ls: cannot access '/nope': No such file or directory",
		"Permission denied (publickey).",
		"docker: Error response from daemon: conflict: unable to delete",
	} {
		if got := r.Redact(text); got != text {
			t.Errorf("redaction changed ordinary output:\n got %q\nwant %q", got, text)
		}
	}
}

func TestUserPatternsAreAdded(t *testing.T) {
	r, err := NewRedactor([]string{`INTERNAL-[0-9]{4}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Redact("ticket INTERNAL-4021 failed"); strings.Contains(got, "4021") {
		t.Errorf("a user pattern did not apply: %q", got)
	}
}

// A bad pattern must be an error naming the pattern, not a silent no-op that
// leaves the user believing they redacted something.
func TestABadUserPatternIsRefused(t *testing.T) {
	_, err := NewRedactor([]string{"([unclosed"})
	if err == nil {
		t.Fatal("an invalid regex was accepted")
	}
	if !strings.Contains(err.Error(), "capture.redact") {
		t.Errorf("the error does not name the setting: %v", err)
	}
}

// The session id comes from a shell variable, so it is user-controlled input
// even when the user is not an attacker. A traversal here writes outside the
// state directory.
func TestSessionIdCannotEscapeTheDirectory(t *testing.T) {
	s := newStore(t, nil)
	for _, id := range []string{
		"../../etc/passwd", "..\\..\\windows\\system32", "a/b", `a\b`,
		"with space", "semi;colon", "$(whoami)", "",
	} {
		got := s.SessionFile(id)
		dir := filepath.Dir(got)
		if dir != s.SessionsDir() {
			t.Errorf("session %q escaped to %q", id, got)
		}
	}
}

func TestSessionIdIsBounded(t *testing.T) {
	s := newStore(t, nil)
	long := strings.Repeat("a", 500)
	name := filepath.Base(s.SessionFile(long))
	if len(name) > 70 {
		t.Errorf("session file name is %d characters", len(name))
	}
}

func TestAppendAndRead(t *testing.T) {
	s := newStore(t, nil)
	ctx := context.Background()

	for i, raw := range []string{"ls", "git psuh", "pwd"} {
		code := 0
		if i == 1 {
			code = 1
		}
		if err := s.Append(ctx, event.Event{
			Session: "s1", Seq: uint64(i), Raw: raw, ExitCode: code,
			At: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.Recent(ctx, event.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("read %d events, want 3", len(all))
	}
	if all[0].Raw != "pwd" {
		t.Errorf("first event is %q, want the newest", all[0].Raw)
	}

	failed, _ := s.Recent(ctx, event.Filter{FailedOnly: true})
	if len(failed) != 1 || failed[0].Raw != "git psuh" {
		t.Errorf("failed-only returned %v", failed)
	}

	last, ok, _ := s.Last(ctx, event.Filter{Correctable: true})
	if !ok || last.Raw != "git psuh" {
		t.Errorf("Last(correctable) = %q, %v", last.Raw, ok)
	}
}

// With capture off, nothing is written. This is the setting people actually
// check before trusting the tool, so it has to be true rather than nearly true.
func TestHistoryDisabledWritesNothing(t *testing.T) {
	s := newStore(t, func(c *config.Config) { c.History.Enabled = false })
	ctx := context.Background()

	if err := s.Append(ctx, event.Event{Raw: "ls", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Recent(ctx, event.Filter{})
	if len(all) != 0 {
		t.Errorf("history is disabled but %d events were stored", len(all))
	}
}

// Below T1, stderr must never reach disk — not truncated, not redacted, not
// at all.
func TestOutputIsDroppedBelowT1(t *testing.T) {
	s := newStore(t, func(c *config.Config) { c.Capture.Tier = config.TierT05 })
	ctx := context.Background()

	err := s.Append(ctx, event.Event{
		Raw: "deploy", Stderr: "token ghp_16CharsAndThenSomeMoreABCDEFGH", At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := s.Recent(ctx, event.Filter{})
	if len(all) != 1 {
		t.Fatal("the event was not stored")
	}
	if all[0].Stderr != "" {
		t.Errorf("stderr was kept at tier T0.5: %q", all[0].Stderr)
	}
}

func TestCapturedOutputIsRedactedOnTheWayIn(t *testing.T) {
	s := newStore(t, func(c *config.Config) { c.Capture.Tier = config.TierT1 })
	ctx := context.Background()

	if err := s.Append(ctx, event.Event{
		Raw: "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456' x",
		At:  time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Read the file itself, not the API. The guarantee is about what is on
	// disk, and an API that filtered on read would still have written it.
	data, err := os.ReadFile(filepath.Join(s.SessionsDir(), "..", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "abcdefghijklmnopqrstuvwxyz123456") {
		t.Error("a bearer token was written to disk in plain text")
	}
}

// Purge is the entire privacy story in one verb, so it has to leave nothing:
// not the log, not the session records the shell is still appending to.
func TestPurgeRemovesEverything(t *testing.T) {
	s := newStore(t, nil)
	ctx := context.Background()

	_ = s.Append(ctx, event.Event{Session: "s1", Raw: "ls", At: time.Now()})
	record := s.SessionFile("s1")
	if err := os.WriteFile(record, []byte(event.FormatRecord(event.Event{Raw: "pwd"})), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := s.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purge reported %d events removed, want 1", n)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Error("purge left the session record behind")
	}
	if all, _ := s.Recent(ctx, event.Filter{}); len(all) != 0 {
		t.Errorf("purge left %d events", len(all))
	}
}

// Ingest folds the shell's records into the log. Running it twice must not
// duplicate them, or every `wut` invocation grows the history.
func TestIngestIsIdempotent(t *testing.T) {
	s := newStore(t, nil)
	ctx := context.Background()

	var body strings.Builder
	for i := 1; i <= 3; i++ {
		body.WriteString(event.FormatRecord(event.Event{
			Seq: uint64(i), Raw: "cmd", At: time.Now(), Tier: event.TierT0,
		}))
	}
	if err := os.WriteFile(s.SessionFile("s1"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := s.Ingest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != 3 {
		t.Fatalf("ingested %d records, want 3", first)
	}
	second, err := s.Ingest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Errorf("a second ingest took %d more records; the log now double-counts", second)
	}
	if all, _ := s.Recent(ctx, event.Filter{}); len(all) != 3 {
		t.Errorf("the log holds %d events after two ingests", len(all))
	}
}

// A corrupt line must not take the whole log down. The file is appended to by
// a process that can be killed mid-write.
func TestACorruptLineIsSkipped(t *testing.T) {
	s := newStore(t, nil)
	ctx := context.Background()
	_ = s.Append(ctx, event.Event{Raw: "ls", At: time.Now()})

	path := filepath.Join(s.SessionsDir(), "..", "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json at all\n")
	f.Close()

	all, err := s.Recent(ctx, event.Filter{})
	if err != nil {
		t.Fatalf("a corrupt line failed the whole read: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("read %d events, want the one good record", len(all))
	}
}

func TestTheLogIsBounded(t *testing.T) {
	s := newStore(t, func(c *config.Config) { c.History.MaxEntries = 10 })
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		if err := s.Append(ctx, event.Event{Seq: uint64(i), Raw: "cmd", At: time.Now()}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	all, _ := s.Recent(ctx, event.Filter{})

	// The bound is max plus a tenth: the file is only rewritten once it is
	// meaningfully over, so a busy shell is not rewriting the whole log on
	// every command. Anything beyond that means trimming is not happening.
	if limit := 10 + 10/10; len(all) > limit {
		t.Errorf("the log holds %d events with max_entries 10 (bound %d)", len(all), limit)
	}
	if len(all) < 10 {
		t.Errorf("trimming cut to %d, below max_entries", len(all))
	}
}

// Captured output expires. Keeping it past the retention window would make the
// setting a suggestion rather than a promise.
func TestOutputExpires(t *testing.T) {
	cutoff := time.Now().Add(-time.Hour)
	old := expireOutput(event.Event{
		At: cutoff.Add(-time.Minute), Stderr: "secret-ish", StderrTrunc: true,
	}, cutoff)
	if old.Stderr != "" || old.StderrTrunc {
		t.Errorf("output outlived its retention: %+v", old)
	}

	fresh := expireOutput(event.Event{At: time.Now(), Stderr: "still here"}, cutoff)
	if fresh.Stderr != "still here" {
		t.Error("output inside the retention window was dropped")
	}
}
