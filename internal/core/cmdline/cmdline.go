// Package cmdline parses a shell command line into the shape the rest of the
// system reasons about.
//
// It is deliberately conservative: everything from the first unquoted control
// operator onward is preserved verbatim as Trailing and never interpreted. WUT
// corrects commands, it does not rewrite pipelines.
//
// This package is pure. It performs no I/O and knows nothing about the machine
// it runs on.
package cmdline

import (
	"strings"
	"unicode"
)

// Role is what a token turned out to be once the line was classified.
type Role uint8

const (
	RoleProgram Role = iota
	RoleSubcommand
	RoleFlag
	RoleOperand
	// RoleSeparator is the bare "--". It is neither an option nor an operand:
	// it only marks where option parsing stops.
	RoleSeparator
)

// Quote records how a token was written, so a rewrite can be re-quoted the
// same way instead of silently changing the shell's interpretation.
type Quote uint8

const (
	QuoteNone Quote = iota
	QuoteSingle
	QuoteDouble
)

// Token is one word of the command line, with its byte span in Head so a
// rewrite can splice rather than reassemble.
type Token struct {
	Text  string // unquoted, unescaped value
	Raw   string // exactly as written, including quotes
	Start int    // byte offset into CommandLine.Head
	End   int
	Quote Quote
	Role  Role
}

// IsFlag reports whether the token was written as an option.
func (t Token) IsFlag() bool { return t.Role == RoleFlag }

// Flag is a parsed option. Name keeps the leading dashes.
type Flag struct {
	Name     string // "--set-upstream" or "-u" or "-rf"
	Value    string
	HasValue bool // true only for the --name=value form
	Long     bool
	Index    int // index into CommandLine.Tokens
}

// CommandLine is a parsed command.
type CommandLine struct {
	Raw        string // the input, unchanged
	Head       string // Raw minus Trailing
	Trailing   string // from the first unquoted control operator, verbatim
	Program    string
	Subcommand []string
	Flags      []Flag
	Operands   []string
	Tokens     []Token
}

// Empty reports a line with no program.
func (c CommandLine) Empty() bool { return c.Program == "" }

// HasFlag reports whether any of the given option names is present. A short
// cluster such as -rf satisfies both -r and -f.
func (c CommandLine) HasFlag(names ...string) bool {
	for _, want := range names {
		for _, f := range c.Flags {
			if f.Name == want {
				return true
			}
			if !f.Long && !strings.HasPrefix(want, "--") && len(want) == 2 &&
				strings.ContainsRune(strings.TrimPrefix(f.Name, "-"), rune(want[1])) {
				return true
			}
		}
	}
	return false
}

// Sub returns the subcommand at depth i, or the empty string when absent.
func (c CommandLine) Sub(i int) string {
	if i < len(c.Subcommand) {
		return c.Subcommand[i]
	}
	return ""
}

// TokenIndexOf returns the index of the first token with the given role whose
// text matches, or -1.
func (c CommandLine) TokenIndexOf(role Role, text string) int {
	for i, t := range c.Tokens {
		if t.Role == role && t.Text == text {
			return i
		}
	}
	return -1
}

// Replace splices a new value into one token and returns the rebuilt command
// line, including any trailing pipeline. The replacement is quoted only if it
// needs to be.
func (c CommandLine) Replace(tokenIndex int, value string) string {
	if tokenIndex < 0 || tokenIndex >= len(c.Tokens) {
		return c.Raw
	}
	t := c.Tokens[tokenIndex]
	var b strings.Builder
	b.WriteString(c.Head[:t.Start])
	b.WriteString(requote(value, t.Quote))
	b.WriteString(c.Head[t.End:])
	b.WriteString(c.Trailing)
	return b.String()
}

// needsQuoting lists the characters that change meaning when left bare.
const needsQuoting = " \t\n'\"\\|&;<>()$`*?[]#~"

// requote wraps value the way the original token was written, or the minimum
// needed if the original was bare.
func requote(value string, original Quote) string {
	switch original {
	case QuoteSingle:
		return singleQuote(value)
	case QuoteDouble:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(value) + `"`
	}
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, needsQuoting) {
		return singleQuote(value)
	}
	return value
}

func singleQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// controlOperators end the part of the line WUT is willing to interpret.
// Longest first, so >> is not mistaken for >.
var controlOperators = []string{"&&", "||", ">>", "2>", "|", ";", ">", "<", "&"}

// Parse turns a raw command line into a CommandLine. It never fails: an
// unparseable line yields an empty Program, which every caller reads as
// "nothing to say".
func Parse(raw string) CommandLine {
	c := CommandLine{Raw: raw}
	c.Head, c.Trailing = splitTrailing(raw)
	c.Tokens = tokenize(c.Head)
	classify(&c)
	return c
}

