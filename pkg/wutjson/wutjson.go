// Package wutjson is WUT's public output contract.
//
// Everything `--output json` emits is defined here, and nothing else. It is
// versioned by the Schema constant: fields may be added within a major
// version, never removed or repurposed. Scripts, editor integrations, and
// other agents are expected to depend on it, which is the whole point — the
// prototype had no machine-readable output at all, so the only way to consume
// WUT was to scrape styled text.
package wutjson

import (
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
)

// Schema names the contract version carried on every payload.
const Schema = "wut.v1.result"

// Kind mirrors candidate.Kind and is stable.
type Kind string

const (
	KindCorrection  Kind = "correction"
	KindRecall      Kind = "recall"
	KindExplanation Kind = "explanation"
	KindStep        Kind = "step"
)

// Result is the top-level payload.
type Result struct {
	Schema     string      `json:"schema"`
	Kind       Kind        `json:"kind"`
	Query      string      `json:"query,omitempty"`
	Confidence string      `json:"confidence,omitempty"`
	Candidates []Candidate `json:"candidates"`
	// Notes carry anything the user should know that is not a candidate:
	// a missing index, a disabled capture tier, a degraded shell class.
	Notes []string `json:"notes,omitempty"`
}

// Candidate is one answer, with the reasons that produced it.
type Candidate struct {
	Command string  `json:"command"`
	Title   string  `json:"title,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	Score   float64 `json:"score"`
	Risk    Risk    `json:"risk"`
	Source  Source  `json:"source"`
	Why     []Why   `json:"why"`
}

// Why is one reason, machine-readable by Code and human-readable by Text.
type Why struct {
	Code   string  `json:"code"`
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
	Ref    string  `json:"ref,omitempty"`
}

// Risk is the policy verdict.
type Risk struct {
	Level  string   `json:"level"`
	Class  []string `json:"class,omitempty"`
	Reason string   `json:"reason,omitempty"`
	Rule   string   `json:"rule,omitempty"`
}

// Source records what produced the candidate, and whether a model wrote any of
// its prose. Generated is never omitted: a consumer must be able to tell.
type Source struct {
	Producer  string `json:"producer"`
	Ref       string `json:"ref,omitempty"`
	Generated bool   `json:"generated"`
}

// Error is the payload emitted when a command fails and JSON was requested.
type Error struct {
	Schema  string `json:"schema"`
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Hint    string `json:"hint,omitempty"`
	Command string `json:"command,omitempty"`
}

// NewError builds an error payload.
func NewError(code int, err error, hint string) Error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return Error{Schema: "wut.v1.error", Error: msg, Code: code, Hint: hint}
}

// From converts an internal result into the public shape.
func From(kind Kind, query string, cands []candidate.Candidate, notes ...string) Result {
	r := Result{
		Schema:     Schema,
		Kind:       kind,
		Query:      query,
		Candidates: make([]Candidate, 0, len(cands)),
	}
	// Empty notes are dropped rather than passed through. A caller building
	// notes conditionally ends up with blanks in the slice, and on the wire a
	// blank note is a blank line in whatever the consumer renders.
	for _, n := range notes {
		if strings.TrimSpace(n) != "" {
			r.Notes = append(r.Notes, n)
		}
	}
	if len(cands) > 0 {
		r.Confidence = string(cands[0].Confidence)
	}
	for _, c := range cands {
		r.Candidates = append(r.Candidates, fromCandidate(c))
	}
	return r
}

func fromCandidate(c candidate.Candidate) Candidate {
	out := Candidate{
		Command: c.Command,
		Title:   c.Title,
		Detail:  c.Detail,
		Score:   round3(c.Score),
		Risk: Risk{
			Level:  c.Risk.Level.String(),
			Reason: c.Risk.Reason,
			Rule:   c.Risk.Rule,
		},
		Source: Source{
			Producer:  string(c.Source.Producer),
			Ref:       c.Source.Ref,
			Generated: c.Source.Generated,
		},
		Why: make([]Why, 0, len(c.Why)),
	}
	for _, cl := range c.Risk.Class {
		out.Risk.Class = append(out.Risk.Class, string(cl))
	}
	for _, w := range c.Why {
		out.Why = append(out.Why, Why{Code: w.Code, Text: w.Text, Weight: round3(w.Weight), Ref: w.Ref})
	}
	return out
}

// round3 keeps scores readable and, more importantly, keeps golden-file tests
// from failing on floating-point noise in the last digits.
func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}
