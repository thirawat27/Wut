package render

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// UI is the one terminal interface WUT has.
//
// One, not four. The prototype had a TUI in the history command, one in the
// config command, one in the suggestion viewer, and one inside the persistence
// package — four event loops, four key maps, four sets of drawing bugs, and no
// two of them agreed on what Escape did.
//
// It knows nothing about use cases. Every capability arrives as a function, so
// this file cannot reach a store, cannot run a search of its own, and cannot
// grow a second way of doing something the CLI already does.
type UI struct {
	Term  *tty.Terminal
	Style Style

	// Search answers the Ask pane. It is called on each keystroke; the index
	// is local and memory-mapped, so there is nothing to debounce.
	Search func(query string) ([]candidate.Candidate, error)
	// History supplies the History pane, newest first.
	History func() ([]Entry, error)
	// Page supplies the Knowledge pane for the selected candidate.
	Page func(name string) (knowledge.Page, bool)
	// Save keeps a command. Optional: without it, the key does nothing and
	// the help line does not offer it.
	Save func(command string) error
}

// Entry is one line of history, already reduced to what a screen needs.
type Entry struct {
	Command  string
	ExitCode int
	At       time.Time
	Dir      string
}

// Failed reports a non-zero exit.
func (e Entry) Failed() bool { return e.ExitCode != 0 }

// Outcome is what the UI ended with.
type Outcome struct {
	// Command is what the user accepted, empty if they quit.
	Command string
	// Saved reports that they kept it rather than accepting it.
	Saved bool
}

type pane int

const (
	paneAsk pane = iota
	paneHistory
	paneKnowledge
	paneCount
)

func (p pane) String() string {
	switch p {
	case paneAsk:
		return "ask"
	case paneHistory:
		return "history"
	default:
		return "knowledge"
	}
}

// state is everything on screen. Kept in one value so a redraw is a pure
// function of it, which is what makes a flickering or half-erased frame a
// thing that cannot happen rather than a thing to debug.
type state struct {
	pane    pane
	query   []rune
	results []candidate.Candidate
	cursor  int
	history []Entry
	hcursor int
	scroll  int
	status  string
	err     error
}

// Run draws the interface and blocks until the user accepts something or
// quits.
func (u *UI) Run(initial string) (Outcome, error) {
	if err := u.Term.MakeRaw(); err != nil {
		return Outcome{}, fmt.Errorf("raw mode: %w", err)
	}
	defer u.Term.Restore()

	// The alternate screen buffer is why this can be full-screen without
	// destroying the scrollback the user was reading a moment ago. Leaving it
	// restores their terminal exactly, including the prompt they typed on.
	u.Term.WriteString("\x1b[?1049h\x1b[?25l")
	defer u.Term.WriteString("\x1b[?25h\x1b[?1049l")

	st := state{query: []rune(initial)}
	if u.History != nil {
		st.history, _ = u.History()
	}
	u.refresh(&st)

	for {
		u.draw(st)
		press, err := tty.ReadKey(u.Term.In)
		if err != nil {
			return Outcome{}, nil
		}
		done, out := u.handle(&st, press)
		if done {
			return out, nil
		}
	}
}

// handle applies one keypress. It returns done=true when the UI should close.
func (u *UI) handle(st *state, press tty.Press) (bool, Outcome) {
	switch press.Key {
	case tty.KeyEscape, tty.KeyCtrlC, tty.KeyCtrlD:
		return true, Outcome{}

	case tty.KeyTab:
		st.pane = (st.pane + 1) % paneCount
		st.status = ""

	case tty.KeyUp:
		u.move(st, -1)
	case tty.KeyDown:
		u.move(st, +1)

	case tty.KeyLeft, tty.KeyRight:
		// Deliberately inert. Horizontal keys in a list are the most common
		// source of "it did something and I do not know what".

	case tty.KeyEnter:
		switch st.pane {
		case paneHistory:
			// Enter on a past command loads it into the question box. This is
			// the repair path: you scroll back to what went wrong and ask
			// about it, rather than retyping it.
			if e, ok := at(st.history, st.hcursor); ok {
				st.query = []rune(e.Command)
				st.pane = paneAsk
				u.refresh(st)
			}
		default:
			if c, ok := at(st.results, st.cursor); ok {
				return true, Outcome{Command: c.Command}
			}
		}

	case tty.KeyBackspace:
		if len(st.query) > 0 {
			st.query = st.query[:len(st.query)-1]
			u.refresh(st)
		}

	case tty.KeyRune:
		if press.Rune == 0x13 { // Ctrl-S
			u.save(st)
			break
		}
		if press.Rune >= ' ' {
			st.query = append(st.query, press.Rune)
			u.refresh(st)
		}
	}
	return false, Outcome{}
}

