package index

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

// BM25 parameters. k1 controls how fast term frequency saturates and b how
// much a long unit is penalised. These are the standard starting values; the
// corpus is short documents, so there is little to gain from tuning them
// before the M4 benchmark exists to measure a change.
const (
	bm25K1 = 1.2
	bm25B  = 0.65
)

// Boost weights. Each one is a claim about what a person means when they type
// a word, and each is reported to the user as its own reason rather than
// folded silently into a number.
const (
	// boostExactName fires when a query word is a command name — but it is
	// scaled by that word's rarity, which matters more than it sounds.
	//
	// "compress a folder to tar.gz" contains the word "compress", and
	// `compress` is also a real (obscure, LZW) command. A flat boost put that
	// page above `tar` for every result on the first page. A word that appears
	// across thousands of pages is being used as English, not as a name.
	boostExactName  = 3.0
	boostPrefixName = 1.0 // the query is a prefix of the command name
	boostIsExample  = 0.9 // an example beats the page: it is the actual answer
	boostCoverage   = 2.5 // scaled by how much of the question was matched

	// platformMismatchWeight is what a page for another operating system keeps.
	//
	// Aggressive on purpose. These pages are not near-misses; they are the
	// right answer to the same question on a machine the user is not sitting
	// at, and they match the question just as well as the right page does.
	// Nothing short of a heavy penalty separates them.
	platformMismatchWeight = 0.25
	// maxPerPage keeps one page from filling the whole result list. Five
	// variations of the same command is not a set of options.
	maxPerPage = 2
	// nameRarityCeiling is the idf at which a name counts as fully distinctive.
	nameRarityCeiling = 4.0
	// nameFloor is the share of the name boost a common word still earns.
	nameFloor = 0.35
	// minUnitLength floors the length used for BM25 normalisation.
	//
	// Without it, very short units win everything: a page summary of four
	// words gets an enormous length bonus and outranks the example that
	// actually answers the question. The effect got much worse once
	// placeholders stopped being indexed, because "make {{path/to/file}}"
	// became "make".
	minUnitLength = 6.0
)

