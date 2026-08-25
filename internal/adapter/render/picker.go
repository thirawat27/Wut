package render

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/risk"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// Picker outcomes.
var (
	// ErrCancelled means the user chose nothing. It is not a failure.
	ErrCancelled = errors.New("cancelled")
	// ErrRefused means the selection was too dangerous to hand to a shell that
	// will run it immediately.
	ErrRefused = errors.New("refused: the selected command is destructive")
)

// Action is what the user asked to happen to the selected candidate.
type Action int

const (
	// ActionAccept runs it.
	ActionAccept Action = iota
	// ActionEdit hands it back for editing instead of running.
	//
	// This is the "almost right" escape hatch. Without it the only options are
	// run-it-exactly or start over, and a candidate that is one word away from
	// correct is the most common thing a correction engine produces.
	ActionEdit
)

// Choice is what the picker returned.
type Choice struct {
	Candidate candidate.Candidate
	Action    Action
}

// Picker draws candidates on the controlling terminal and returns the one the
// user accepted.
//
// It never writes to stdout. In shell mode stdout is a pipe carrying the
// accepted command back to the shell, and anything else written there would be
// executed.
type Picker struct {
	Term  *tty.Terminal
	Style Style
	// Header is the context line, e.g. the command that failed.
	Header string
	// AllowRisky permits accepting a Destructive or Irreversible candidate.
	// False in shell mode, where acceptance means immediate execution.
	AllowRisky bool
}

// Run draws the list and blocks until the user chooses or cancels.
func (p *Picker) Run(cands []candidate.Candidate) (Choice, error) {
	if len(cands) == 0 {
		return Choice{}, ErrCancelled
	}
	if err := p.Term.MakeRaw(); err != nil {
		return Choice{}, fmt.Errorf("raw mode: %w", err)
	}
	defer p.Term.Restore()

	width, _ := p.Term.Size()
	p.Style.Width = width

	cursor := 0
	// Low confidence starts with nothing selected: the tool saying "I am not
	// sure" should not also be pre-selecting an answer.
	if cands[0].Confidence == candidate.Low {
		cursor = -1
	}
	showWhy := true

	drawn := 0
	for {
		drawn = p.draw(cands, cursor, showWhy, drawn)
		press, err := tty.ReadKey(p.Term.In)
		if err != nil {
			p.clear(drawn)
			return Choice{}, ErrCancelled
		}
		switch press.Key {
		case tty.KeyUp:
			cursor = clampCursor(cursor-1, len(cands))
		case tty.KeyDown:
			cursor = clampCursor(cursor+1, len(cands))
		case tty.KeyEscape, tty.KeyCtrlC, tty.KeyCtrlD:
			p.clear(drawn)
			return Choice{}, ErrCancelled
		case tty.KeyEnter:
			if cursor < 0 {
				cursor = 0
				continue
			}
			chosen := cands[cursor]
			if !p.AllowRisky && chosen.Risk.Blocking() {
				p.clear(drawn)
				return Choice{}, fmt.Errorf("%w: %s", ErrRefused, chosen.Risk.Reason)
			}
			p.clear(drawn)
			return Choice{Candidate: chosen, Action: ActionAccept}, nil
		case tty.KeyRune:
			switch press.Rune {
			case 'w', 'W', '?':
				showWhy = !showWhy
			case 'e', 'E':
				if cursor < 0 {
					cursor = 0
					continue
				}
				p.clear(drawn)
				return Choice{Candidate: cands[cursor], Action: ActionEdit}, nil
			case 'q', 'Q':
				p.clear(drawn)
				return Choice{}, ErrCancelled
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				if n := int(press.Rune - '1'); n < len(cands) {
					cursor = n
				}
			}
		}
	}
}

func clampCursor(v, n int) int {
	if v < 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

// draw repaints the list, returning how many lines it used so the next repaint
// can erase exactly that many. Redrawing in place is what keeps the picker
// from scrolling the user's history away.
func (p *Picker) draw(cands []candidate.Candidate, cursor int, showWhy bool, previous int) int {
	s := p.Style
	if previous > 0 {
		p.clear(previous)
	}
	var b strings.Builder
	lines := 0
	writeLine := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\r\n")
		lines++
	}

	if p.Header != "" {
		writeLine("  %s", s.Dim(p.Header))
		writeLine("")
	}
	if cursor < 0 {
		writeLine("  %s", s.Yellow("Not sure about this one — here is what I found."))
		writeLine("")
	}

	for i, c := range cands {
		marker := s.Pointer(i == cursor)
		cmd := c.Command
		if i == cursor {
			cmd = s.Bold(cmd)
		}
		conf := s.Dim(s.ConfidenceDots(string(c.Confidence)))
		writeLine("%s%s  %s", marker, cmd, conf)

		if !c.Risk.Safe() {
			label := strings.ToUpper(c.Risk.Level.String())
			body := "    " + label + ": " + c.Risk.Reason
			if c.Risk.Level >= risk.Destructive {
				writeLine("%s", s.Red(body))
			} else {
				writeLine("%s", s.Yellow(body))
			}
		}
		if showWhy && i == cursor {
			for _, w := range c.Why {
				text := w.Text
				if w.Ref != "" {
					text += "  (" + w.Ref + ")"
				}
				for _, line := range Wrap(text, s.Width, 8) {
					writeLine("    %s %s", s.Grey(s.Bullet()), s.Grey(line))
				}
			}
		}
	}

	writeLine("")
	writeLine("  %s", s.Grey(p.helpLine()))

	p.Term.WriteString(b.String())
	return lines
}

func (p *Picker) helpLine() string {
	parts := []string{"up/down choose", "enter accept", "w why", "e edit", "esc cancel"}
	if !p.AllowRisky {
		parts = append(parts, "destructive commands are refused")
	}
	return strings.Join(parts, "   ")
}

// clear erases n previously drawn lines, leaving the cursor where it started.
func (p *Picker) clear(n int) {
	if n <= 0 {
		return
	}
	// Move up n lines, clearing each. \r returns to column zero first so the
	// erase covers the whole line whatever the cursor was doing.
	p.Term.WriteString(strings.Repeat("\x1b[1A\x1b[2K\r", n))
}
