package app

import (
	"context"
	"strings"
	"testing"

	"github.com/thirawat27/wut/internal/adapter/nullport"
	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/core/facts"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/platform/paths"
	"github.com/thirawat27/wut/internal/port"
)

// The use-case layer is where the product's promises live: never invent an
// answer, never stay silent about why, never claim to know something it does
// not. Every one of those is checked here against ports rather than against a
// real machine, which is the whole reason the ports exist.

// --- fakes ------------------------------------------------------------------

type fakeFacts struct{ f facts.Facts }

func (p fakeFacts) For(string) facts.Facts {
	if p.f == nil {
		return facts.Empty{}
	}
	return p.f
}

type fakeEvents struct {
	nullport.Events
	last  event.Event
	found bool
}

func (f fakeEvents) Last(context.Context, event.Filter) (event.Event, bool, error) {
	return f.last, f.found, nil
}

type fakeKnowledge struct {
	nullport.Knowledge
	pages []knowledge.Page
}

func (k fakeKnowledge) Ready() bool { return len(k.pages) > 0 }

func (k fakeKnowledge) Lookup(_ context.Context, name string, _ []knowledge.Platform) (knowledge.Page, bool, error) {
	for _, p := range k.pages {
		if strings.EqualFold(p.Name, name) {
			return p, true, nil
		}
	}
	return knowledge.Page{}, false, nil
}