// Search answers a natural-language question.
func (r *Reader) Search(ctx context.Context, q knowledge.Query, limit int) ([]knowledge.Hit, error) {
	if !r.Ready() || len(q.Terms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	terms := q.Terms
	// The scorer works on the index's own tokenisation, so a question and a
	// page are always split the same way.
	if tokens := knowledge.TokenizeQuery(strings.Join(q.Terms, " ")); len(tokens) > 0 {
		terms = tokens
	}

	avgLen := r.averageUnitLength()
	scores := make(map[uint32]float64, 256)
	matched := make(map[uint32]int, 256)
	reasons := make(map[uint32][]string, 256)
	idfByTerm := make(map[string]float64, len(terms))

	for _, term := range terms {
		docs := r.postingsFor(term)
		if len(docs) == 0 {
			continue
		}
		idf := math.Log(1 + (float64(len(r.units))-float64(len(docs))+0.5)/(float64(len(docs))+0.5))
		idfByTerm[term] = idf
		for _, id := range docs {
			if int(id) >= len(r.units) {
				continue
			}
			u := r.units[id]
			length := math.Max(float64(u.Length), minUnitLength)
			// One occurrence per unit: the index does not store term
			// frequencies, because in documents this short a term appearing
			// twice says almost nothing that its presence did not already say.
			tf := 1.0
			norm := tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*length/avgLen))
			scores[id] += idf * norm
			matched[id]++
		}
	}
	// The semantic pass answers the questions keyword matching cannot: the
	// ones where the user's words and the documentation's words simply differ.
	var semantic map[uint32]float64
	if !q.NoSemantic {
		semantic = r.semanticScores(terms, limit*8)
	}
	lexicalOnly := scores
	scores = fuse(scores, semantic)
	if len(scores) == 0 {
		return nil, nil
	}

	// Boosts are applied after the base score so the reason strings can name
	// which one fired.
	termSet := make(map[string]bool, len(terms))
	for _, t := range terms {
		termSet[t] = true
	}

	type scored struct {
		id    uint32
		score float64
	}
	ranked := make([]scored, 0, len(scores))
	for id, base := range scores {
		if int(id) >= len(r.units) {
			continue
		}
		u := r.units[id]
		page, err := r.Page(u.Page)
		if err != nil {
			continue
		}
		name := strings.ToLower(page.Name)
		score := base * platformWeight(page.Platform, q.Platforms)
		var why []string

		// Naming a command only counts as naming it when something else in the
		// question also matches that page.
		//
		// "make a file executable" and "see which process is using a port" both
		// contain a real command name — make(1) and port(1) — used as ordinary
		// English. In both, the command name is the *only* thing that matches,
		// while chmod and lsof match the rest of the question. Requiring a
		// second match separates the two cases without a list of English verbs
		// to maintain.
		// A page's own summary unit is almost never the answer to a how-to
		// question — the example is. So the name boost goes to examples once
		// the question is long enough to be a how-to rather than a lookup.
		isHowTo := len(terms) >= 3
		namesTheCommand := termSet[name] &&
			(matched[id] >= 2 || len(terms) == 1) &&
			(u.Example >= 0 || !isHowTo)

		switch {
		case namesTheCommand:
			// Scale by how distinctive the word is. A name that is also a
			// common English word earns almost nothing here; a name that
			// appears nowhere else earns the full boost.
			// A floor, because naming the command is always some evidence
			// even when the word is common: someone typing "find files larger
			// than 100M" probably does mean find(1). The floor is small enough
			// that it does not resurrect the "compress" problem.
			rarity := nameFloor + (1-nameFloor)*math.Min(1, idfByTerm[name]/nameRarityCeiling)
			score += boostExactName * rarity
			if rarity > 0.6 {
				why = append(why, fmt.Sprintf("the question names %q", page.Name))
			}
		default:
			for t := range termSet {
				if len(t) >= 3 && strings.HasPrefix(name, t) {
					score += boostPrefixName * math.Min(1, idfByTerm[t]/nameRarityCeiling)
					why = append(why, fmt.Sprintf("%q starts with %q", page.Name, t))
					break
				}
			}
		}
		if u.Example >= 0 {
			score += boostIsExample
		}
		// Coverage, squared, rather than all-or-nothing. Matching three of
		// four words is much better than two, and demanding all four means a
		// single unindexed word throws away the right answer.
		if len(terms) > 1 {
			coverage := float64(matched[id]) / float64(len(terms))
			score += boostCoverage * coverage * coverage
			if matched[id] == len(terms) {
				why = append(why, "every word in the question appears here")
			}
		}
		if sem, ok := semantic[id]; ok {
			why = append(why, semanticReason(sem))
		}
		if len(why) == 0 {
			if _, lexical := lexicalOnly[id]; lexical {
				why = append(why, matchReason(matched[id], len(terms)))
			} else {
				why = append(why, semanticReason(semantic[id]))
			}
		}
		reasons[id] = why
		ranked = append(ranked, scored{id: id, score: score})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})

	// Normalise to 0..1 against the best result, so the score means "how much
	// better than the alternatives" rather than an absolute anyone might read
	// as a probability.
	best := ranked[0].score
	if best <= 0 {
		best = 1
	}

	var hits []knowledge.Hit
	seenCommand := make(map[string]bool, limit)
	perPage := make(map[uint32]int, limit)
	for _, s := range ranked {
		if len(hits) >= limit {
			break
		}
		u := r.units[s.id]
		if perPage[u.Page] >= maxPerPage {
			continue
		}
		page, err := r.Page(u.Page)
		if err != nil {
			continue
		}
		perPage[u.Page]++
		h := knowledge.Hit{
			Page:     page,
			Example:  int(u.Example),
			Score:    clamp01(s.score / best * 0.95),
			Reason:   strings.Join(reasons[s.id], "; "),
			Producer: producerFor(lexicalOnly[s.id] > 0, semantic[s.id] > 0),
		}
		cmd := strings.TrimSpace(h.Command())
		if cmd == "" || seenCommand[cmd] {
			continue
		}
		seenCommand[cmd] = true
		hits = append(hits, h)
	}
	return hits, nil
}

func matchReason(matched, total int) string {
	if total <= 1 {
		return "matches the word you used"
	}
	return fmt.Sprintf("matches %d of %d words in the question", matched, total)
}

// platformWeight scales a page by how well its platform matches the caller's.
//
// A weight, not a filter. The right answer to "how do I list processes" on
// Windows is the Windows page, but a user who deliberately asks about a
// Linux-only tool must still be able to find it — so an off-platform page is
// pushed down rather than removed. Ranked below everything relevant it is
// invisible; removed, it is unfindable.
func platformWeight(p knowledge.Platform, preference []knowledge.Platform) float64 {
	if len(preference) == 0 || p == knowledge.PlatformCommon {
		return 1
	}
	for i, want := range preference {
		if p == want {
			// First preference is neutral; later ones taper. On macOS that
			// ranks an osx page above a linux page above a windows one, which
			// is the order a macOS user wants them in.
			return 1 - float64(i)*0.05
		}
	}
	return platformMismatchWeight
}

func (r *Reader) averageUnitLength() float64 {
	if len(r.units) == 0 {
		return 1
	}
	total := 0.0
	for _, u := range r.units {
		total += float64(u.Length)
	}
	if total == 0 {
		return 1
	}
	return total / float64(len(r.units))
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// Names returns every command name in the index, deduplicated and sorted.
//
// This is the corpus for correcting a mistyped program against something
// better than a hard-coded list: with an index installed, WUT knows about four
// thousand commands rather than the two hundred compiled into the binary.
func (r *Reader) Names() []string {
	if !r.Ready() {
		return nil
	}
	out := make([]string, 0, len(r.names))
	var last string
	for _, entry := range r.names {
		n := nameOf(entry)
		if n != last {
			out = append(out, n)
			last = n
		}
	}
	return out
}