// splitTrailing finds the first control operator outside quotes.
func splitTrailing(raw string) (head, trailing string) {
	q := QuoteNone
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case ch == '\\' && q != QuoteSingle && i+1 < len(raw) && escapable(raw[i+1]):
			escaped = true
		case q == QuoteNone && ch == '\'':
			q = QuoteSingle
		case q == QuoteNone && ch == '"':
			q = QuoteDouble
		case q == QuoteSingle && ch == '\'':
			q = QuoteNone
		case q == QuoteDouble && ch == '"':
			q = QuoteNone
		case q == QuoteNone:
			for _, op := range controlOperators {
				if strings.HasPrefix(raw[i:], op) {
					return raw[:i], raw[i:]
				}
			}
		}
	}
	return raw, ""
}

// tokenize splits on unquoted whitespace, recording byte spans.
func tokenize(s string) []Token {
	var out []Token
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		var text strings.Builder
		q := QuoteNone
		seen := QuoteNone
		for i < len(s) {
			ch := s[i]
			if q == QuoteNone && isSpace(ch) {
				break
			}
			switch {
			case ch == '\\' && q != QuoteSingle && i+1 < len(s) && escapable(s[i+1]):
				i++
				text.WriteByte(s[i])
			case q == QuoteNone && ch == '\'':
				q, seen = QuoteSingle, QuoteSingle
			case q == QuoteNone && ch == '"':
				q, seen = QuoteDouble, QuoteDouble
			case q == QuoteSingle && ch == '\'':
				q = QuoteNone
			case q == QuoteDouble && ch == '"':
				q = QuoteNone
			default:
				text.WriteByte(ch)
			}
			i++
		}
		out = append(out, Token{
			Text:  text.String(),
			Raw:   s[start:i],
			Start: start,
			End:   i,
			Quote: seen,
		})
	}
	return out
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// escapable lists the characters a backslash may legitimately be protecting.
//
// POSIX shells strip a backslash before *any* character, so bash reads
// C:\tools\rm.exe as C:toolsrm.exe. That is correct for bash and useless for
// WUT, which also has to make sense of Windows paths typed into PowerShell and
// cmd. Honouring the escape only before genuinely special characters differs
// from bash exactly where bash's answer is one nobody wanted.
func escapable(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\'', '"', '\\', '|', '&', ';', '<', '>',
		'(', ')', '$', '`', '*', '?', '[', ']', '#', '~', '!':
		return true
	}
	return false
}

// classify assigns roles and fills the convenience slices.
func classify(c *CommandLine) {
	if len(c.Tokens) == 0 {
		return
	}
	c.Tokens[0].Role = RoleProgram
	c.Program = c.Tokens[0].Text

	depth := SubcommandDepth(c.Program)
	endOfFlags := false
	consumedSub := 0

	for i := 1; i < len(c.Tokens); i++ {
		t := &c.Tokens[i]
		switch {
		case !endOfFlags && t.Text == "--":
			endOfFlags = true
			t.Role = RoleSeparator
		case !endOfFlags && looksLikeFlag(t.Text):
			t.Role = RoleFlag
			c.Flags = append(c.Flags, parseFlag(t.Text, i))
		case consumedSub < depth && len(c.Flags) == 0 && isWordy(t.Text) &&
			(consumedSub == 0 || TakesSecondVerb(c.Program, c.Subcommand[0])):
			t.Role = RoleSubcommand
			c.Subcommand = append(c.Subcommand, t.Text)
			consumedSub++
		default:
			t.Role = RoleOperand
			c.Operands = append(c.Operands, t.Text)
		}
	}
}

// looksLikeFlag excludes bare "-", negative numbers, and paths, all of which
// are operands that merely start with a dash.
func looksLikeFlag(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	rest := s[1:]
	if rest[0] == '.' || rest[0] == '/' {
		return false
	}
	return !unicode.IsDigit(rune(rest[0]))
}

func parseFlag(s string, idx int) Flag {
	f := Flag{Name: s, Index: idx, Long: strings.HasPrefix(s, "--")}
	if eq := strings.IndexByte(s, '='); eq > 0 {
		f.Name = s[:eq]
		f.Value = s[eq+1:]
		f.HasValue = true
		f.Long = strings.HasPrefix(f.Name, "--")
	}
	return f
}

// isWordy rejects anything that is obviously a path, a URL, or a value rather
// than a subcommand verb.
func isWordy(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "/\\.:@=$") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return !unicode.IsDigit(rune(s[0]))
}
