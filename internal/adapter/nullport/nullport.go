// Package nullport holds the do-nothing implementation of every port.
//
// It exists so the rest of the system never has to write `if x != nil`. A
// missing knowledge index, a disabled event store, or an absent Tier 2 model
// are all normal states — on a fresh install, all three are true at once — and
// the code that handles them should read the same as the code that does not.
//
// These are also the second implementation every port is required to have, and
// what the use-case tests run against.
package nullport

import (
	"context"
	"errors"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/port"
)

// Knowledge answers nothing and says so.
type Knowledge struct {
	// Reason is shown to the user as a note, e.g. "run wut db sync".
	Reason string
}

var _ port.KnowledgeSource = Knowledge{}

func (Knowledge) Lookup(context.Context, string, []knowledge.Platform) (knowledge.Page, bool, error) {
	return knowledge.Page{}, false, nil
}

func (Knowledge) Search(context.Context, knowledge.Query, int) ([]knowledge.Hit, error) {
	return nil, nil
}

func (Knowledge) Ready() bool { return false }

func (k Knowledge) Stats() port.KnowledgeStats { return port.KnowledgeStats{Ready: false} }

// Events discards everything. This is what `history.enabled: false` uses, so
// turning history off is one construction choice rather than a branch in every
// caller.
type Events struct{}

var _ port.EventStore = Events{}

func (Events) Append(context.Context, event.Event) error { return nil }

func (Events) Recent(context.Context, event.Filter) ([]event.Event, error) { return nil, nil }

func (Events) Last(context.Context, event.Filter) (event.Event, bool, error) {
	return event.Event{}, false, nil
}

func (Events) Ingest(context.Context) (int, error) { return 0, nil }

func (Events) Purge(context.Context) (int, error) { return 0, nil }

func (Events) Stats(context.Context) (port.EventStats, error) {
	return port.EventStats{CaptureTier: "off"}, nil
}

// Generator is the absent Tier 2 model. Available() reporting false is the
// whole contract: every caller already has to handle it, because most users
// will never install a model.
type Generator struct{}

var _ port.Generator = Generator{}

func (Generator) Available() bool { return false }
func (Generator) Name() string    { return "none" }

func (Generator) Generate(context.Context, port.GenRequest) (port.GenResult, error) {
	return port.GenResult{}, port.ErrNoGenerator
}

// Embedder is the absent Tier 1 model.
type Embedder struct{}

var _ port.Embedder = Embedder{}

func (Embedder) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }
func (Embedder) Dimensions() int                                      { return 0 }
func (Embedder) ID() string                                           { return "none" }

// ConfigWriter refuses to write, and says why.
//
// Unlike the other nulls, this one returns an error rather than succeeding
// quietly. A `wut config set` that prints "done" and changes nothing is worse
// than one that fails: the user walks away believing a setting took effect.
type ConfigWriter struct {
	// Reason explains what went wrong when the real writer could not be
	// constructed, e.g. an unwritable config directory.
	Reason string
}

var _ port.ConfigWriter = ConfigWriter{}

func (c ConfigWriter) Save(config.Config) error {
	if c.Reason != "" {
		return errors.New("cannot save configuration: " + c.Reason)
	}
	return errors.New("cannot save configuration: no writable config file")
}

func (ConfigWriter) Path() string { return "" }
