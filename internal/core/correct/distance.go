package correct

import (
	"strings"
	"unicode"
)

// Damerau computes the Damerau-Levenshtein distance between two strings,
// counting a transposition as one edit rather than two.
//
// Transposition matters more than it sounds: the single most common typo shape
// in a terminal is two adjacent keys swapped — psuh, gti, comit, sl. Plain
// Levenshtein scores those as two edits, which pushes them past the threshold
// that keeps unrelated words out.
func Damerau(a, b string) int {
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Three rolling rows are enough: the current one, the previous, and the
	// one before that, which is where a transposition is read from.
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			best := min3(
				cur[j-1]+1,     // insertion
				prev[j]+1,      // deletion
				prev[j-1]+cost, // substitution
			)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				if t := prev2[j-2] + 1; t < best {
					best = t
				}
			}
			cur[j] = best
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// MaxDistanceFor returns how many edits are tolerable for a word of this
// length. Short words get a tight budget because at distance 2 almost every
// three-letter command is two edits from every other one, and a correction
// nobody asked for is worse than no correction.
func MaxDistanceFor(s string) int {
	switch n := len([]rune(s)); {
	case n <= 3:
		return 1
	case n <= 6:
		return 2
	case n <= 12:
		return 3
	default:
		return 4
	}
}

// Match is one scored corpus hit.
type Match struct {
	Value      string
	Distance   int
	Confidence float64
}

// BestMatches returns the closest corpus entries to token, nearest first, at
// most limit of them. It returns nothing when the token is itself in the
// corpus: a word that is already correct is not a typo.
func BestMatches(token string, corpus []string, limit int) []Match {
	if token == "" || len(corpus) == 0 {
		return nil
	}
	lower := strings.ToLower(token)
	maxDist := MaxDistanceFor(token)

	var out []Match
	for _, cand := range corpus {
		if cand == "" {
			continue
		}
		lc := strings.ToLower(cand)
		if lc == lower {
			return nil // already correct
		}
		d := Damerau(lower, lc)
		if d > maxDist {
			continue
		}
		out = append(out, Match{Value: cand, Distance: d, Confidence: confidenceFor(token, d)})
	}
	// Nearest first; ties keep corpus order, which puts the more common
	// commands first when the corpus is ordered by popularity.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Distance < out[j-1].Distance; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// BestMatch returns the single closest corpus entry, or ok=false.
func BestMatch(token string, corpus []string) (Match, bool) {
	m := BestMatches(token, corpus, 1)
	if len(m) == 0 {
		return Match{}, false
	}
	return m[0], true
}

// confidenceFor turns an edit distance into a 0..1 confidence, scaled by how
// much of the word survived. One edit in a long word is near-certain; one edit
// in a three-letter word could be anything.
func confidenceFor(token string, distance int) float64 {
	n := float64(len([]rune(token)))
	if n == 0 {
		return 0
	}
	ratio := 1 - float64(distance)/n
	switch {
	case ratio > 0.95:
		ratio = 0.95
	case ratio < 0.3:
		ratio = 0.3
	}
	return ratio
}

// LooksLikePathOrURL reports tokens that should never be corrected against a
// command corpus, however close they happen to land.
func LooksLikePathOrURL(s string) bool {
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, "/\\") || strings.Contains(s, "://") {
		return true
	}
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "$") {
		return true
	}
	// A dotted name is a filename far more often than a mistyped command.
	if strings.Contains(s, ".") {
		return true
	}
	digits := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return digits == len([]rune(s))
}
