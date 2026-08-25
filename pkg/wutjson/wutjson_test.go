package wutjson

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/risk"
)

// This package is the only thing WUT promises to other programs. Editors and
// other agents are expected to depend on it, which means the field names are
// the contract and a rename is a breaking change however innocuous it looks in
// a diff.

func sample() Result {
	c := candidate.New(
		candidate.KindCorrection,
		"git push --set-upstream origin main",
		candidate.Provenance{Producer: candidate.ProducerRules, Ref: "git/push-no-upstream"},
		candidate.Why{Code: "git.no_upstream", Text: "branch has no upstream", Weight: 0.6, Ref: "git rev-parse"},
	).WithTitle("Push and set the upstream")

	set := candidate.NewSet(1)
	set.Add(c)
	set.Assess(risk.Builtin())

	return From(KindCorrection, "git psuh", set.Ranked(0), "a note")
}

func TestTheSchemaIdentifierIsStable(t *testing.T) {
	if Schema != "wut.v1.result" {
		t.Errorf("schema = %q; changing it breaks every consumer", Schema)
	}
	if got := sample().Schema; got != Schema {
		t.Errorf("a result carried schema %q", got)
	}
}

// The field names are checked against literal strings on purpose. Asserting
// through the Go struct would pass after a `json:"..."` tag was renamed, which
// is exactly the change that breaks a consumer without breaking the build.
func TestWireFieldNames(t *testing.T) {
	data, err := json.Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"schema", "kind", "query", "candidates", "confidence"} {
		if _, ok := wire[field]; !ok {
			t.Errorf("the result has no %q field", field)
		}
	}

	cands, _ := wire["candidates"].([]any)
	if len(cands) != 1 {
		t.Fatalf("got %d candidates", len(cands))
	}
	c, _ := cands[0].(map[string]any)
	for _, field := range []string{"command", "score", "risk", "source", "why"} {
		if _, ok := c[field]; !ok {
			t.Errorf("a candidate has no %q field", field)
		}
	}

	whys, _ := c["why"].([]any)
	if len(whys) == 0 {
		t.Fatal("a candidate reached the wire with no reasons")
	}
	w, _ := whys[0].(map[string]any)
	for _, field := range []string{"code", "text", "weight"} {
		if _, ok := w[field]; !ok {
			t.Errorf("a reason has no %q field", field)
		}
	}
}

// Every candidate on the wire carries its reasons. The guarantee is not just
// about the terminal: a consumer that shows a suggestion without being able to
// show why is exactly what this design refuses to produce.
func TestEveryCandidateCarriesItsReasons(t *testing.T) {
	for _, c := range sample().Candidates {
		if len(c.Why) == 0 {
			t.Errorf("%q reached the wire with no reasons", c.Command)
		}
		if c.Source.Producer == "" {
			t.Errorf("%q reached the wire with no producer", c.Command)
		}
	}
}

func TestScoresAreRoundedForReadability(t *testing.T) {
	if got := round3(0.123456); got != 0.123 {
		t.Errorf("round3(0.123456) = %v", got)
	}
	if got := round3(1); got != 1 {
		t.Errorf("round3(1) = %v", got)
	}
	if got := round3(0.0004); got != 0 {
		t.Errorf("round3(0.0004) = %v", got)
	}
}

func TestNotesSurvive(t *testing.T) {
	res := From(KindRecall, "q", nil, "no index yet", "")
	if len(res.Notes) != 1 {
		t.Errorf("notes = %v; empty ones should be dropped and real ones kept", res.Notes)
	}
}

func TestErrorShape(t *testing.T) {
	e := NewError(5, errors.New("no index"), "run wut db sync")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	_ = json.Unmarshal(data, &wire)

	for _, field := range []string{"schema", "error", "code", "hint"} {
		if _, ok := wire[field]; !ok {
			t.Errorf("an error has no %q field: %s", field, data)
		}
	}
	if wire["schema"] != "wut.v1.error" {
		t.Errorf("error schema = %v", wire["schema"])
	}
}

// A result with nothing in it must still be valid JSON with the schema on it.
// A consumer parsing "no answer" should not have to handle a different shape
// from "here is an answer".
func TestEmptyResultIsStillWellFormed(t *testing.T) {
	data, err := json.Marshal(From(KindRecall, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Schema != Schema {
		t.Errorf("an empty result lost its schema: %s", data)
	}
}

func TestRiskTravelsWithTheCandidate(t *testing.T) {
	set := candidate.NewSet(1)
	set.Add(candidate.New(candidate.KindCorrection, "rm -rf /",
		candidate.Provenance{Producer: candidate.ProducerRules},
		candidate.Why{Code: "c", Text: "t", Weight: 1}))
	set.Assess(risk.Builtin())

	res := From(KindCorrection, "q", set.Ranked(0))
	if len(res.Candidates) != 1 {
		t.Fatal("no candidate")
	}
	r := res.Candidates[0].Risk
	if r.Level == "" || r.Level == "none" {
		t.Errorf("rm -rf / reached the wire as risk %q", r.Level)
	}
	if r.Reason == "" {
		t.Error("a risky candidate reached the wire with no reason given")
	}
}
