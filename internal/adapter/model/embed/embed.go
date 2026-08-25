// Package embed is WUT's Tier 1 model: the one that has to run everywhere.
//
// It is not a downloaded neural network. It is a semantic index *trained on
// the corpus itself*, using random indexing — a technique that gets you
// distributional semantics without a training run, a model file, or a matrix
// library.
//
// Why this rather than a downloaded sentence encoder:
//
//   - No download, no licence question, no supply-chain surface, and nothing
//     to keep in sync with the index it describes.
//   - Pure Go, no cgo, no SIMD requirement, and it cross-compiles to every
//     platform in the release matrix from one runner. That is the whole of
//     "runs on every machine", satisfied absolutely rather than conditionally.
//   - It is trained on tldr pages, so it learns *this* vocabulary. A general
//     encoder knows that "compress" and "archive" are related in English; this
//     one knows they are related in command documentation, which is the
//     question actually being asked.
//
// How it works, in three steps:
//
//  1. Every term gets a sparse random ternary vector, derived deterministically
//     from a hash of the term. Two different terms are near-orthogonal by
//     construction, so no training is needed to keep them apart.
//  2. A term's *context* vector is the sum of the random vectors of the terms
//     it co-occurs with, across the corpus. This is where meaning comes from:
//     "compress" and "archive" appear in the same sentences as "gzip" and
//     "tar", so their context vectors end up pointing the same way.
//  3. A document, or a question, is the idf-weighted sum of its terms' context
//     vectors, normalised. Similarity is cosine.
//
// The result is deterministic: the same corpus always produces the same
// vectors, so an index is reproducible and a benchmark is repeatable.
package embed

import (
	"context"
	"hash/fnv"
	"math"
	"sort"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

// Dimensions is the vector width.
//
// 256 is chosen for the corpus size rather than by convention: with roughly
// forty thousand short documents, random ternary vectors at 256 dimensions
// stay near-orthogonal, and the whole vector section is 256 bytes per unit —
// about ten megabytes for the real index.
const Dimensions = 256

// nonZerosPerTerm is how many of the 256 slots a term's random vector fills.
// Sparse vectors are what keeps them near-orthogonal; dense random vectors
// would collide.
const nonZerosPerTerm = 12

// ID identifies the model that produced a vector. The index header records it
// so a vector built by a different version is never compared against a query
// built by this one.
const ID = "wut-random-indexing-v1-d256"

// Model is a trained semantic index.
type Model struct {
	// context holds each term's learned vector.
	context map[string][]float32
	// idf weights terms when composing a document vector.
	idf map[string]float32
	// docs is the number of documents the model was trained on.
	docs int
}

// Trainer accumulates co-occurrence while documents are added.
type Trainer struct {
	context  map[string][]float32
	docFreq  map[string]int
	docs     int
	maxTerms int
}

// NewTrainer starts a training run.
func NewTrainer() *Trainer {
	return &Trainer{
		context:  make(map[string][]float32, 1<<15),
		docFreq:  make(map[string]int, 1<<15),
		maxTerms: 64,
	}
}

// Add trains on one document's terms.
//
// Every term in a document is context for every other term in it. Documents
// here are one tldr example or one page summary — a sentence or two — so the
// whole document is the right context window. A sliding window would be
// correct for prose and pointless at this length.
func (t *Trainer) Add(terms []string) {
	if len(terms) < 2 {
		// A single term has no context to learn from, but it still counts
		// toward document frequency.
		for _, term := range terms {
			t.docFreq[term]++
		}
		if len(terms) > 0 {
			t.docs++
		}
		return
	}
	if len(terms) > t.maxTerms {
		terms = terms[:t.maxTerms]
	}
	t.docs++

	// The sum of every term's random vector. Each term's context then gets
	// that sum minus its own vector, which is the same as "the sum of everyone
	// else" for a fraction of the work.
	total := make([]float32, Dimensions)
	randoms := make([][]float32, len(terms))
	for i, term := range terms {
		t.docFreq[term]++
		r := randomVector(term)
		randoms[i] = r
		for d, v := range r {
			total[d] += v
		}
	}

	// Weight by document length: a term in a short, focused example says more
	// about its neighbours than one in a long list.
	weight := float32(1) / float32(math.Sqrt(float64(len(terms))))

	for i, term := range terms {
		ctx, ok := t.context[term]
		if !ok {
			ctx = make([]float32, Dimensions)
			t.context[term] = ctx
		}
		own := randoms[i]
		for d := 0; d < Dimensions; d++ {
			ctx[d] += (total[d] - own[d]) * weight
		}
	}
}

// Finish normalises the accumulated vectors into a model.
func (t *Trainer) Finish() *Model {
	m := &Model{
		context: make(map[string][]float32, len(t.context)),
		idf:     make(map[string]float32, len(t.docFreq)),
		docs:    t.docs,
	}
	docs := float64(t.docs)
	if docs < 1 {
		docs = 1
	}
	for term, ctx := range t.context {
		df := float64(t.docFreq[term])
		if df < 1 {
			df = 1
		}
		// A term that appears in nearly every document carries no signal, and
		// its context vector is the average of the whole corpus. Dropping it
		// is both cheaper and more accurate than keeping it at low weight.
		if df/docs > 0.35 {
			continue
		}
		norm := normalise(ctx)
		if norm == nil {
			continue
		}
		m.context[term] = norm
		m.idf[term] = float32(math.Log(1 + (docs-df+0.5)/(df+0.5)))
	}
	return m
}

// Terms reports how many terms the model knows, for diagnostics.
func (m *Model) Terms() int { return len(m.context) }

// Dimensions reports the vector width.
func (m *Model) Dimensions() int { return Dimensions }

// ID identifies the model.
func (m *Model) ID() string { return ID }

// Embed turns texts into unit vectors. It never fails: a text with no known
// terms yields a zero vector, which is orthogonal to everything and therefore
// simply never matches.
func (m *Model) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = m.EmbedTerms(knowledge.Tokenize(text))
	}
	return out, nil
}

