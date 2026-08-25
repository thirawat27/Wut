package knowledge

import (
	"strings"
	"unicode"
)

// Tokenize splits text into index terms.
//
// The rules are tuned for command documentation rather than prose. Two matter:
//
//   - A hyphenated or dotted token is kept whole *and* split, so "tar.gz"
//     matches a query for "tar" and a query for "tar.gz" alike.
//   - A flag keeps its dashes as one token, because "--force" and "force" are
//     different questions.
func Tokenize(text string) []string {
	if text == "" {
		return nil
	}
	lower := stripFlagBrackets(stripPlaceholders(strings.ToLower(text)))
	var out []string
	seen := make(map[string]struct{}, 16)

	add := func(tok string) {
		if len(tok) < 2 || len(tok) > 40 || stopWords[tok] {
			return
		}
		if _, dup := seen[tok]; dup {
			return
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}

	for _, field := range strings.FieldsFunc(lower, isSeparator) {
		field = strings.Trim(field, "-_.")
		if field == "" {
			continue
		}
		add(field)
		// Also index the parts of a compound, so "tar.gz" and "get-content"
		// are reachable by either half.
		if strings.ContainsAny(field, ".-_") {
			for _, part := range strings.FieldsFunc(field, func(r rune) bool {
				return r == '.' || r == '-' || r == '_'
			}) {
				add(part)
			}
		}
	}
	return out
}

// bracketStripper removes the square brackets tldr uses to mark which letters
// of a word correspond to a flag.
//
// This is not cosmetic. tldr writes "[c]reate a g[z]ipped archive", and
// treating brackets as separators splits that into c / reate / g / z / ipped —
// so the words "create" and "gzipped" never existed as index terms at all, and
// the question "create a gzipped archive" could not match the page that
// answers it. The brackets stay in the displayed text, where they are useful;
// they are only removed on the way into the index.
var bracketStripper = strings.NewReplacer("[", "", "]", "")

func stripFlagBrackets(s string) string {
	if !strings.ContainsAny(s, "[]") {
		return s
	}
	return bracketStripper.Replace(s)
}

// stripPlaceholders removes the contents of tldr's {{...}} slots.
//
// A placeholder is a hole to fill in, not documentation, and its wording is
// boilerplate: {{path/to/file}}, {{package}}, {{branch_name}} put the same
// couple of dozen words into tens of thousands of units. That does two kinds
// of damage. It corrupts idf, so "file" and "path" look meaningful when they
// are furniture. And it poisons the co-occurrence model, which concludes that
// "file" is related to everything — which is the same as saying it is related
// to nothing.
//
// The placeholders stay in the text the user sees. They are only invisible to
// the index.
func stripPlaceholders(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		open := strings.Index(s, "{{")
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])
		b.WriteByte(' ')
		rest := s[open+2:]
		closeIdx := strings.Index(rest, "}}")
		if closeIdx < 0 {
			return b.String()
		}
		s = rest[closeIdx+2:]
	}
}

// isSeparator splits on everything that is not part of a command-ish word.
func isSeparator(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	switch r {
	case '.', '-', '_', '+':
		return false
	}
	return true
}

// stopWords are dropped. The list is short deliberately: an aggressive stop
// list throws away "in", "to", and "not", each of which changes what a command
// question means.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "and": true, "or": true,
	"is": true, "are": true, "be": true, "this": true, "that": true,
	"it": true, "its": true, "as": true, "at": true, "by": true,
	"how": true, "do": true, "does": true, "you": true, "your": true,
	"can": true, "will": true, "would": true, "should": true,
	"want": true, "need": true, "please": true, "using": true, "use": true,
	"more": true, "information": true, "see": true, "also": true,
}

// TokenizeQuery splits a question the same way pages are split, including the
// compound expansion.
//
// The expansion matters on the query side too: someone who types "tar.gz" is
// also asking about "tar", and without the split the query term would only
// match pages that happen to spell the whole compound.
func TokenizeQuery(text string) []string {
	lower := strings.ToLower(text)
	var out []string
	seen := map[string]bool{}
	add := func(tok string) {
		if len(tok) < 2 || stopWords[tok] || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}
	for _, field := range strings.FieldsFunc(lower, isSeparator) {
		field = strings.Trim(field, "-_.")
		if field == "" {
			continue
		}
		add(field)
		if strings.ContainsAny(field, ".-_") {
			for _, part := range strings.FieldsFunc(field, func(r rune) bool {
				return r == '.' || r == '-' || r == '_'
			}) {
				add(part)
			}
		}
	}
	return out
}
