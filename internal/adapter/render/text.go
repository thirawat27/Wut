package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/risk"
)

// Text renders for a person at a terminal.
type Text struct {
	Out   io.Writer
	Style Style
	// ShowWhy controls whether reasons are printed under each candidate.
	// They are the point, so this defaults to true and is only turned off by
	// an explicit --quiet.
	ShowWhy bool
	// MaxWhy caps how many reasons are shown before the rest are summarised.
	MaxWhy int
}

// NewText builds a text renderer for a writer.
func NewText(out io.Writer, style Style) *Text {
	return &Text{Out: out, Style: style, ShowWhy: true, MaxWhy: 3}
}

// Result prints a header, then each candidate with its reasons.
func (t *Text) Result(header string, cands []candidate.Candidate) {
	s := t.Style
	if header != "" {
		fmt.Fprintf(t.Out, "%s\n\n", s.Dim(header))
	}
	if len(cands) == 0 {
		fmt.Fprintf(t.Out, "  %s\n", s.Dim("Nothing to suggest."))
		return
	}
	for i, c := range cands {
		t.candidate(c, i == 0, len(cands) > 1)
	}
}

// candidate prints one entry.
func (t *Text) candidate(c candidate.Candidate, first, numbered bool) {
	s := t.Style
	prefix := "  "
	if first && numbered {
		prefix = s.Pointer(true)
	}

	line := s.Bold(c.Command)
	conf := s.Dim(s.ConfidenceDots(string(c.Confidence)) + " " + string(c.Confidence))
	fmt.Fprintf(t.Out, "%s%s%s%s\n", prefix, line, Pad(gap(c.Command, s.Width)), conf)

	if c.Title != "" {
		fmt.Fprintf(t.Out, "    %s\n", s.Dim(c.Title))
	}
	if !c.Risk.Safe() {
		fmt.Fprintf(t.Out, "    %s\n", t.riskLine(c.Risk))
	}
	if t.ShowWhy {
		t.why(c)
	}
	if c.Source.Generated {
		fmt.Fprintf(t.Out, "    %s\n", s.Dim("(wording written by the local model)"))
	}
	fmt.Fprintln(t.Out)
}

// why prints the reasons. These are what turn a suggestion into something a
// person can check rather than trust.
func (t *Text) why(c candidate.Candidate) {
	s := t.Style
	shown := c.Why
	hidden := 0
	if t.MaxWhy > 0 && len(shown) > t.MaxWhy {
		hidden = len(shown) - t.MaxWhy
		shown = shown[:t.MaxWhy]
	}
	for _, w := range shown {
		text := w.Text
		if w.Ref != "" {
			text += "  " + s.Grey("("+w.Ref+")")
		}
		for i, line := range Wrap(text, s.Width, 8) {
			if i == 0 {
				fmt.Fprintf(t.Out, "    %s %s\n", s.Grey(s.Bullet()), line)
			} else {
				fmt.Fprintf(t.Out, "      %s\n", line)
			}
		}
	}
	if hidden > 0 {
		fmt.Fprintf(t.Out, "    %s\n", s.Grey(fmt.Sprintf("%s %d more reason(s) — see --output json", s.Bullet(), hidden)))
	}
}

func (t *Text) riskLine(a risk.Assessment) string {
	s := t.Style
	label := strings.ToUpper(a.Level.String())
	body := label + ": " + a.Reason
	if a.Rule != "" {
		body += "  " + s.Grey("["+a.Rule+"]")
	}
	if a.Level >= risk.Destructive {
		return s.Red(body)
	}
	return s.Yellow(body)
}

// Note prints a line that is not a candidate.
func (t *Text) Note(format string, args ...any) {
	fmt.Fprintf(t.Out, "  %s\n", t.Style.Dim(fmt.Sprintf(format, args...)))
}

// Error prints a failure in a shape that reads the same as everything else.
func (t *Text) Error(err error, hint string) {
	fmt.Fprintf(t.Out, "%s %v\n", t.Style.Red("error:"), err)
	if hint != "" {
		fmt.Fprintf(t.Out, "  %s\n", t.Style.Dim(hint))
	}
}

// gap computes the padding that right-aligns the confidence marker, collapsing
// to a single space when the command is too long to leave room.
func gap(command string, width int) int {
	const confWidth = 12
	n := width - len(command) - confWidth - 4
	if n < 1 {
		return 1
	}
	return n
}
