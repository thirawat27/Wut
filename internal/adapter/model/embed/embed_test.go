package embed

import (
	"context"
	"math"
	"testing"
)

// This is the Tier 1 "model": random indexing trained from the tldr corpus
// during db sync. It is the reason WUT needs no model download, and the reason
// natural-language search works at all — so its arithmetic is worth pinning
// down, because a silent regression here looks like "search got a bit worse".

func trainedModel(t *testing.T) *Model {
	t.Helper()
	tr := NewTrainer()

	// Two clusters that never share a word, so any measured relatedness comes
	// from co-occurrence rather than from the terms themselves.
	for i := 0; i < 20; i++ {
		tr.Add([]string{"archive", "compress", "tar", "gzip"})
		tr.Add([]string{"branch", "commit", "git", "repository"})
	}
	// Filler, and not optional. Training drops any term appearing in more than
	// a third of documents as a stop word — correctly, since such a term's
	// context vector is just the average of the corpus. A fixture of two
	// documents therefore trains a model that knows nothing, which is what the
	// first version of this test discovered about itself.
	for i := 0; i < 120; i++ {
		tr.Add([]string{
			"filler" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			"other" + string(rune('a'+i%17)),
			"word" + string(rune('a'+i%13)),
		})
	}

	m := tr.Finish()
	if m == nil {
		t.Fatal("training produced no model")
	}
	if m.Terms() == 0 {
		t.Fatal("training produced a model that knows no terms")
	}
	return m
}

func TestTrainingLearnsCoOccurrence(t *testing.T) {
	m := trainedModel(t)

	together := cosine(m.EmbedTerms([]string{"archive"}), m.EmbedTerms([]string{"compress"}))
	apart := cosine(m.EmbedTerms([]string{"archive"}), m.EmbedTerms([]string{"commit"}))

	if together <= apart {
		t.Errorf("terms that always co-occur (%.3f) are no closer than terms that never do (%.3f)",
			together, apart)
	}
}

func TestVectorsAreNormalised(t *testing.T) {
	m := trainedModel(t)
	v := m.EmbedTerms([]string{"tar", "gzip"})
	if len(v) != Dimensions {
		t.Fatalf("vector has %d dimensions, want %d", len(v), Dimensions)
	}
	if n := norm(v); math.Abs(n-1) > 1e-4 && n != 0 {
		t.Errorf("vector norm = %v, want 1", n)
	}
}

func TestUnknownTermsProduceNothingRatherThanNoise(t *testing.T) {
	m := trainedModel(t)
	v := m.EmbedTerms([]string{"zzzznotaword"})
	if norm(v) > 1e-6 {
		t.Errorf("an unknown term produced a non-zero vector; it would match at random")
	}
}

// The vector for a term is derived from the term itself, so an index built on
// one machine means the same thing on another. If this drifts, every stored
// vector silently stops matching the queries built against it.
func TestTermVectorsAreDeterministic(t *testing.T) {
	a := randomVector("compress")
	b := randomVector("compress")
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("randomVector is not deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
	if c := randomVector("commit"); sameVector(a, c) {
		t.Error("two different terms produced the same vector")
	}
}

func TestQuantisationRoundTripsApproximately(t *testing.T) {
	m := trainedModel(t)
	v := m.EmbedTerms([]string{"archive"})

	back := Dequantize(Quantize(v))
	if got := cosine(v, back); got < 0.99 {
		t.Errorf("quantisation cost %.4f of similarity; int8 should cost almost nothing", 1-got)
	}
}

func TestCosineQMatchesFloatCosine(t *testing.T) {
	m := trainedModel(t)
	a := m.EmbedTerms([]string{"archive"})
	b := m.EmbedTerms([]string{"compress"})

	want := cosine(a, b)
	got := CosineQ(a, Quantize(b))
	if math.Abs(want-got) > 0.02 {
		t.Errorf("CosineQ = %.4f, float cosine = %.4f", got, want)
	}
}

func TestNearestRanksByScore(t *testing.T) {
	m := trainedModel(t)
	query := m.EmbedTerms([]string{"archive"})

	terms := []string{"compress", "commit", "tar", "branch"}
	var flat []int8
	for _, term := range terms {
		flat = append(flat, Quantize(m.EmbedTerms([]string{term}))...)
	}

	matches := Nearest(query, flat, Dimensions, 4, -1)
	if len(matches) == 0 {
		t.Fatal("nothing matched")
	}
	for i := 1; i < len(matches); i++ {
		if matches[i-1].Score < matches[i].Score {
			t.Fatalf("results are not sorted by score: %v", matches)
		}
	}
	// The two archive-cluster terms must outrank the two git-cluster ones.
	top := terms[matches[0].Unit]
	if top != "compress" && top != "tar" {
		t.Errorf("nearest to 'archive' is %q", top)
	}
}

func TestNearestRespectsTheLimitAndTheFloor(t *testing.T) {
	m := trainedModel(t)
	query := m.EmbedTerms([]string{"archive"})
	var flat []int8
	for _, term := range []string{"compress", "commit", "tar", "branch"} {
		flat = append(flat, Quantize(m.EmbedTerms([]string{term}))...)
	}

	if got := Nearest(query, flat, Dimensions, 2, -1); len(got) > 2 {
		t.Errorf("limit 2 returned %d", len(got))
	}
	if got := Nearest(query, flat, Dimensions, 10, 2.0); len(got) != 0 {
		t.Errorf("an impossible minimum score returned %d matches", len(got))
	}
}

func TestEmbedImplementsThePort(t *testing.T) {
	m := trainedModel(t)
	vectors, err := m.Embed(context.Background(), []string{"compress a folder", "commit my work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 {
		t.Fatalf("got %d vectors for 2 texts", len(vectors))
	}
	if m.Dimensions() != Dimensions {
		t.Errorf("Dimensions() = %d", m.Dimensions())
	}
	if m.ID() == "" {
		t.Error("the model has no id; a stored index could not name what built it")
	}
	if m.Terms() == 0 {
		t.Error("the model learned no terms")
	}
}

// The model id is written into every index. Changing it without a plan means
// existing indexes are silently read with vectors that mean something else.
func TestTheModelIdIsStable(t *testing.T) {
	if ID != "wut-random-indexing-v1-d256" {
		t.Errorf("model id = %q; changing it invalidates every stored index", ID)
	}
	if Dimensions != 256 {
		t.Errorf("Dimensions = %d; the id says 256", Dimensions)
	}
}

func TestComposeQueryWeights(t *testing.T) {
	m := trainedModel(t)
	archive := m.EmbedTerms([]string{"archive"})
	commit := m.EmbedTerms([]string{"commit"})

	// Weighted entirely towards the first term, the result must look like it.
	composed := ComposeQuery([][]float32{archive, commit}, []float32{1, 0})
	if got := cosine(composed, archive); got < 0.99 {
		t.Errorf("a query weighted 1/0 is only %.3f similar to its own first term", got)
	}
}

func TestAnEmptyTrainerProducesNoModel(t *testing.T) {
	if m := NewTrainer().Finish(); m != nil && m.Terms() != 0 {
		t.Errorf("an untrained trainer produced %d terms", m.Terms())
	}
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func sameVector(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
