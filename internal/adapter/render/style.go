// Package render turns results into something a person or a program can read.
//
// Three modes, one contract:
//
//   - text  styled for a human at a terminal
//   - json  the versioned public schema in pkg/wutjson
//   - shell only the accepted command on stdout, everything else on the tty
//
// The shell mode is not a formatting choice, it is a safety boundary: stdout
// is what the calling shell will execute.
package render

import (
	"os"
	"strings"
)

// Style holds the escape sequences in use, or empty strings when colour is off.
type Style struct {
	enabled bool
	Width   int
}

// ANSI codes, used only when Style.enabled.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
	ansiGrey   = "\x1b[90m"
)

// NewStyle decides whether to colour, honouring the conventions people
// actually rely on: NO_COLOR wins over everything, TERM=dumb means no escapes,
// and a non-terminal stdout is assumed to be a pipe.
func NewStyle(colorable bool, width int) Style {
	enabled := colorable
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		enabled = false
	}
	if os.Getenv("TERM") == "dumb" {
		enabled = false
	}
	if os.Getenv("WUT_PLAIN") != "" {
		enabled = false
	}
	if width <= 0 {
		width = 80
	}
	return Style{enabled: enabled, Width: width}
}

// Plain reports whether styling is off, which also means no box drawing and no
// cursor movement — the mode a screen reader needs.
func (s Style) Plain() bool { return !s.enabled }

func (s Style) wrap(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (s Style) Bold(t string) string   { return s.wrap(ansiBold, t) }
func (s Style) Dim(t string) string    { return s.wrap(ansiDim, t) }
func (s Style) Red(t string) string    { return s.wrap(ansiRed, t) }
func (s Style) Green(t string) string  { return s.wrap(ansiGreen, t) }
func (s Style) Yellow(t string) string { return s.wrap(ansiYellow, t) }
func (s Style) Blue(t string) string   { return s.wrap(ansiBlue, t) }
func (s Style) Cyan(t string) string   { return s.wrap(ansiCyan, t) }
func (s Style) Grey(t string) string   { return s.wrap(ansiGrey, t) }

// Bullet returns the marker for a Why line, degrading to ASCII when the
// terminal cannot be trusted with Unicode.
func (s Style) Bullet() string {
	if s.ascii() {
		return "-"
	}
	return "·"
}

// Pointer marks the selected candidate.
func (s Style) Pointer(selected bool) string {
	switch {
	case !selected:
		return "  "
	case s.ascii():
		return "> "
	default:
		return "▸ "
	}
}

// ConfidenceDots renders confidence as three filled or hollow marks. It is
// deliberately not a percentage: a number invites arithmetic that the score
// does not support.
func (s Style) ConfidenceDots(level string) string {
	filled, hollow := "●", "○"
	if s.ascii() {
		filled, hollow = "*", "."
	}
	n := 1
	switch level {
	case "high":
		n = 3
	case "medium":
		n = 2
	}
	return strings.Repeat(filled, n) + strings.Repeat(hollow, 3-n)
}

// ascii reports that only ASCII should be emitted.
func (s Style) ascii() bool {
	if os.Getenv("WUT_PLAIN") != "" || os.Getenv("TERM") == "dumb" {
		return true
	}
	// A Windows console on a legacy code page renders box-drawing characters
	// as noise, so check for the modern terminal that does not.
	if isLegacyWindowsConsole() {
		return true
	}
	return false
}

// Wrap breaks text to the given width, indenting continuation lines. It never
// splits a word: a truncated flag name is worse than a ragged margin.
func Wrap(text string, width, indent int) []string {
	if width <= indent+10 {
		return []string{text}
	}
	limit := width - indent
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > limit {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

// Pad returns n spaces.
func Pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}
