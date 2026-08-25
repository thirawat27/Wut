package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// AskRequest is a natural-language question.
type AskRequest struct {
	Question string
	Cwd      string
	Limit    int
}

// Ask answers "how do I do X" from the knowledge base.
//
// The knowledge base is the answer engine; a model, when installed, only helps
// find and phrase. That ordering is what keeps a small local model from
// inventing flags: it never gets to produce a command in the first place.
func (a *App) Ask(ctx context.Context, req AskRequest) (Result, error) {
	res := Result{Kind: wutjson.KindRecall, Query: req.Question}

	q := knowledge.ParseQuery(req.Question)
	q.Platforms = knowledge.PlatformPreference(runtime.GOOS)
	if q.Empty() {
		res.note("ask me something, for example: wut compress a folder to tar.gz")
		return res, nil
	}
	if !a.deps.Knowledge.Ready() {
		res.note("no knowledge index yet. Build it with: wut db sync")
		return res, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}

	hits, err := a.deps.Knowledge.Search(ctx, q, limit*2)
	if err != nil {
		return res, err
	}
	if len(hits) == 0 {
		res.note(fmt.Sprintf("nothing in the index matches %q. Try fewer, more specific words.", strings.TrimSpace(req.Question)))
		return res, nil
	}

	set := candidate.NewSet(len(hits))
	for _, h := range hits {
		set.Add(candidateFromHit(h))
	}
	set.Assess(a.deps.Policy)
	res.Candidates = set.Ranked(limit)
	return res, nil
}

// candidateFromHit turns a search result into a candidate, carrying the reason
// it matched rather than only the score that came with it.
func candidateFromHit(h knowledge.Hit) candidate.Candidate {
	producer := candidate.ProducerLexical
	if h.Producer == "semantic" {
		producer = candidate.ProducerSemantic
	}

	why := []candidate.Why{{
		Code:   "tldr.match",
		Text:   h.Reason,
		Ref:    string(h.Page.Platform) + "/" + h.Page.Name,
		Weight: h.Score,
	}}
	if d := h.Description(); d != "" {
		why = append(why, candidate.Why{
			Code:   "tldr.description",
			Text:   d,
			Ref:    h.Page.Name,
			Weight: 0,
		})
	}
	if h.Page.Platform != knowledge.PlatformCommon {
		why = append(why, candidate.Why{
			Code:   "tldr.platform",
			Text:   fmt.Sprintf("from the %s page for this platform", h.Page.Platform),
			Weight: 0,
		})
	}

	return candidate.New(
		candidate.KindRecall, h.Command(),
		candidate.Provenance{Producer: producer, Ref: h.Page.Name},
		why...,
	).WithTitle(h.Description())
}

// LookupPage fetches one page by name, for `wut explain` and the TUI's
// knowledge pane.
func (a *App) LookupPage(ctx context.Context, name string) (knowledge.Page, bool, error) {
	return a.deps.Knowledge.Lookup(ctx, name, knowledge.PlatformPreference(runtime.GOOS))
}
