package candidate

import (
	"testing"

	"github.com/thirawat27/wut/internal/core/risk"
)

func why(code string, weight float64) Why {
	return Why{Code: code, Text: code, Weight: weight}
}

func cand(command string, why ...Why) Candidate {
	return New(KindCorrection, command, Provenance{Producer: ProducerRules}, why...)
}

// The central promise of the whole design: a candidate with no reasons cannot
// be shown. This is the test that keeps it a property rather than a slogan —
// it is enforced in Add, so no producer can route around it.
func TestACandidateWithoutReasonsCannotBeAdded(t *testing.T) {
	s := NewSet(4)
	s.Add(New(KindCorrection, "rm -rf /", Provenance{Producer: ProducerRules}))
	s.Add(cand("   ", why("empty", 1)))
	s.Add(cand("git push", why("real", 0.5)))

	if s.Len() != 1 {
		t.Fatalf("set holds %d candidates, want only the justified one", s.Len())
	}
	if got := s.Ranked(0)[0].Command; got != "git push" {
		t.Errorf("kept %q", got)
	}
}

func TestPresentable(t *testing.T) {
	cases := map[string]struct {
		c    Candidate
		want bool
	}{
		"command and reason":  {cand("ls", why("a", 1)), true},
		"no reason":           {New(KindRecall, "ls", Provenance{}), false},
		"blank command":       {cand("", why("a", 1)), false},
		"whitespace command":  {cand(" \t ", why("a", 1)), false},
		"zero-weight reason":  {cand("ls", why("a", 0)), true},
		"reason with no text": {cand("ls", Why{Code: "c", Weight: 1}), true},
	}
	for name, tc := range cases {
		if got := tc.c.Presentable(); got != tc.want {
			t.Errorf("%s: Presentable = %v, want %v", name, got, tc.want)
		}
	}
}

// Two producers agreeing is evidence, so merging unions the reasons instead of
// picking a winner. If it picked one, the second producer's agreement would be
// invisible in both the score and the explanation.
func TestMergingUnionsReasons(t *testing.T) {
	s := NewSet(2)
	s.Add(cand("git push", why("typo", 0.5)))
	s.Add(cand("git push", why("no-upstream", 0.3)))

	if s.Len() != 1 {
		t.Fatalf("the same command produced %d candidates", s.Len())
	}
	got := s.Ranked(0)[0]
	if len(got.Why) != 2 {
		t.Fatalf("merged candidate has %d reasons, want 2: %+v", len(got.Why), got.Why)
	}
	if got.Score <= 0.5 {
		t.Errorf("score %v did not rise when a second producer agreed", got.Score)
	}
}

// The same reason arriving twice must not count twice, or a producer that runs
// two rules over one fact inflates its own confidence.
func TestTheSameReasonDoesNotCountTwice(t *testing.T) {
	s := NewSet(2)
	s.Add(cand("git push", why("typo", 0.5)))
	s.Add(cand("git push", why("typo", 0.5)))

	got := s.Ranked(0)[0]
	if len(got.Why) != 1 {
		t.Errorf("the same reason was recorded %d times", len(got.Why))
	}
}

// Producers format commands differently — one pads around a pipe, another does
// not. Without normalisation the same answer appears twice, splitting its own
// evidence between two rows.
func TestWhitespaceDifferencesStillMerge(t *testing.T) {
	s := NewSet(2)
	s.Add(cand("git   push  --force", why("a", 0.4)))
	s.Add(cand("git push --force", why("b", 0.4)))

	if s.Len() != 1 {
		t.Errorf("whitespace alone split one answer into %d", s.Len())
	}
}

func TestScoreIsTheSumOfReasonWeights(t *testing.T) {
	c := cand("ls", why("a", 0.3), why("b", 0.2))
	if c.Score != 0.5 {
		t.Errorf("score = %v, want 0.5", c.Score)
	}
	over := cand("ls", why("a", 0.9), why("b", 0.9))
	if over.Score != 1 {
		t.Errorf("score = %v, want it clamped to 1", over.Score)
	}
	under := cand("ls", why("a", -5))
	if under.Score != 0 {
		t.Errorf("score = %v, want it clamped to 0", under.Score)
	}
}

