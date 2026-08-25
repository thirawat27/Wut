// Package correct turns a command that did not work into candidates that
// might.
//
// It never runs anything. That is not a policy this package enforces at the
// edges — it is a property of what it imports: nothing here can reach os/exec,
// so the class of defect that made the prototype's `oops` re-run `git push` is
// not reachable from this code at all.
//
// Two kinds of correction live here, split by what they need:
//
//   - Fact rules (rules.yaml) decide from the command line plus read-only
//     facts. Adding one needs no Go.
//   - Fuzzy producers (producers.go) search a corpus by edit distance. That is
//     an algorithm, so it is Go.
package correct

import (
	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/cmdline"
	"github.com/thirawat27/wut/internal/core/facts"
	"github.com/thirawat27/wut/internal/core/risk"
)

// Engine holds the compiled rule set and corpora.
type Engine struct {
	rules   *RuleSet
	corpora *Corpora
	policy  *risk.Policy
}

// New builds an engine from the data compiled into the binary.
func New() *Engine {
	return &Engine{
		rules:   BuiltinRules(),
		corpora: BuiltinCorpora(),
		policy:  risk.Builtin(),
	}
}

// NewWith builds an engine from supplied data, for tests and for a future
// user-extension path.
func NewWith(rules *RuleSet, corpora *Corpora, policy *risk.Policy) *Engine {
	return &Engine{rules: rules, corpora: corpora, policy: policy}
}

// Rules exposes the rule set.
func (e *Engine) Rules() *RuleSet { return e.rules }

// Corpora exposes the corpora.
func (e *Engine) Corpora() *Corpora { return e.corpora }

// Correct returns the ranked candidates for a raw command line.
//
// The result is empty when nothing is confidently wrong, and an empty result
// is a correct answer: a correction engine that always produces something is
// one that cannot be trusted when it does.
func (e *Engine) Correct(raw string, f facts.Facts) []candidate.Candidate {
	return e.CorrectLimit(raw, f, 5)
}

// CorrectLimit is Correct with an explicit cap.
func (e *Engine) CorrectLimit(raw string, f facts.Facts, limit int) []candidate.Candidate {
	if f == nil {
		f = facts.Empty{}
	}
	line := cmdline.Parse(raw)
	if line.Empty() {
		return nil
	}

	set := candidate.NewSet(8)
	set.AddAll(e.rules.Apply(line, f))
	for _, p := range producers {
		set.AddAll(p(line, f, e.corpora))
	}

	// A candidate identical to what the user typed is not a correction. This
	// is checked after merging so a rule and a producer that agree on a no-op
	// are both discarded together.
	set = withoutIdentity(set, raw)

	set.Assess(e.policy)
	return set.Ranked(limit)
}

// withoutIdentity rebuilds the set without any candidate equal to the input.
func withoutIdentity(set *candidate.Set, raw string) *candidate.Set {
	ranked := set.Ranked(0)
	out := candidate.NewSet(len(ranked))
	for _, c := range ranked {
		if normalizeSpace(c.Command) == normalizeSpace(raw) {
			continue
		}
		out.Add(c)
	}
	return out
}
