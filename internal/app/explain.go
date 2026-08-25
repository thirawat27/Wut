package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/cmdline"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// ExplainRequest asks what a command does.
type ExplainRequest struct {
	Command string
	Cwd     string
	// Verbose includes every example from the page rather than the closest.
	Verbose bool
}

// Explain describes a command, grounded in its tldr page and in the risk
// policy.
//
// The explanation is built from the page by template. A Tier 2 model, when one
// is installed, may rephrase it — never replace it, and never add a flag the
// page does not contain. See app/ground.go for the check that enforces that.
func (a *App) Explain(ctx context.Context, req ExplainRequest) (Result, error) {
	res := Result{Kind: wutjson.KindExplanation, Query: req.Command}

	line := cmdline.Parse(req.Command)
	if line.Empty() {
		res.note("give me a command to explain, for example: wut explain \"tar -xzf archive.tar.gz\"")
		return res, nil
	}

	assessment := a.deps.Policy.Assess(line)
	platforms := knowledge.PlatformPreference(runtime.GOOS)

	page, found, err := a.deps.Knowledge.Lookup(ctx, line.Program, platforms)
	if err != nil {
		return res, err
	}
	if !found {
		res.Candidates = ranked(a.structuralExplanation(line, assessment))
		if !a.deps.Knowledge.Ready() {
			res.note("no knowledge index yet, so this is structure only. Get the descriptions with: wut db sync")
		} else {
			res.note(fmt.Sprintf("no tldr page for %q — this is what can be said from the command itself", line.Program))
		}
		return res, nil
	}

	cand := a.pageExplanation(line, page, assessment, req.Verbose)
	cand = a.rephrase(ctx, cand, page)
	res.Candidates = ranked(cand)
	return res, nil
}

// ranked puts candidates through the set so Confidence is derived the same way
// everywhere. Assigning it by hand at each call site is how a renderer ends up
// printing an empty confidence label, which is what happened before this
// existed.
func ranked(cands ...candidate.Candidate) []candidate.Candidate {
	set := candidate.NewSet(len(cands))
	set.AddAll(cands)
	return set.Ranked(0)
}

// pageExplanation builds the grounded explanation from a tldr page.
func (a *App) pageExplanation(line cmdline.CommandLine, page knowledge.Page, assessment riskAssessment, verbose bool) candidate.Candidate {
	why := []candidate.Why{{
		Code:   "tldr.page",
		Text:   page.Description,
		Ref:    string(page.Platform) + "/" + page.Name,
		Weight: 0.9,
	}}

	if sub := line.Sub(0); sub != "" {
		if ex, ok := closestExample(page, line); ok {
			why = append(why, candidate.Why{
				Code:   "tldr.example",
				Text:   ex.Description,
				Ref:    ex.Command,
				Weight: 0.05,
			})
		}
	}
	for _, f := range line.Flags {
		why = append(why, candidate.Why{
			Code:   "cmdline.flag",
			Text:   fmt.Sprintf("%s is passed as an option", f.Name),
			Weight: 0,
		})
	}
	if !assessment.Safe() {
		why = append(why, candidate.Why{
			Code:   "risk." + assessment.Rule,
			Text:   assessment.Reason,
			Ref:    assessment.Rule,
			Weight: 0,
		})
	}
	if verbose {
		for _, ex := range page.Examples {
			why = append(why, candidate.Why{
				Code:   "tldr.example",
				Text:   ex.Description,
				Ref:    ex.Command,
				Weight: 0,
			})
		}
	}

	cand := candidate.New(
		candidate.KindExplanation, line.Raw,
		candidate.Provenance{Producer: candidate.ProducerLexical, Ref: page.Name},
		why...,
	)
	cand.Risk = assessment
	return cand.WithTitle(summaryLine(page, line))
}

// structuralExplanation is what can be said with no page at all: the shape of
// the command, and whether the risk policy recognises it.
//
// This is the path most users take on a fresh install, so it is built first
// and treated as a real answer rather than as a failure message.
func (a *App) structuralExplanation(line cmdline.CommandLine, assessment riskAssessment) candidate.Candidate {
	why := []candidate.Why{{
		Code:   "cmdline.program",
		Text:   fmt.Sprintf("%s is the program being run", line.Program),
		Weight: 0.4,
	}}
	if sub := strings.Join(line.Subcommand, " "); sub != "" {
		why = append(why, candidate.Why{
			Code:   "cmdline.subcommand",
			Text:   fmt.Sprintf("%s is the subcommand", sub),
			Weight: 0.1,
		})
	}
	for _, f := range line.Flags {
		text := f.Name + " is an option"
		if f.HasValue {
			text = fmt.Sprintf("%s is set to %q", f.Name, f.Value)
		}
		why = append(why, candidate.Why{Code: "cmdline.flag", Text: text, Weight: 0})
	}
	for _, op := range line.Operands {
		why = append(why, candidate.Why{
			Code:   "cmdline.operand",
			Text:   fmt.Sprintf("%s is an argument", op),
			Weight: 0,
		})
	}
	if line.Trailing != "" {
		why = append(why, candidate.Why{
			Code:   "cmdline.pipeline",
			Text:   "everything from " + strings.TrimSpace(line.Trailing) + " onward is a separate stage, left as written",
			Weight: 0,
		})
	}
	if !assessment.Safe() {
		why = append(why, candidate.Why{
			Code:   "risk." + assessment.Rule,
			Text:   assessment.Reason,
			Ref:    assessment.Rule,
			Weight: 0.2,
		})
	}
	cand := candidate.New(
		candidate.KindExplanation, line.Raw,
		candidate.Provenance{Producer: candidate.ProducerRules, Ref: "cmdline/structure"},
		why...,
	)
	cand.Risk = assessment
	return cand.WithTitle("Structure of the command")
}

// closestExample picks the page example that best matches what was typed.
func closestExample(page knowledge.Page, line cmdline.CommandLine) (knowledge.Example, bool) {
	want := strings.Fields(strings.ToLower(line.Raw))
	best, bestScore := knowledge.Example{}, 0
	for _, ex := range page.Examples {
		score := 0
		lower := strings.ToLower(ex.Command)
		for _, w := range want {
			if strings.Contains(lower, w) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = ex, score
		}
	}
	return best, bestScore > 0
}

func summaryLine(page knowledge.Page, line cmdline.CommandLine) string {
	if sub := line.Sub(0); sub != "" {
		return fmt.Sprintf("%s %s — %s", page.Name, sub, trimSentence(page.Description))
	}
	return fmt.Sprintf("%s — %s", page.Name, trimSentence(page.Description))
}

func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
