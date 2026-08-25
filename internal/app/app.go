// Package app is the use-case layer: one function per thing a user can ask
// for, orchestrating the pure core over the ports.
//
// It imports internal/core and internal/port, and never an adapter. That is
// what lets the daemon serve the same use cases the CLI runs in-process — the
// daemon is a transport, not a second implementation, and any drift between
// the two would show up as a failing test rather than as a support ticket.
package app

import (
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/correct"
	"github.com/thirawat27/wut/internal/core/risk"
	"github.com/thirawat27/wut/internal/platform/paths"
	"github.com/thirawat27/wut/internal/port"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// Deps is everything a use case may reach. Every field is required: the
// null implementations in adapter/nullport exist so a caller never has to
// leave one out.
type Deps struct {
	Config       config.Config
	Dirs         paths.Dirs
	Facts        port.FactProvider
	Knowledge    port.KnowledgeSource
	Events       port.EventStore
	Generator    port.Generator
	Embedder     port.Embedder
	Shell        port.ShellIntegration
	Syncer       port.Syncer
	UserData     port.UserData
	ConfigWriter port.ConfigWriter
	Clock        port.Clock
	Corrector    *correct.Engine
	Policy       *risk.Policy
	Version      string
}

// App is the use-case layer.
type App struct {
	deps Deps
}

// New wires an App. It fills in the pieces that have exactly one sensible
// default so a caller only supplies what it actually chose.
func New(d Deps) *App {
	if d.Clock == nil {
		d.Clock = port.SystemClock{}
	}
	if d.Corrector == nil {
		d.Corrector = correct.New()
	}
	if d.Policy == nil {
		d.Policy = risk.Builtin()
	}
	return &App{deps: d}
}

// Deps exposes the wiring, for commands that report on it (doctor, status).
func (a *App) Deps() Deps { return a.deps }

// Config is the loaded configuration.
func (a *App) Config() config.Config { return a.deps.Config }

// Result is what every use case returns.
//
// Notes carry things the user should know that are not candidates — a missing
// index, a capture tier that cannot see the failure, a model that declined.
// Keeping them out of the candidate list is what stops "here is why I could
// not help" from being ranked against "here is the answer".
type Result struct {
	Kind       wutjson.Kind
	Query      string
	Candidates []candidate.Candidate
	Notes      []string
}

// Empty reports a result with nothing to show.
func (r Result) Empty() bool { return len(r.Candidates) == 0 }

// Top returns the best candidate, if there is one.
func (r Result) Top() (candidate.Candidate, bool) {
	if len(r.Candidates) == 0 {
		return candidate.Candidate{}, false
	}
	return r.Candidates[0], true
}

// note appends a note, ignoring empties so callers can build them inline.
func (r *Result) note(msg string) {
	if strings.TrimSpace(msg) != "" {
		r.Notes = append(r.Notes, msg)
	}
}

// factsFor returns runtime context for a directory, honouring the capture
// setting. With capture off, facts still work: reading the directory in front
// of you is not surveillance, and turning it off would silently disable half
// the corrections without saying so.
func (a *App) factsFor(dir string) factsView {
	return a.deps.Facts.For(dir)
}
