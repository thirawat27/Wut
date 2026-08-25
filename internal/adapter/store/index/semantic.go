package index

import (
	"math"
	"sort"

	"github.com/thirawat27/wut/internal/adapter/model/embed"
)

// HasSemantic reports whether this index carries a usable semantic layer.
//
// Both halves are required: the unit vectors, and the term vectors that turn a
// question into something comparable to them. An index with one and not the
// other is not degraded, it is broken, so it is treated as absent.
func (r *Reader) HasSemantic() bool {
	dim := r.VectorDim()
	return dim > 0 && len(r.vectors) >= dim && len(r.termVecs) >= dim &&
		r.modelID == embed.ID
}

// termVector returns the learned vector for a dictionary term.
func (r *Reader) termVector(term string) ([]float32, bool) {
	dim := r.VectorDim()
	if dim == 0 {
		return nil, false
	}
	i := sort.SearchStrings(r.terms, term)
	if i >= len(r.terms) || r.terms[i] != term {
		return nil, false
	}
	start := i * dim
	if start+dim > len(r.termVecs) {
		return nil, false
	}
	return embed.Dequantize(r.termVecs[start : start+dim]), true
}

// embedQuery turns a question into a vector in the same space as the stored
// units.
func (r *Reader) embedQuery(terms []string) []float32 {
	dim := r.VectorDim()
	if dim == 0 {
		return nil
	}
	var (
		vectors [][]float32
		weights []float32
	)
	total := float64(len(r.units))
	for _, t := range terms {
		vec, ok := r.termVector(t)
		if !ok {
			continue
		}
		// Weight by the same idf the lexical side uses, so a rare word steers
		// the query vector as much as it steers the keyword score.
		df := float64(len(r.postingsFor(t)))
		if df < 1 {
			df = 1
		}
		idf := math.Log(1 + (total-df+0.5)/(df+0.5))
		vectors = append(vectors, vec)
		weights = append(weights, float32(idf))
	}
	return embed.ComposeQuery(vectors, weights)
}

// semanticScores returns cosine similarity per unit for a question.
//
// minSemanticScore exists because random indexing gives every pair of
// unrelated vectors a small non-zero similarity. Without a floor the tail of
// the corpus arrives as weak noise, and noise blended into a ranking is worse
// than no second opinion at all.
const minSemanticScore = 0.25

func (r *Reader) semanticScores(terms []string, limit int) map[uint32]float64 {
	if !r.HasSemantic() {
		return nil
	}
	query := r.embedQuery(terms)
	if len(query) == 0 {
		return nil
	}
	matches := embed.Nearest(query, r.vectors, r.VectorDim(), limit, minSemanticScore)
	if len(matches) == 0 {
		return nil
	}
	out := make(map[uint32]float64, len(matches))
	for _, m := range matches {
		out[m.Unit] = m.Score
	}
	return out
}

// Fusion weights. Keyword matching stays the senior partner: it is exact, and
// when it fires it is nearly always right. The semantic side is there for the
// questions keyword matching cannot answer at all — the ones where the user's
// words and the documentation's words simply differ.
const (
	weightLexical  = 1.0
	weightSemantic = 0.55
)

// fuse blends the two rankings.
//
// Each side is normalised against its own best score before blending, because
// BM25 and cosine are not on the same scale and comparing them directly would
// let whichever happened to produce bigger numbers win every time.
func fuse(lexical map[uint32]float64, semantic map[uint32]float64) map[uint32]float64 {
	if len(semantic) == 0 {
		return lexical
	}
	lexMax := maxValue(lexical)
	semMax := maxValue(semantic)

	out := make(map[uint32]float64, len(lexical)+len(semantic))
	for id, s := range lexical {
		out[id] = weightLexical * s / lexMax
	}
	for id, s := range semantic {
		out[id] += weightSemantic * s / semMax
	}
	return out
}

func maxValue(m map[uint32]float64) float64 {
	best := 0.0
	for _, v := range m {
		if v > best {
			best = v
		}
	}
	if best <= 0 {
		return 1
	}
	return best
}

// semanticReason describes a semantic-only match in words the user can judge.
func semanticReason(score float64) string {
	switch {
	case score >= 0.6:
		return "closely related to the wording of your question"
	case score >= 0.4:
		return "related to the wording of your question"
	default:
		return "loosely related to the wording of your question"
	}
}

// producerFor labels where a hit came from, which travels into the candidate's
// provenance and is shown to the user.
func producerFor(inLexical, inSemantic bool) string {
	switch {
	case inLexical && inSemantic:
		return "hybrid"
	case inSemantic:
		return "semantic"
	default:
		return "lexical"
	}
}

// SemanticStats reports the state of the semantic layer, for doctor.
func (r *Reader) SemanticStats() (ready bool, model string, vectors int) {
	dim := r.VectorDim()
	if dim == 0 {
		return false, "", 0
	}
	return r.HasSemantic(), r.modelID, len(r.vectors) / dim
}