// EmbedTerms composes a vector from already-tokenised terms.
func (m *Model) EmbedTerms(terms []string) []float32 {
	vec := make([]float32, Dimensions)
	any := false
	for _, term := range terms {
		ctx, ok := m.context[term]
		if !ok {
			continue
		}
		w := m.idf[term]
		if w <= 0 {
			w = 1
		}
		for d := 0; d < Dimensions; d++ {
			vec[d] += ctx[d] * w
		}
		any = true
	}
	if !any {
		return vec
	}
	if n := normalise(vec); n != nil {
		return n
	}
	return vec
}

// Quantize converts a unit vector to int8 for storage.
//
// int8 costs a percent or two of precision and a quarter of the disk. At this
// corpus size that is ten megabytes rather than forty, which is the difference
// between an index people keep and one they delete.
func Quantize(v []float32) []int8 {
	out := make([]int8, len(v))
	for i, f := range v {
		q := int(math.Round(float64(f) * 127))
		switch {
		case q > 127:
			q = 127
		case q < -127:
			q = -127
		}
		out[i] = int8(q)
	}
	return out
}

// CosineQ is the similarity between a float query vector and a stored int8
// document vector.
//
// Both sides are unit vectors, so the dot product is the cosine and no
// normalisation is needed here — which is what makes scanning tens of
// thousands of vectors cheap enough to do without an approximate index.
func CosineQ(query []float32, doc []int8) float64 {
	if len(doc) == 0 || len(query) == 0 {
		return 0
	}
	n := len(query)
	if len(doc) < n {
		n = len(doc)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(query[i]) * float64(doc[i])
	}
	return dot / 127
}

// randomVector is a term's sparse ternary signature.
//
// Deterministic from the term itself, so the model never has to store it and
// two runs over the same corpus produce identical output.
func randomVector(term string) []float32 {
	v := make([]float32, Dimensions)
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(term))
	h := hasher.Sum64()
	for i := 0; i < nonZerosPerTerm; i++ {
		// A cheap splitmix step gives independent-looking draws from one hash.
		h ^= h >> 33
		h *= 0xff51afd7ed558ccd
		h ^= h >> 29
		idx := int(h % Dimensions)
		if h&1 == 0 {
			v[idx] += 1
		} else {
			v[idx] -= 1
		}
	}
	return v
}

func normalise(v []float32) []float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return nil
	}
	inv := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = f * inv
	}
	return out
}

// Nearest scans stored vectors and returns the best matches.
//
// Brute force, deliberately. Forty thousand 256-dimension int8 vectors is ten
// megabytes and one linear pass — under two milliseconds. An approximate index
// would add a dependency, a build step, and a tuning parameter to save time
// that is already imperceptible.
func Nearest(query []float32, vectors []int8, dim, limit int, minScore float64) []Match {
	if dim <= 0 || len(vectors) < dim || len(query) == 0 {
		return nil
	}
	count := len(vectors) / dim
	matches := make([]Match, 0, limit*2)
	for i := 0; i < count; i++ {
		score := CosineQ(query, vectors[i*dim:(i+1)*dim])
		if score < minScore {
			continue
		}
		matches = append(matches, Match{Unit: uint32(i), Score: score})
	}
	sort.Slice(matches, func(a, b int) bool {
		if matches[a].Score != matches[b].Score {
			return matches[a].Score > matches[b].Score
		}
		return matches[a].Unit < matches[b].Unit
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// Match is one semantic hit.
type Match struct {
	Unit  uint32
	Score float64
}

// QuantizedTerms returns every learned term vector, quantised for storage.
//
// These are what let a question be embedded at query time. Without them the
// stored unit vectors would be unusable: there would be nothing to compare
// against them in the same space.
func (m *Model) QuantizedTerms() map[string][]int8 {
	out := make(map[string][]int8, len(m.context))
	for term, vec := range m.context {
		out[term] = Quantize(vec)
	}
	return out
}

// Dequantize converts a stored int8 vector back to float32.
func Dequantize(v []int8) []float32 {
	out := make([]float32, len(v))
	for i, q := range v {
		out[i] = float32(q) / 127
	}
	return out
}

// ComposeQuery builds a query vector from term vectors and their weights.
//
// It is the same composition the builder used for documents, which is the
// point: a query and a document only land in the same space if they were
// composed the same way.
func ComposeQuery(vectors [][]float32, weights []float32) []float32 {
	if len(vectors) == 0 {
		return nil
	}
	out := make([]float32, Dimensions)
	for i, v := range vectors {
		// A weight that was supplied is used as given, including zero. Only a
		// *missing* weight defaults to 1.
		//
		// The distinction is not pedantic: these weights are idf values, and
		// idf approaches zero for a word that appears everywhere. Treating a
		// zero weight as "unweighted" gave the most meaningless word in the
		// question the same pull on the query vector as the most distinctive
		// one.
		w := float32(1)
		if i < len(weights) {
			w = weights[i]
			if w < 0 {
				w = 0
			}
		}
		for d := 0; d < Dimensions && d < len(v); d++ {
			out[d] += v[d] * w
		}
	}
	return normalise(out)
}
