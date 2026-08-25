package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/event"
	corefacts "github.com/thirawat27/wut/internal/core/facts"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// factsView is the shape App needs from a FactProvider. Naming it here keeps
// app from importing the adapter for a type it only ever reads.
type factsView = corefacts.Facts

// staleAfter bounds how old a failure may be and still be what "wut" on its
// own is about. Past this, the user has moved on and silently correcting a
// command from twenty minutes ago would be startling.
const staleAfter = 5 * time.Minute

// FixRequest asks for corrections.
type FixRequest struct {
	// Command is an explicit command to correct. When empty, the last failed
	// event from the session is used instead — which is the bare `wut` path.
	Command string
	// Cwd is the directory to read facts from.
	Cwd string
	// Session scopes the event lookup to one shell.
	Session string
	// Stderr is output the user piped in explicitly (tier T2).
	Stderr string
	// Limit caps the candidates returned.
	Limit int
}

// Fix returns corrections for a command.
//
// It never runs anything. That is not enforced by a check here — it is a
// property of what this package can reach: the correction engine is pure, and
// the only process WUT starts anywhere is an allowlisted read-only probe in
// adapter/facts.
func (a *App) Fix(ctx context.Context, req FixRequest) (Result, error) {
	res := Result{Kind: wutjson.KindCorrection}

	target := strings.TrimSpace(req.Command)
	var source event.Event
	haveEvent := false

	if target == "" {
		ev, ok, err := a.lastFailure(ctx, req.Session, req.Cwd)
		if err != nil {
			return res, err
		}
		if !ok {
			res.note(a.noEventHint())
			return res, nil
		}
		source, haveEvent = ev, true
		target = ev.Raw
		if req.Cwd == "" {
			req.Cwd = ev.Cwd
		}
	}

	if strings.TrimSpace(target) == "" {
		return res, nil
	}
	res.Query = target

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	cands := a.deps.Corrector.CorrectLimit(target, a.factsFor(req.Cwd), limit)
	res.Candidates = cands

	if haveEvent {
		res.Candidates = annotateWithEvent(res.Candidates, source)
		if source.NotFound != "" {
			res.note(fmt.Sprintf("the shell reported %q was not found", source.NotFound))
		}
	}
	if len(res.Candidates) == 0 {
		res.note(a.nothingFoundHint(target, haveEvent, source))
	}
	return res, nil
}

// lastFailure finds the most recent correctable event for this session.
func (a *App) lastFailure(ctx context.Context, session, cwd string) (event.Event, bool, error) {
	filter := event.Filter{
		Session:     session,
		Correctable: true,
		Since:       a.deps.Clock.Now().Add(-staleAfter),
		Limit:       1,
	}
	ev, ok, err := a.deps.Events.Last(ctx, filter)
	if err != nil || ok {
		return ev, ok, err
	}
	// Fall back to any session: a user who opened a second terminal to ask is
	// still asking about the same failure.
	filter.Session = ""
	filter.Cwd = cwd
	return a.deps.Events.Last(ctx, filter)
}

// annotateWithEvent adds what the shell observed as an extra reason.
//
// The exit code is evidence in its own right, and showing it is what tells the
// user WUT is working from what actually happened rather than from a guess
// about the text.
func annotateWithEvent(cands []candidate.Candidate, ev event.Event) []candidate.Candidate {
	if len(cands) == 0 {
		return cands
	}
	why := candidate.Why{
		Code:   "shell.exit_code",
		Text:   fmt.Sprintf("the shell reported exit code %d after %s", ev.ExitCode, humanDuration(ev.Duration)),
		Ref:    string(ev.Tier),
		Weight: 0,
	}
	for i := range cands {
		cands[i].Why = append(cands[i].Why, why)
	}
	return cands
}

func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "no measured time"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// noEventHint explains why there is nothing to correct, in terms of what the
// user can change. A shell that cannot report events is the most common
// reason, and saying "nothing found" without saying that is unhelpful.
func (a *App) noEventHint() string {
	if !a.deps.Config.CapturesEvents() {
		return "capture is off, so WUT cannot see what just failed. Turn it on with: wut shell capture on"
	}
	return "no recent failed command in this session. Give it one directly: wut fix \"<command>\""
}

func (a *App) nothingFoundHint(target string, haveEvent bool, ev event.Event) string {
	if haveEvent && ev.Tier != event.TierT1 && ev.Tier != event.TierT2 {
		return "nothing confident to suggest for " + shortCommand(target) +
			". WUT could not see the error text — pipe it in with: " + firstWord(target) + " 2>&1 | wut"
	}
	return "nothing confident to suggest for " + shortCommand(target) + "."
}

func shortCommand(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return "'" + s[:57] + "...'"
	}
	return "'" + s + "'"
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return "command"
}
