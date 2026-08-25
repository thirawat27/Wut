package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// The interactive loop cannot be tested without a terminal, but the part that
// decides what happens can: handle() is a pure transition over state, and
// every key that matters is checked here rather than by someone noticing later
// that Escape stopped working.

func testUI(searched *[]string) *UI {
	return &UI{
		Style: NewStyle(false, 80),
		Search: func(q string) ([]candidate.Candidate, error) {
			if searched != nil {
				*searched = append(*searched, q)
			}
			if q == "boom" {
				return nil, errors.New("index is damaged")
			}
			return []candidate.Candidate{
				candidate.New(candidate.KindRecall, "tar czf out.tar.gz src",
					candidate.Provenance{Ref: "tar"},
					candidate.Why{Code: "t", Text: "matched", Weight: 1}),
				candidate.New(candidate.KindRecall, "zip -r out.zip src",
					candidate.Provenance{Ref: "zip"},
					candidate.Why{Code: "t", Text: "matched", Weight: 0.5}),
			}, nil
		},
		History: func() ([]Entry, error) {
			return []Entry{
				{Command: "git psuh", ExitCode: 1, At: time.Unix(0, 0)},
				{Command: "ls", ExitCode: 0, At: time.Unix(0, 0)},
			}, nil
		},
		Page: func(name string) (knowledge.Page, bool) {
			return knowledge.Page{Name: name, Description: "an archiver"}, true
		},
	}
}

func rune_(r rune) tty.Press { return tty.Press{Key: tty.KeyRune, Rune: r} }

func typeText(u *UI, st *state, text string) {
	for _, r := range text {
		u.handle(st, rune_(r))
	}
}

func TestTypingSearches(t *testing.T) {
	var searched []string
	u := testUI(&searched)
	st := &state{}

	typeText(u, st, "tar")

	if got := string(st.query); got != "tar" {
		t.Errorf("query = %q, want %q", got, "tar")
	}
	if len(st.results) != 2 {
		t.Fatalf("got %d results, want 2", len(st.results))
	}
	// One search per keystroke: the index is local, and debouncing a
	// memory-mapped lookup buys nothing but a stale screen.
	if len(searched) != 3 {
		t.Errorf("searched %d times for 3 keystrokes: %v", len(searched), searched)
	}
}

func TestBackspaceReSearches(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	typeText(u, st, "tar")
	u.handle(st, tty.Press{Key: tty.KeyBackspace})

	if got := string(st.query); got != "ta" {
		t.Errorf("query = %q, want %q", got, "ta")
	}
	if len(st.results) == 0 {
		t.Error("backspace left the results stale rather than re-searching")
	}
}

// An empty query must clear the results. Leaving the last search on screen
// after the user has deleted their question shows an answer to a question
// nobody is asking.
func TestEmptyQueryClearsResults(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	typeText(u, st, "ab")
	u.handle(st, tty.Press{Key: tty.KeyBackspace})
	u.handle(st, tty.Press{Key: tty.KeyBackspace})

	if len(st.results) != 0 {
		t.Errorf("an empty query left %d results on screen", len(st.results))
	}
}

func TestSearchErrorIsHeldNotPanicked(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	typeText(u, st, "boom")

	if st.err == nil {
		t.Fatal("a failing search left no error to show")
	}
	if len(st.results) != 0 {
		t.Error("a failing search left results on screen")
	}
}

func TestEnterAcceptsTheSelection(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	typeText(u, st, "tar")
	u.handle(st, tty.Press{Key: tty.KeyDown})

	done, out := u.handle(st, tty.Press{Key: tty.KeyEnter})
	if !done {
		t.Fatal("enter did not close the UI")
	}
	if out.Command != "zip -r out.zip src" {
		t.Errorf("accepted %q, want the second candidate", out.Command)
	}
}

func TestEnterWithNothingSelectedDoesNotClose(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	if done, _ := u.handle(st, tty.Press{Key: tty.KeyEnter}); done {
		t.Error("enter on an empty result list closed the UI with nothing")
	}
}

func TestCursorStaysInRange(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	typeText(u, st, "tar")

	for i := 0; i < 10; i++ {
		u.handle(st, tty.Press{Key: tty.KeyDown})
	}
	if st.cursor != len(st.results)-1 {
		t.Errorf("cursor ran past the end: %d of %d", st.cursor, len(st.results))
	}
	for i := 0; i < 10; i++ {
		u.handle(st, tty.Press{Key: tty.KeyUp})
	}
	if st.cursor != 0 {
		t.Errorf("cursor ran past the start: %d", st.cursor)
	}
}

func TestTabCyclesPanes(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	seen := []pane{st.pane}
	for i := 0; i < 3; i++ {
		u.handle(st, tty.Press{Key: tty.KeyTab})
		seen = append(seen, st.pane)
	}
	want := []pane{paneAsk, paneHistory, paneKnowledge, paneAsk}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("pane sequence %v, want %v", seen, want)
		}
	}
}

// Enter in the history pane is the repair path: it loads the command you are
// looking at into the question box rather than accepting it. Accepting a past
// command would hand back the thing that already failed.
func TestEnterInHistoryLoadsTheCommand(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	st.history, _ = u.History()
	u.handle(st, tty.Press{Key: tty.KeyTab})

	done, out := u.handle(st, tty.Press{Key: tty.KeyEnter})
	if done {
		t.Fatalf("enter in the history pane closed the UI with %q", out.Command)
	}
	if got := string(st.query); got != "git psuh" {
		t.Errorf("query = %q, want the selected history entry", got)
	}
	if st.pane != paneAsk {
		t.Error("loading a command did not move focus to the ask pane")
	}
}

func TestQuitKeys(t *testing.T) {
	for name, press := range map[string]tty.Press{
		"escape": {Key: tty.KeyEscape},
		"ctrl-c": {Key: tty.KeyCtrlC},
		"ctrl-d": {Key: tty.KeyCtrlD},
	} {
		u := testUI(nil)
		st := &state{}
		typeText(u, st, "tar")
		done, out := u.handle(st, press)
		if !done {
			t.Errorf("%s did not close the UI", name)
		}
		if out.Command != "" {
			t.Errorf("%s returned %q; quitting must accept nothing", name, out.Command)
		}
	}
}

func TestSaveReportsFailure(t *testing.T) {
	u := testUI(nil)
	u.Save = func(string) error { return errors.New("disk full") }
	st := &state{}
	typeText(u, st, "tar")

	u.handle(st, rune_(0x13)) // Ctrl-S
	if st.status == "" {
		t.Fatal("a failed save said nothing")
	}
	if want := "could not save"; !strings.Contains(st.status, want) {
		t.Errorf("status = %q, want it to contain %q", st.status, want)
	}
}

func TestSaveWithoutAHandlerIsInert(t *testing.T) {
	u := testUI(nil) // no Save
	st := &state{}
	typeText(u, st, "tar")
	u.handle(st, rune_(0x13))

	if st.status != "" {
		t.Errorf("ctrl-S did something without a save handler: %q", st.status)
	}
	if strings.Contains(u.help(paneAsk), "keep") {
		t.Error("the help line offers a key that does nothing")
	}
}

// Control characters must not end up in the question. A stray key producing an
// invisible character in the search box is the kind of bug that reads as
// "search randomly stops working".
func TestControlCharactersAreNotTyped(t *testing.T) {
	u := testUI(nil)
	st := &state{}
	u.handle(st, rune_(0x07))
	u.handle(st, rune_(0x1b))

	if len(st.query) != 0 {
		t.Errorf("query = %q, want it empty", string(st.query))
	}
}