func TestRankedOrdersByScoreThenBrevity(t *testing.T) {
	s := NewSet(4)
	s.Add(cand("git push --set-upstream origin main", why("a", 0.4)))
	s.Add(cand("git push", why("b", 0.4)))
	s.Add(cand("git pull", why("c", 0.9)))

	got := s.Ranked(0)
	want := []string{"git pull", "git push", "git push --set-upstream origin main"}
	for i := range want {
		if got[i].Command != want[i] {
			t.Fatalf("order = %v, want %v", commands(got), want)
		}
	}
}

func TestRankedRespectsTheLimit(t *testing.T) {
	s := NewSet(5)
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		s.Add(cand(c, why("w", 0.5)))
	}
	if got := len(s.Ranked(2)); got != 2 {
		t.Errorf("limit 2 returned %d", got)
	}
	if got := len(s.Ranked(0)); got != 5 {
		t.Errorf("limit 0 returned %d, want all of them", got)
	}
}

// Confidence is a property of the whole set, not of the leader. A strong top
// answer with an equally strong runner-up is a coin toss, and saying "high"
// about a coin toss is the fastest way to lose a user's trust.
func TestConfidenceReadsTheWholeSet(t *testing.T) {
	cases := map[string]struct {
		scores []float64
		want   Confidence
	}{
		"one strong answer":            {[]float64{0.95}, High},
		"strong, with a clear gap":     {[]float64{0.95, 0.4}, High},
		"strong, but a close runner":   {[]float64{0.95, 0.9}, Medium},
		"moderate":                     {[]float64{0.6}, Medium},
		"weak":                         {[]float64{0.3}, Low},
		"two weak answers":             {[]float64{0.3, 0.29}, Low},
		"strong but the gap is barely": {[]float64{0.9, 0.66}, Medium},
	}
	for name, tc := range cases {
		s := NewSet(len(tc.scores))
		for i, score := range tc.scores {
			s.Add(cand(string(rune('a'+i)), why("w", score)))
		}
		got := s.Ranked(0)
		if got[0].Confidence != tc.want {
			t.Errorf("%s: confidence = %s, want %s", name, got[0].Confidence, tc.want)
		}
	}
}

func TestEmptySetIsLowConfidence(t *testing.T) {
	if got := confidenceOf(nil); got != Low {
		t.Errorf("confidence of nothing = %s, want %s", got, Low)
	}
}

// The heaviest reason goes first, so a display that shows only one shows the
// reason that actually decided the ranking.
func TestReasonsAreOrderedByWeight(t *testing.T) {
	s := NewSet(1)
	s.Add(cand("ls", why("light", 0.1), why("heavy", 0.6), why("middle", 0.3)))

	got := s.Ranked(0)[0].Why
	for i := 1; i < len(got); i++ {
		if got[i-1].Weight < got[i].Weight {
			t.Fatalf("reasons out of order: %v", got)
		}
	}
}

func TestAssessAttachesRisk(t *testing.T) {
	s := NewSet(2)
	s.Add(cand("ls -la", why("a", 0.5)))
	s.Add(cand("rm -rf /", why("b", 0.5)))
	s.Assess(risk.Builtin())

	for _, c := range s.Ranked(0) {
		if c.Command == "rm -rf /" && c.Risk.Safe() {
			t.Error("rm -rf / was assessed as safe")
		}
		if c.Command == "ls -la" && !c.Risk.Safe() {
			t.Errorf("ls -la was assessed as %s: %s", c.Risk.Level, c.Risk.Reason)
		}
	}
}

// Assess with no policy must leave the set usable rather than panic. A caller
// building candidates before a policy is loaded is a normal state.
func TestAssessWithoutAPolicyIsSafe(t *testing.T) {
	s := NewSet(1)
	s.Add(cand("ls", why("a", 0.5)))
	s.Assess(nil)
	if s.Len() != 1 {
		t.Error("assessing with no policy dropped the candidates")
	}
}

// The zero value must work. Requiring NewSet would make every caller that
// declares a Set as a struct field wrong in a way that only shows up at run
// time.
func TestZeroValueSetWorks(t *testing.T) {
	var s Set
	s.Add(cand("ls", why("a", 0.5)))
	if s.Empty() || s.Len() != 1 {
		t.Errorf("the zero value did not accept a candidate")
	}
}

func TestAddAll(t *testing.T) {
	var s Set
	s.AddAll([]Candidate{
		cand("a", why("w", 0.5)),
		cand("b", why("w", 0.5)),
		New(KindRecall, "c", Provenance{}), // no reasons: must be dropped
	})
	if s.Len() != 2 {
		t.Errorf("AddAll kept %d of 3, want 2", s.Len())
	}
}

func commands(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Command
	}
	return out
}
