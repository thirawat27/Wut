// Package eval holds the benchmarks that decide whether the answer engine is
// good enough, as opposed to the unit tests that decide whether it works.
//
// The distinction matters. Every retrieval test elsewhere in this repository
// asserts a behaviour — this term matches, that boost applies. None of them can
// answer "is it any good", and without an answer to that, tuning retrieval is
// hill-climbing on whichever handful of examples someone happened to try. That
// is exactly how this project traded one working case for another, repeatedly,
// before these files existed.
package eval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/thirawat27/wut/internal/adapter/store/index"
	"github.com/thirawat27/wut/internal/core/knowledge"
)

// Targets, from docs/architecture/06-intelligence-slm.md §6.
const (
	// top3Target is where retrieval needs to be. It is not met.
	top3Target = 0.80
	// top3LexicalTarget is the target for the lexical baseline alone.
	top3LexicalTarget = 0.60
)

// top3Floor is what retrieval actually achieves today, minus a point of noise.
//
// The target above is not met and this constant is the honest way to hold that
// open. A test that fails on every run is a test everybody learns to ignore,
// and one whose target was quietly lowered to the current number is a lie; so
// the target stays where it is, gets logged on every run, and the assertion
// catches the thing an assertion can actually catch — a regression.
//
// Measured at 43.8% on the corpus pinned in testdata/corpus.sha256, with the
// Linux platform preference. Both of those are pinned deliberately: the number
// is meaningless without them, which is how this floor came to fail a run that
// had changed no code at all.
//
// Raise this whenever retrieval improves. Lowering it needs a reason in the
// commit message.
const top3Floor = 0.43

// corpusIsPinned reports whether the index under test was built from the
// corpus the floor was measured against.
//
// tldr-pages republishes tldr.zip in place, under a release tag that has not
// moved since 2025, so "latest" is a different corpus from one day to the next
// — and a floor asserted against an input nobody pinned goes red for reasons
// that have nothing to do with the code. CI pins the bytes by digest and sets
// this variable when it could not get them, so the benchmark still reports its
// numbers but stops pretending they are a regression signal.
func corpusIsPinned() bool { return os.Getenv("WUT_EVAL_CORPUS_UNPINNED") == "" }

// question is one benchmark case.
type question struct {
	Text     string
	Expected []string
	Line     int
}

func loadQuestions(t *testing.T) []question {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "e1-retrieval.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out []question
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		q, expected, ok := strings.Cut(text, "\t")
		if !ok {
			t.Fatalf("testdata line %d has no tab: %q", line, text)
		}
		var names []string
		for _, name := range strings.Split(expected, ",") {
			if n := strings.TrimSpace(name); n != "" {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			t.Fatalf("testdata line %d names no expected page", line)
		}
		out = append(out, question{Text: strings.TrimSpace(q), Expected: names, Line: line})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// openIndex finds an index to measure against.
//
// It never builds one. A benchmark that quietly constructs a toy corpus would
// report a number that has nothing to do with what a user experiences, which is
// worse than reporting nothing.
func openIndex(t *testing.T) *index.Reader {
	t.Helper()

	candidates := []string{os.Getenv("WUT_EVAL_INDEX")}
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "windows":
			local := os.Getenv("LOCALAPPDATA")
			if local == "" {
				local = filepath.Join(home, "AppData", "Local")
			}
			candidates = append(candidates, filepath.Join(local, "wut", "knowledge", "tldr.idx"))
		case "darwin":
			candidates = append(candidates,
				filepath.Join(home, "Library", "Application Support", "wut", "knowledge", "tldr.idx"))
		default:
			data := os.Getenv("XDG_DATA_HOME")
			if data == "" {
				data = filepath.Join(home, ".local", "share")
			}
			candidates = append(candidates, filepath.Join(data, "wut", "knowledge", "tldr.idx"))
		}
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if reader, err := index.Open(path); err == nil {
			t.Logf("index: %s", path)
			return reader
		}
	}
	t.Skip("no knowledge index found. Build one with `wut db sync`, or point WUT_EVAL_INDEX at one.")
	return nil
}

