// Package candidate holds the single currency of the system.
//
// Everything the user is ever shown is a Candidate, whatever produced it — a
// correction rule, a lexical hit, a semantic hit, or the history log. The
// prototype had two separate result types with two separate merge rules; the
// whole point of this package is that there is now one.
//
// The invariant that matters: a Candidate with no Why cannot be presented.
// Scores are not asserted, they are summed from the reasons, so "why is this
// first" is answered by the lines already on screen.
package candidate

import (
	"sort"
	"strings"

	"github.com/thirawat27/wut/internal/core/risk"
)

// Kind is what sort of answer this is. One type, four presentations.
type Kind string

const (
	KindCorrection  Kind = "correction"
	KindRecall      Kind = "recall"
	KindExplanation Kind = "explanation"
	KindStep        Kind = "step"
)

// Confidence is derived from the ranked set, never set by a producer.
type Confidence string

const (
	Low    Confidence = "low"
	Medium Confidence = "medium"
	High   Confidence = "high"
)

// Producer identifies what generated a candidate.
type Producer string

const (
	ProducerRules    Producer = "rules"
	ProducerTypo     Producer = "typo"
	ProducerLexical  Producer = "tldr-lexical"
	ProducerSemantic Producer = "tldr-semantic"
	ProducerHistory  Producer = "history"
)

// Why is one reason a candidate exists and scored as it did.
//
// Code is stable and machine-readable. Text is the sentence a person reads.
// Weight is this reason's contribution to Score. Ref points at whatever backs
// it: a rule id, a page name, a command that was run to learn it.
type Why struct {
	Code   string  `json:"code"`
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
	Ref    string  `json:"ref,omitempty"`
}

// Provenance records where a candidate came from, including whether a model
// wrote any of its prose.
type Provenance struct {
	Producer  Producer `json:"producer"`
	Ref       string   `json:"ref,omitempty"`
	Generated bool     `json:"generated"`
}

// Candidate is one answer.
type Candidate struct {
	Command    string          `json:"command"`
	Title      string          `json:"title,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	Kind       Kind            `json:"kind"`
	Score      float64         `json:"score"`
	Confidence Confidence      `json:"confidence,omitempty"`
	Why        []Why           `json:"why"`
	Risk       risk.Assessment `json:"risk"`
	Source     Provenance      `json:"source"`
}

// New builds a candidate from a command and its reasons. Score is the sum of
// the reasons, so a producer states evidence rather than a number.
func New(kind Kind, command string, src Provenance, why ...Why) Candidate {
	c := Candidate{Command: command, Kind: kind, Source: src, Why: why}
	c.Score = c.computeScore()
	return c
}

// WithTitle returns a copy carrying a one-line human summary.
func (c Candidate) WithTitle(title string) Candidate { c.Title = title; return c }

// WithDetail returns a copy carrying a longer explanation.
func (c Candidate) WithDetail(detail string) Candidate { c.Detail = detail; return c }

// Presentable reports whether this candidate may be shown at all. The Why
// requirement is the enforcement point for the "always show why" goal: a
// producer that cannot justify a candidate cannot get it on screen.
func (c Candidate) Presentable() bool {
	return strings.TrimSpace(c.Command) != "" && len(c.Why) > 0
}

func (c Candidate) computeScore() float64 {
	total := 0.0
	for _, w := range c.Why {
		total += w.Weight
	}
	return clamp01(total)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Set is an ordered collection of candidates plus the merge and ranking rules.
// The zero value is ready to use.
type Set struct {
	items []Candidate
	index map[string]int // command -> position in items
}

// NewSet returns an empty set sized for n candidates.
func NewSet(n int) *Set {
	return &Set{items: make([]Candidate, 0, n), index: make(map[string]int, n)}
}

// Add inserts a candidate, merging it into an existing one when the command
// text already appears. Merging unions the reasons rather than picking a
// winner, because two independent producers agreeing is itself evidence.
func (s *Set) Add(c Candidate) {
	if !c.Presentable() {
		return
	}
	if s.index == nil {
		s.index = make(map[string]int)
	}
	key := normalizeCommand(c.Command)
	if pos, ok := s.index[key]; ok {
		s.items[pos] = merge(s.items[pos], c)
		return
	}
	s.index[key] = len(s.items)
	s.items = append(s.items, c)
}

// AddAll inserts many candidates.
func (s *Set) AddAll(list []Candidate) {
	for _, c := range list {
		s.Add(c)
	}
}

// Len reports how many distinct candidates the set holds.
func (s *Set) Len() int { return len(s.items) }

// Empty reports a set with nothing to show.
func (s *Set) Empty() bool { return len(s.items) == 0 }

// merge unions two candidates for the same command.
func merge(existing, incoming Candidate) Candidate {
	seen := make(map[string]bool, len(existing.Why))
	for _, w := range existing.Why {
		seen[w.Code+"|"+w.Ref] = true
	}
	for _, w := range incoming.Why {
		if !seen[w.Code+"|"+w.Ref] {
			existing.Why = append(existing.Why, w)
		}
	}
	if existing.Title == "" {
		existing.Title = incoming.Title
	}
	if existing.Detail == "" {
		existing.Detail = incoming.Detail
	}
	// Provenance keeps the stronger claim: a deterministic producer outranks a
	// generated one, so prose written by a model can never quietly take credit
	// for a command a rule found.
	if existing.Source.Generated && !incoming.Source.Generated {
		existing.Source = incoming.Source
	}
	if existing.Risk.Level < incoming.Risk.Level {
		existing.Risk = incoming.Risk
	}
	existing.Score = existing.computeScore()
	return existing
}

// normalizeCommand collapses whitespace so two producers that formatted the
// same command differently still merge.
func normalizeCommand(cmd string) string {
	return strings.Join(strings.Fields(cmd), " ")
}

// Assess runs every candidate through the risk policy. It is separate from Add
// so a caller can build a set without a policy in hand.
func (s *Set) Assess(p *risk.Policy) {
	if p == nil {
		return
	}
	for i := range s.items {
		s.items[i].Risk = p.AssessString(s.items[i].Command)
	}
}

// Ranked sorts by score, assigns Confidence from the shape of the whole set,
// and returns at most limit candidates. A limit of zero means no limit.
func (s *Set) Ranked(limit int) []Candidate {
	out := make([]Candidate, len(s.items))
	copy(out, s.items)

	for i := range out {
		sortWhy(out[i].Why)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		// A tie is broken toward the shorter command, which is almost always
		// the more direct answer, and then alphabetically so output is stable.
		if len(out[i].Command) != len(out[j].Command) {
			return len(out[i].Command) < len(out[j].Command)
		}
		return out[i].Command < out[j].Command
	})

	conf := confidenceOf(out)
	for i := range out {
		out[i].Confidence = conf
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// sortWhy puts the heaviest reason first, so a truncated display shows the
// reason that actually decided the ranking.
func sortWhy(why []Why) {
	sort.SliceStable(why, func(i, j int) bool { return why[i].Weight > why[j].Weight })
}

// confidenceOf reads confidence off the ranked set rather than off the leader
// alone: a strong top candidate with an equally strong runner-up is not a
// confident answer, it is a coin toss.
func confidenceOf(ranked []Candidate) Confidence {
	if len(ranked) == 0 {
		return Low
	}
	top := ranked[0].Score
	gap := top
	if len(ranked) > 1 {
		gap = top - ranked[1].Score
	}
	switch {
	case top >= 0.85 && gap >= 0.25:
		return High
	case top >= 0.55:
		return Medium
	default:
		return Low
	}
}
