package knowledge

import "testing"

// tldr marks the letters that map to flags with square brackets. Splitting on
// them destroys the word: "[c]reate a g[z]ipped archive" has to yield "create"
// and "gzipped", or the most natural question about tar cannot match tar.
func TestTokenizeIgnoresFlagBrackets(t *testing.T) {
	got := map[string]bool{}
	for _, tok := range Tokenize("[c]reate a g[z]ipped archive and write it to a [f]ile") {
		got[tok] = true
	}
	for _, want := range []string{"create", "gzipped", "archive", "write", "file"} {
		if !got[want] {
			t.Errorf("Tokenize did not produce %q; got %v", want, keys(got))
		}
	}
	for _, unwanted := range []string{"reate", "ipped", "ile"} {
		if got[unwanted] {
			t.Errorf("Tokenize produced the fragment %q", unwanted)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTokenizeSplitsCompounds(t *testing.T) {
	got := Tokenize("extract a tar.gz archive with get-content")
	want := map[string]bool{"extract": true, "tar.gz": true, "tar": true, "gz": true,
		"archive": true, "with": true, "get-content": true, "get": true, "content": true}
	for _, tok := range got {
		delete(want, tok)
	}
	if len(want) > 0 {
		t.Errorf("Tokenize missed %v (got %v)", want, got)
	}
}

func TestQueryTokenizerMatchesIndexTokenizer(t *testing.T) {
	// A term the index stores but the query splitter misses is a term that can
	// never match, which is invisible until someone benchmarks retrieval.
	text := "compress a folder to tar.gz"
	indexed := map[string]bool{}
	for _, tok := range Tokenize(text) {
		indexed[tok] = true
	}
	for _, tok := range TokenizeQuery(text) {
		if !indexed[tok] {
			t.Errorf("query produced %q, which the index tokenizer never emits", tok)
		}
	}
}