func (k fakeKnowledge) Search(_ context.Context, q knowledge.Query, limit int) ([]knowledge.Hit, error) {
	var out []knowledge.Hit
	for _, p := range k.pages {
		for _, term := range q.Terms {
			if strings.Contains(strings.ToLower(p.Text()), term) {
				out = append(out, knowledge.Hit{
					Page: p, Example: 0, Score: 1, Reason: "matched " + term, Producer: "lexical",
				})
				break
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type recordingWriter struct {
	saved config.Config
	calls int
	err   error
}

func (w *recordingWriter) Save(c config.Config) error {
	w.calls++
	if w.err != nil {
		return w.err
	}
	w.saved = c
	return nil
}
func (w *recordingWriter) Path() string { return "/tmp/config.yaml" }

func testApp(t *testing.T, mutate func(*Deps)) *App {
	t.Helper()
	d := Deps{
		Config:    config.Default(),
		Dirs:      paths.Dirs{Config: t.TempDir(), Data: t.TempDir(), State: t.TempDir()},
		Facts:     fakeFacts{},
		Knowledge: nullport.Knowledge{Reason: "run wut db sync"},
		Events:    nullport.Events{},
		Generator: nullport.Generator{},
		Embedder:  nullport.Embedder{},
		UserData:  nil,
		Clock:     port.SystemClock{},
		Version:   "1.0.0",
	}
	if mutate != nil {
		mutate(&d)
	}
	return New(d)
}

// --- fix --------------------------------------------------------------------

func TestFixCorrectsAnExplicitCommand(t *testing.T) {
	a := testApp(t, nil)
	res, err := a.Fix(context.Background(), FixRequest{Command: "git psuh"})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := res.Top()
	if !ok {
		t.Fatalf("no correction for 'git psuh': %v", res.Notes)
	}
	if top.Command != "git push" {
		t.Errorf("corrected to %q, want %q", top.Command, "git push")
	}
	if len(top.Why) == 0 {
		t.Error("a correction was produced with no reasons")
	}
}

// With nothing recorded and nothing given, WUT must say so rather than invent
// something to correct.
func TestFixWithNothingToWorkOnStaysSilent(t *testing.T) {
	a := testApp(t, nil)
	res, err := a.Fix(context.Background(), FixRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Errorf("produced %d candidates from nothing", len(res.Candidates))
	}
	if len(res.Notes) == 0 {
		t.Error("produced nothing and explained nothing")
	}
}

func TestFixReadsTheLastFailure(t *testing.T) {
	a := testApp(t, func(d *Deps) {
		d.Events = fakeEvents{
			last:  event.Event{Raw: "git psuh", ExitCode: 1, Session: "s1"},
			found: true,
		}
	})
	res, err := a.Fix(context.Background(), FixRequest{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := res.Top()
	if !ok {
		t.Fatalf("the recorded failure produced nothing: %v", res.Notes)
	}
	if top.Command != "git push" {
		t.Errorf("corrected to %q", top.Command)
	}
}

// A command that is already correct must produce nothing. A tool that
// "corrects" working commands is worse than one that stays quiet.
func TestFixDoesNotCorrectWorkingCommands(t *testing.T) {
	a := testApp(t, nil)
	for _, cmd := range []string{
		"git status", "ls -la", "docker ps", "npm install",
		"go test ./...", "kubectl get pods", "echo hello",
	} {
		res, err := a.Fix(context.Background(), FixRequest{Command: cmd})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Empty() {
			t.Errorf("%q was 'corrected' to %q", cmd, res.Candidates[0].Command)
		}
	}
}

func TestEveryCandidateCarriesReasons(t *testing.T) {
	a := testApp(t, nil)
	for _, cmd := range []string{"git psuh", "dokcer ps", "git commit -am"} {
		res, _ := a.Fix(context.Background(), FixRequest{Command: cmd})
		for _, c := range res.Candidates {
			if len(c.Why) == 0 {
				t.Errorf("%q -> %q has no reasons", cmd, c.Command)
			}
			if !c.Presentable() {
				t.Errorf("%q -> %q is not presentable but was returned", cmd, c.Command)
			}
		}
	}
}

// --- ask --------------------------------------------------------------------

func TestAskWithoutAnIndexSaysSo(t *testing.T) {
	a := testApp(t, nil)
	res, err := a.Ask(context.Background(), AskRequest{Question: "compress a folder"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Error("answered a question with no index loaded")
	}
	if len(res.Notes) == 0 || !strings.Contains(strings.Join(res.Notes, " "), "db sync") {
		t.Errorf("notes = %v, want one that says how to fix it", res.Notes)
	}
}

func TestAskReturnsCandidatesWithProvenance(t *testing.T) {
	a := testApp(t, func(d *Deps) {
		d.Knowledge = fakeKnowledge{pages: []knowledge.Page{{
			Name:        "tar",
			Platform:    knowledge.PlatformCommon,
			Description: "Archiving utility",
			Examples: []knowledge.Example{
				{Description: "Create a gzipped archive", Command: "tar czf out.tar.gz src"},
			},
		}}}
	})
	res, err := a.Ask(context.Background(), AskRequest{Question: "create a gzipped archive"})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := res.Top()
	if !ok {
		t.Fatalf("no answer: %v", res.Notes)
	}
	if top.Command != "tar czf out.tar.gz src" {
		t.Errorf("answer = %q", top.Command)
	}
	if top.Source.Producer != candidate.ProducerLexical {
		t.Errorf("producer = %q", top.Source.Producer)
	}
	if top.Source.Generated {
		t.Error("a template answer was marked as model-generated")
	}
	if len(top.Why) == 0 {
		t.Error("an answer arrived with no reasons")
	}
}

func TestAskWithNoWordsAsksForSome(t *testing.T) {
	a := testApp(t, nil)
	res, _ := a.Ask(context.Background(), AskRequest{Question: "   "})
	if len(res.Notes) == 0 {
		t.Error("an empty question produced no guidance")
	}
}

// --- explain ----------------------------------------------------------------

func TestExplainWorksWithoutAnIndex(t *testing.T) {
	a := testApp(t, nil)
	res, err := a.Explain(context.Background(), ExplainRequest{Command: "rm -rf ./build"})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := res.Top()
	if !ok {
		t.Fatalf("explain produced nothing: %v", res.Notes)
	}
	if top.Risk.Safe() {
		t.Errorf("rm -rf was explained as safe: %+v", top.Risk)
	}
	if len(top.Why) == 0 {
		t.Error("an explanation arrived with no reasons")
	}
}

func TestExplainUsesThePageWhenThereIsOne(t *testing.T) {
	a := testApp(t, func(d *Deps) {
		d.Knowledge = fakeKnowledge{pages: []knowledge.Page{{
			Name: "tar", Description: "Archiving utility",
			Examples: []knowledge.Example{{Description: "Create", Command: "tar cf a.tar b"}},
		}}}
	})
	res, err := a.Explain(context.Background(), ExplainRequest{Command: "tar cf a.tar b"})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := res.Top()
	if !ok {
		t.Fatal("no explanation")
	}
	if !strings.Contains(strings.ToLower(top.Title+top.Detail), "archiv") {
		t.Errorf("the page was not used: title=%q detail=%q", top.Title, top.Detail)
	}
}

func TestExplainRefusesNothing(t *testing.T) {
	a := testApp(t, nil)
	res, err := a.Explain(context.Background(), ExplainRequest{Command: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Error("explained an empty command")
	}
}

// --- config -----------------------------------------------------------------

func TestSetConfigWritesAndReportsTheChange(t *testing.T) {
	w := &recordingWriter{}
	a := testApp(t, func(d *Deps) { d.ConfigWriter = w })

	got, err := a.SetConfig("capture.tier", "T1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed || got.Previous != "T0.5" || got.Value != "T1" {
		t.Errorf("result = %+v", got)
	}
	if w.calls != 1 {
		t.Errorf("the writer was called %d times", w.calls)
	}
	if w.saved.Capture.Tier != config.TierT1 {
		t.Errorf("saved tier = %q", w.saved.Capture.Tier)
	}
	// The running process must agree with the file it just wrote, or the
	// daemon keeps serving the old value.
	if a.Config().Capture.Tier != config.TierT1 {
		t.Error("the in-memory configuration was not updated")
	}
}

// A rejected value must not reach the writer at all. Writing first and
// validating on the next load is how a tool becomes unable to start.
func TestSetConfigRefusesBeforeWriting(t *testing.T) {
	w := &recordingWriter{}
	a := testApp(t, func(d *Deps) { d.ConfigWriter = w })

	if _, err := a.SetConfig("capture.tier", "T9"); err == nil {
		t.Fatal("an invalid value was accepted")
	}
	if w.calls != 0 {
		t.Errorf("the writer was called %d times for a rejected value", w.calls)
	}
	if a.Config().Capture.Tier != config.TierT05 {
		t.Error("a rejected value changed the running configuration")
	}
}

func TestSetConfigWithNoWriterIsAnError(t *testing.T) {
	a := testApp(t, nil)
	if _, err := a.SetConfig("ui.theme", "dark"); err == nil {
		t.Error("a configuration change succeeded with nowhere to write it")
	}
}

func TestSetConfigReportsAnUnchangedValue(t *testing.T) {
	w := &recordingWriter{}
	a := testApp(t, func(d *Deps) { d.ConfigWriter = w })

	got, err := a.SetConfig("capture.tier", "T0.5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Error("setting a value to what it already was reported a change")
	}
}

// --- wiring -----------------------------------------------------------------

// New must fill in the pieces with exactly one sensible default, so a caller
// only supplies what it actually chose. Without this every test and the daemon
// would have to construct a policy and a corrector by hand.
func TestNewFillsInTheObviousDefaults(t *testing.T) {
	a := New(Deps{})
	if a.Deps().Clock == nil {
		t.Error("no clock")
	}
	if a.Deps().Corrector == nil {
		t.Error("no corrector")
	}
	if a.Deps().Policy == nil {
		t.Error("no risk policy")
	}
}

func TestResultHelpers(t *testing.T) {
	var r Result
	if !r.Empty() {
		t.Error("an empty result is not empty")
	}
	if _, ok := r.Top(); ok {
		t.Error("an empty result had a top candidate")
	}
	r.note("  ")
	r.note("something")
	if len(r.Notes) != 1 {
		t.Errorf("notes = %v; blanks should be dropped", r.Notes)
	}
}