func (u *UI) move(st *state, delta int) {
	switch st.pane {
	case paneHistory:
		st.hcursor = clamp(st.hcursor+delta, len(st.history))
	case paneKnowledge:
		st.scroll = clamp(st.scroll+delta, 1<<20)
	default:
		st.cursor = clamp(st.cursor+delta, len(st.results))
		st.scroll = 0
	}
}

func (u *UI) save(st *state) {
	c, ok := at(st.results, st.cursor)
	if !ok || u.Save == nil {
		return
	}
	if err := u.Save(c.Command); err != nil {
		st.status = "could not save: " + err.Error()
		return
	}
	st.status = "saved " + c.Command
}

// refresh re-runs the search for the current query.
func (u *UI) refresh(st *state) {
	st.cursor, st.scroll = 0, 0
	query := strings.TrimSpace(string(st.query))
	if query == "" || u.Search == nil {
		st.results, st.err = nil, nil
		return
	}
	st.results, st.err = u.Search(query)
}

func (u *UI) draw(st state) {
	width, height := u.Term.Size()
	if width < 40 {
		width = 40
	}
	if height < 10 {
		height = 24
	}
	s := u.Style
	s.Width = width

	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")

	line := func(format string, args ...any) {
		b.WriteString(fmt.Sprintf(format, args...))
		b.WriteString("\r\n")
	}

	line("  %s   %s", s.Bold("wut"), u.tabs(st.pane))
	line("")

	body := height - 6
	switch st.pane {
	case paneAsk:
		u.drawAsk(line, s, st, body)
	case paneHistory:
		u.drawHistory(line, s, st, body)
	case paneKnowledge:
		u.drawKnowledge(line, s, st, body)
	}

	line("")
	if st.status != "" {
		line("  %s", s.Green(st.status))
	} else {
		line("  %s", s.Grey(u.help(st.pane)))
	}
	u.Term.WriteString(b.String())
}

func (u *UI) tabs(active pane) string {
	var parts []string
	for p := paneAsk; p < paneCount; p++ {
		name := p.String()
		if p == active {
			parts = append(parts, u.Style.Bold(name))
		} else {
			parts = append(parts, u.Style.Dim(name))
		}
	}
	return strings.Join(parts, u.Style.Grey("  ·  "))
}

func (u *UI) help(p pane) string {
	switch p {
	case paneHistory:
		return "up/down choose   enter ask about it   tab next pane   esc quit"
	case paneKnowledge:
		return "up/down scroll   tab next pane   esc quit"
	default:
		if u.Save != nil {
			return "type to search   up/down choose   enter accept   ^S keep   tab next pane   esc quit"
		}
		return "type to search   up/down choose   enter accept   tab next pane   esc quit"
	}
}

