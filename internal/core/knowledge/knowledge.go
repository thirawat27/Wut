// Package knowledge holds the shape of what WUT knows about commands.
//
// The knowledge base is tldr-pages and nothing else, by decision. This package
// is the boundary that keeps that decision reversible: adding man pages or a
// team playbook later means adding a source that produces these types, not
// changing anything downstream of them.
//
// Pure: no I/O, no format, no index. The packed file lives in adapter/store.
package knowledge

import (
	"sort"
	"strings"
)

// Platform is a tldr page platform directory.
type Platform string

const (
	PlatformCommon  Platform = "common"
	PlatformLinux   Platform = "linux"
	PlatformOSX     Platform = "osx"
	PlatformWindows Platform = "windows"
	PlatformSunOS   Platform = "sunos"
	PlatformAndroid Platform = "android"
	PlatformFreeBSD Platform = "freebsd"
	PlatformNetBSD  Platform = "netbsd"
	PlatformOpenBSD Platform = "openbsd"
)

// Example is one usage snippet: what it does, and the command that does it.
type Example struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

// Page is one command's documentation.
type Page struct {
	Name        string    `json:"name"`
	Platform    Platform  `json:"platform"`
	Description string    `json:"description"`
	MoreInfo    string    `json:"more_info,omitempty"`
	Examples    []Example `json:"examples"`
}

// Text returns everything on the page as one string, for indexing and as the
// grounding set a generated explanation is checked against.
func (p Page) Text() string {
	var b strings.Builder
	b.WriteString(p.Name)
	b.WriteByte(' ')
	b.WriteString(p.Description)
	for _, e := range p.Examples {
		b.WriteByte(' ')
		b.WriteString(e.Description)
		b.WriteByte(' ')
		b.WriteString(e.Command)
	}
	return b.String()
}

// Hit is one search result. Reason travels with it so the candidate built from
// it can say why it matched rather than only how much.
type Hit struct {
	Page     Page
	Example  int // index into Page.Examples, or -1 for the page itself
	Score    float64
	Reason   string
	Producer string
}

// Query is a normalised natural-language question.
type Query struct {
	Raw   string
	Terms []string
	// Program is set when the question clearly names one, which lets an exact
	// page lookup short-circuit the search entirely.
	Program string
	// Platforms is the caller's platform preference, most preferred first.
	//
	// Search does not filter on it — a Linux user asking about `Get-Process`
	// should still find it — but it weights on it heavily, because the tldr
	// corpus carries every platform's page for the same task and without this
	// a Windows machine answers "delete a directory" with `dir` and macOS
	// answers "convert text to uppercase" with `textutil`. Both were real.
	Platforms []Platform
	// NoSemantic restricts the search to lexical matching.
	//
	// It exists for the retrieval benchmark. A semantic layer that cannot be
	// switched off cannot be measured against the baseline it is supposed to
	// beat, and a layer nobody measured is a layer nobody can justify keeping.
	NoSemantic bool
}

// ParseQuery normalises a natural-language question into terms, using exactly
// the same splitting the index used when it was built.
//
// Sharing the tokenizer is not tidiness. A query term the index never emits
// can never match anything, and that failure is completely silent — it shows
// up as "search is a bit weak" rather than as a bug.
func ParseQuery(raw string) Query {
	q := Query{Raw: strings.TrimSpace(raw)}
	q.Terms = TokenizeQuery(q.Raw)
	return q
}

// Empty reports a question with nothing searchable in it.
func (q Query) Empty() bool { return len(q.Terms) == 0 }

// SortHits orders results by score, breaking ties toward the shorter command,
// which is nearly always the more direct answer.
func SortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		ci, cj := hits[i].Command(), hits[j].Command()
		if len(ci) != len(cj) {
			return len(ci) < len(cj)
		}
		return ci < cj
	})
}

// Command returns the command text for a hit: the example's, or the page name
// when the hit is the page itself.
func (h Hit) Command() string {
	if h.Example >= 0 && h.Example < len(h.Page.Examples) {
		return h.Page.Examples[h.Example].Command
	}
	return h.Page.Name
}

// Description returns the human line for a hit.
func (h Hit) Description() string {
	if h.Example >= 0 && h.Example < len(h.Page.Examples) {
		return h.Page.Examples[h.Example].Description
	}
	return h.Page.Description
}

// PlatformPreference returns the platforms to try, best first, for a runtime
// GOOS. Common is always last: a platform-specific page is more useful than a
// generic one when both exist.
func PlatformPreference(goos string) []Platform {
	switch goos {
	case "windows":
		return []Platform{PlatformWindows, PlatformCommon}
	case "darwin":
		return []Platform{PlatformOSX, PlatformLinux, PlatformCommon}
	case "freebsd":
		return []Platform{PlatformFreeBSD, PlatformLinux, PlatformCommon}
	case "netbsd":
		return []Platform{PlatformNetBSD, PlatformLinux, PlatformCommon}
	case "openbsd":
		return []Platform{PlatformOpenBSD, PlatformLinux, PlatformCommon}
	case "android":
		return []Platform{PlatformAndroid, PlatformLinux, PlatformCommon}
	default:
		return []Platform{PlatformLinux, PlatformCommon}
	}
}