// TestE1Retrieval measures top-1 and top-3 hit rate over the question set.
//
// It reports the full picture — the rate, and every miss with what came back
// instead — because a bare pass/fail tells you nothing about what to fix, and
// the misses are the only place the next improvement can come from.
func TestE1Retrieval(t *testing.T) {
	reader := openIndex(t)

	questions := loadQuestions(t)
	if len(questions) < 200 {
		t.Fatalf("the benchmark has %d questions; the gate is defined over 200", len(questions))
	}

	assertGroundTruthExists(t, reader, questions)

	stats := reader.Stats()
	semantic := stats.Vectors > 0
	target := top3LexicalTarget
	layer := "lexical only"
	if semantic {
		target = top3Target
		layer = "lexical + semantic"
	}

	ctx := context.Background()
	var top1, top3 int
	var misses []string

	for _, q := range questions {
		hits, err := reader.Search(ctx, benchmarkQuery(q.Text), 3)
		if err != nil {
			t.Fatalf("%q: %v", q.Text, err)
		}
		names := pageNames(hits)
		switch {
		case len(names) > 0 && matches(names[:1], q.Expected):
			top1++
			top3++
		case matches(names, q.Expected):
			top3++
		default:
			misses = append(misses, fmt.Sprintf("  %-52s want %-28s got %s",
				truncate(q.Text, 52), strings.Join(q.Expected, "|"), strings.Join(names, ", ")))
		}
	}

	n := float64(len(questions))
	t1, t3 := float64(top1)/n, float64(top3)/n

	t.Logf("E1 retrieval over %d questions (%s, %d pages)", len(questions), layer, stats.Pages)
	t.Logf("  top-1  %5.1f%%", t1*100)
	t.Logf("  top-3  %5.1f%%   floor %.0f%%   target %.0f%%", t3*100, top3Floor*100, target*100)

	if len(misses) > 0 {
		sort.Strings(misses)
		shown := misses
		if len(shown) > 40 {
			shown = shown[:40]
		}
		t.Logf("misses (%d, showing %d):\n%s", len(misses), len(shown), strings.Join(shown, "\n"))
	}

	switch {
	case !corpusIsPinned():
		t.Logf("this is not the pinned corpus, so the %.0f%% floor is reported rather than asserted. "+
			"Review the numbers above and re-pin internal/eval/testdata/corpus.sha256.", top3Floor*100)
	case t3 < top3Floor:
		t.Errorf("top-3 hit rate %.1f%% has regressed below the %.0f%% floor",
			t3*100, top3Floor*100)
	case t3 < target:
		// Not a failure, and not a pass either. Keyword search over a corpus
		// plus embeddings trained from that same corpus is at its ceiling
		// here; closing the rest needs a real sentence encoder, which is the
		// escalation R5 in 06-intelligence-slm.md already names.
		t.Logf("NOT MET: the %.0f%% target for %s. Current: %.1f%%. "+
			"See docs/architecture/06-intelligence-slm.md §7 R5.", target*100, layer, t3*100)
	case t3 > top3Floor+0.03:
		t.Logf("retrieval improved: raise top3Floor from %.2f towards %.2f", top3Floor, t3)
	}
}

// TestE1LexicalBaseline measures the same questions with the semantic layer
// removed, which is the only way to know whether that layer earns its size.
func TestE1LexicalBaseline(t *testing.T) {
	reader := openIndex(t)

	if reader.Stats().Vectors == 0 {
		t.Skip("this index has no semantic layer, so there is nothing to compare against")
	}

	questions := loadQuestions(t)
	ctx := context.Background()

	var lexical, hybrid int
	for _, q := range questions {
		query := benchmarkQuery(q.Text)

		lexOnly := query
		lexOnly.NoSemantic = true
		if hits, err := reader.Search(ctx, lexOnly, 3); err == nil && matches(pageNames(hits), q.Expected) {
			lexical++
		}
		if hits, err := reader.Search(ctx, query, 3); err == nil && matches(pageNames(hits), q.Expected) {
			hybrid++
		}
	}

	n := float64(len(questions))
	t.Logf("top-3, lexical only: %.1f%%", float64(lexical)/n*100)
	t.Logf("top-3, with semantic: %.1f%%", float64(hybrid)/n*100)
	t.Logf("the semantic layer is worth %+d questions", hybrid-lexical)

	if hybrid < lexical {
		t.Errorf("the semantic layer made retrieval worse: %d -> %d", lexical, hybrid)
	}
}

// assertGroundTruthExists refuses to report a score against answers that are
// not in the corpus.
//
// This check exists because the first run of this benchmark scored 35%, and
// the reason was not retrieval: the expected pages were written as `git-branch`
// and `kubectl`, while tldr names them `git branch` and `kubectl delete`. A
// benchmark whose right answers do not exist measures nothing and reads like a
// verdict, which is worse than having no benchmark at all.
func assertGroundTruthExists(t *testing.T, reader *index.Reader, questions []question) {
	t.Helper()

	known := make(map[string]bool, 8000)
	for _, name := range reader.Names() {
		known[strings.ToLower(name)] = true
	}

	missing := map[string][]int{}
	for _, q := range questions {
		for _, want := range q.Expected {
			if !known[strings.ToLower(want)] {
				missing[want] = append(missing[want], q.Line)
			}
		}
	}
	if len(missing) == 0 {
		return
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, fmt.Sprintf("  %-28s line %v", name, missing[name]))
	}
	sort.Strings(names)
	t.Fatalf("%d expected page(s) do not exist in this index:\n%s",
		len(missing), strings.Join(names, "\n"))
}

// benchmarkQuery builds a query the same way the Ask use case does, platform
// preference included. Measuring without it would score a configuration no
// user ever runs.
// benchmarkPlatform is the platform the gate is measured on.
//
// Not runtime.GOOS. Platform preference reweights every result, so the same
// index and the same code score differently depending on who ran them: this
// corpus gives 43.8% with a Linux preference and 45.3% with a Windows one. A
// floor that moves with the developer's laptop is not a floor, and the gap is
// wider than the floor's own margin. Linux is what CI runs, so Linux is what
// the number below means.
const benchmarkPlatform = "linux"

func benchmarkQuery(text string) knowledge.Query {
	q := knowledge.ParseQuery(text)
	q.Platforms = knowledge.PlatformPreference(benchmarkPlatform)
	return q
}

func pageNames(hits []knowledge.Hit) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		if !seen[h.Page.Name] {
			seen[h.Page.Name] = true
			out = append(out, h.Page.Name)
		}
	}
	return out
}

func matches(got, want []string) bool {
	for _, g := range got {
		for _, w := range want {
			if strings.EqualFold(g, w) {
				return true
			}
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