func (u *UI) drawAsk(line func(string, ...any), s Style, st state, body int) {
	query := string(st.query)
	if query == "" {
		line("  %s %s", s.Cyan(">"), s.Dim("how do I ..."))
	} else {
		line("  %s %s", s.Cyan(">"), s.Bold(query))
	}
	line("")

	switch {
	case st.err != nil:
		line("  %s", s.Red(st.err.Error()))
		return
	case strings.TrimSpace(query) == "":
		line("  %s", s.Dim("Ask in plain language. Nothing leaves this machine."))
		line("  %s", s.Dim("  compress a folder to tar.gz"))
		line("  %s", s.Dim("  undo the last commit"))
		return
	case len(st.results) == 0:
		line("  %s", s.Dim("nothing matches. Try fewer, more specific words."))
		return
	}

	// Each result costs two lines, plus the reasons under the selected one.
	rows := body / 2
	if rows < 1 {
		rows = 1
	}
	start := 0
	if st.cursor >= rows {
		start = st.cursor - rows + 1
	}
	for i := start; i < len(st.results) && i < start+rows; i++ {
		c := st.results[i]
		cmd := c.Command
		if i == st.cursor {
			cmd = s.Bold(cmd)
		}
		line("%s%s  %s", s.Pointer(i == st.cursor), cmd, s.Dim(s.ConfidenceDots(string(c.Confidence))))
		if !c.Risk.Safe() {
			line("    %s", s.Yellow(strings.ToUpper(c.Risk.Level.String())+": "+c.Risk.Reason))
		}
		if i == st.cursor {
			for _, w := range c.Why {
				for _, l := range Wrap(w.Text, s.Width, 8) {
					line("    %s %s", s.Grey(s.Bullet()), s.Grey(l))
				}
			}
		}
	}
}

func (u *UI) drawHistory(line func(string, ...any), s Style, st state, body int) {
	if len(st.history) == 0 {
		line("  %s", s.Dim("nothing recorded yet. Set it up with: wut shell install"))
		return
	}
	start := 0
	if st.hcursor >= body {
		start = st.hcursor - body + 1
	}
	for i := start; i < len(st.history) && i < start+body; i++ {
		e := st.history[i]
		status := s.Green("  ok")
		if e.Failed() {
			status = s.Red(fmt.Sprintf("%4d", e.ExitCode))
		}
		cmd := e.Command
		if i == st.hcursor {
			cmd = s.Bold(cmd)
		}
		line("%s%s %s  %s", s.Pointer(i == st.hcursor), status, s.Dim(e.At.Format("15:04:05")), cmd)
	}
}

func (u *UI) drawKnowledge(line func(string, ...any), s Style, st state, body int) {
	c, ok := at(st.results, st.cursor)
	if !ok || u.Page == nil {
		line("  %s", s.Dim("Choose something in the ask pane, and its page appears here."))
		return
	}
	name := c.Source.Ref
	if name == "" {
		name = firstWord(c.Command)
	}
	page, found := u.Page(name)
	if !found {
		line("  %s", s.Dim("no page for "+name))
		return
	}

	var lines []string
	lines = append(lines, s.Bold(page.Name)+"  "+s.Grey(string(page.Platform)))
	for _, l := range Wrap(page.Description, s.Width, 2) {
		lines = append(lines, "  "+l)
	}
	for _, ex := range page.Examples {
		lines = append(lines, "")
		for _, l := range Wrap(ex.Description, s.Width, 2) {
			lines = append(lines, "  "+s.Dim(l))
		}
		lines = append(lines, "    "+s.Cyan(ex.Command))
	}
	if page.MoreInfo != "" {
		lines = append(lines, "", "  "+s.Grey(page.MoreInfo))
	}

	// Clamp the scroll here rather than where the key is handled: the length
	// is not known until the page is rendered, and clamping against a stale
	// length is how a list scrolls into empty space.
	start := st.scroll
	if start > len(lines)-1 {
		start = len(lines) - 1
	}
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines) && i < start+body; i++ {
		line("  %s", lines[i])
	}
	if start+body < len(lines) {
		line("  %s", s.Grey("... "+strconv.Itoa(len(lines)-start-body)+" more lines"))
	}
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

func clamp(v, n int) int {
	if v < 0 || n == 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

func at[T any](items []T, i int) (T, bool) {
	var zero T
	if i < 0 || i >= len(items) {
		return zero, false
	}
	return items[i], true
}
