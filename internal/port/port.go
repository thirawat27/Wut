// Package port declares every seam in the system. Interfaces only, no
// implementations, no I/O.
//
// Two rules keep this useful rather than decorative:
//
//  1. Every port has at least two implementations — the real adapter, and a
//     fake that tests use. A port with one implementation is a type alias
//     wearing a costume.
//  2. internal/app depends on these and never on an adapter. That is what
//     makes the daemon possible: it calls the same use cases the CLI does,
//     with the same ports behind them.
//
// Ports for subsystems that are not built yet are declared here anyway, so
// nothing downstream has to change shape when they arrive.
package port

import (
	"context"
	"errors"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/core/facts"
	"github.com/thirawat27/wut/internal/core/knowledge"
)

// Sentinel errors that callers branch on.
var (
	// ErrNoGenerator reports that no Tier 2 model is installed. It is an
	// expected condition, not a failure: the template path is the default
	// experience.
	ErrNoGenerator = errors.New("no local model installed")
	// ErrNoKnowledge reports a missing or damaged index.
	ErrNoKnowledge = errors.New("no knowledge index: run wut db sync")
)

// FactProvider supplies read-only runtime context for a directory.
type FactProvider interface {
	For(dir string) facts.Facts
}

// KnowledgeSource answers questions about commands.
//
// tldr is the only implementation today. The interface is the reason adding
// man pages or a team playbook later would be additive rather than surgical.
type KnowledgeSource interface {
	// Lookup returns one page by name, preferring the given platforms in
	// order. It reports ok=false rather than an error when the page simply is
	// not there, because "no such command" is a normal answer.
	Lookup(ctx context.Context, name string, platforms []knowledge.Platform) (knowledge.Page, bool, error)
	// Search returns hits for a natural-language query.
	Search(ctx context.Context, q knowledge.Query, limit int) ([]knowledge.Hit, error)
	// Ready reports whether the knowledge base is usable. A missing index is
	// an expected state on a fresh install, not a failure.
	Ready() bool
	// Stats describes what is loaded, for `wut db status` and `wut doctor`.
	Stats() KnowledgeStats
}

// KnowledgeStats describes the installed index.
type KnowledgeStats struct {
	Ready     bool      `json:"ready"`
	Pages     int       `json:"pages"`
	Examples  int       `json:"examples"`
	Vectors   int       `json:"vectors"`
	Release   string    `json:"release,omitempty"`
	BuiltAt   time.Time `json:"built_at,omitempty"`
	Path      string    `json:"path,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
}

// EventStore keeps what the shell reported.
type EventStore interface {
	Append(ctx context.Context, e event.Event) error
	// Recent returns events newest first.
	Recent(ctx context.Context, f event.Filter) ([]event.Event, error)
	// Last returns the most recent event matching the filter.
	Last(ctx context.Context, f event.Filter) (event.Event, bool, error)
	// Ingest folds session record files — the ones the shell hook wrote with
	// builtins only — into the log, and reports how many were new.
	//
	// This is the second half of the zero-spawn design: the shell pays one
	// printf, and the cost of turning records into events is paid here, by
	// whichever invocation happens next. It must be idempotent, because it
	// re-reads the same files every time.
	Ingest(ctx context.Context) (int, error)
	// Purge deletes everything and reports how many events went.
	Purge(ctx context.Context) (int, error)
	// Stats describes what is stored.
	Stats(ctx context.Context) (EventStats, error)
}

// EventStats describes the event log.
type EventStats struct {
	Events       int       `json:"events"`
	WithOutput   int       `json:"with_output"`
	Oldest       time.Time `json:"oldest,omitempty"`
	Newest       time.Time `json:"newest,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	SessionsOpen int       `json:"sessions_open"`
	CaptureTier  string    `json:"capture_tier"`
	RetentionHrs float64   `json:"retention_hours"`
	DroppedByAge int       `json:"dropped_by_age,omitempty"`
	DroppedByCap int       `json:"dropped_by_cap,omitempty"`
}

// Embedder is the Tier 1 model: text in, vector out. Always available.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions reports the vector width, which the index header records so a
	// mismatched model cannot silently produce nonsense scores.
	Dimensions() int
	// ID identifies the model that produced a vector, for the same reason.
	ID() string
}

// Generator is the Tier 2 model: optional, and allowed to be absent.
//
// It may only rephrase. It never produces a command — that comes from a
// deterministic producer — and every token it writes is checked against the
// grounding set before a user sees it.
type Generator interface {
	Available() bool
	Name() string
	Generate(ctx context.Context, req GenRequest) (GenResult, error)
}

// GenRequest is a grounded generation request. Grounding is not advisory: the
// validator rejects output containing tokens that do not appear in it.
type GenRequest struct {
	System      string
	Task        string
	Grounding   []string
	MaxTokens   int
	Timeout     time.Duration
	Temperature float64
}

// GenResult is what came back.
type GenResult struct {
	Text     string
	Tokens   int
	Duration time.Duration
	Model    string
}

// ShellIntegration manages the managed block in a user's rc files.
type ShellIntegration interface {
	Detect() ([]DetectedShell, error)
	Render(shell string) (string, error)
	Install(req InstallRequest) (InstallReport, error)
	Uninstall(req InstallRequest) (InstallReport, error)
}

// DetectedShell is a shell found on this machine.
type DetectedShell struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	RCFile    string `json:"rc_file"`
	Class     string `json:"class"` // full | full-later | manual
	Tier      string `json:"tier"`  // T0 | T0.5 | T1 | none
	Active    bool   `json:"active"`
	Installed bool   `json:"installed"`
	Legacy    bool   `json:"legacy_block"` // a block from the prototype is present
}

// InstallRequest describes one install or uninstall.
type InstallRequest struct {
	Shells []string
	DryRun bool
	Alias  string
}

// InstallReport is what happened, per shell.
type InstallReport struct {
	Changes []InstallChange `json:"changes"`
}

// InstallChange is one rc file's outcome.
type InstallChange struct {
	Shell   string `json:"shell"`
	RCFile  string `json:"rc_file"`
	Action  string `json:"action"` // installed | updated | removed | unchanged | skipped
	Backup  string `json:"backup,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Message string `json:"message,omitempty"`
	Err     string `json:"error,omitempty"`
}

// Syncer rebuilds the knowledge index from its upstream source.
//
// It is a port rather than a direct call so the CLI never has to construct the
// network adapter itself — that belongs to cmd/wut, and an architecture test
// fails the build if the rule slips.
type Syncer interface {
	Sync(ctx context.Context, indexPath string, opts SyncOptions) (SyncResult, error)
}

// SyncOptions carries everything a sync run needs from the caller.
type SyncOptions struct {
	// FromArchive builds from a local zip instead of downloading. It is an
	// explicit flag and never inferred from the working directory — inferring it
	// was how the prototype produced a different database depending on where it
	// ran.
	FromArchive string
	// Embed also trains and stores the semantic index.
	Embed bool
	// Progress reports each step to the user.
	Progress func(string)
}

// SyncResult describes a completed sync.
type SyncResult struct {
	IndexPath string        `json:"index_path"`
	Pages     int           `json:"pages"`
	Bytes     int64         `json:"bytes"`
	Digest    string        `json:"digest"`
	Source    string        `json:"source"`
	Took      time.Duration `json:"took"`
}

// UserData holds the only things in WUT the user authored themselves: saved
// commands and aliases.
//
// A port like everything else, even though only the CLI reads it. The
// alternative — "it is only the CLI's own data, so it can construct it
// directly" — is exactly the special pleading that produced the prototype,
// where twelve files built their own storage handle for equally
// reasonable-sounding reasons.
type UserData interface {
	Path() string
	Add(command, note string, tags []string) (SavedCommand, error)
	Remove(match string) (SavedCommand, error)
	List(filter string) ([]SavedCommand, error)
	SetAlias(name, command, note string) (UserAlias, error)
	RemoveAlias(name string) error
	Aliases() ([]UserAlias, error)
	ShellDefinitions(shell string) (string, error)
}

// SavedCommand is one command the user kept.
type SavedCommand struct {
	Command string    `json:"command"`
	Note    string    `json:"note,omitempty"`
	Tags    []string  `json:"tags,omitempty"`
	Added   time.Time `json:"added"`
}

// UserAlias is a shorthand the user defined.
type UserAlias struct {
	Name    string    `json:"name"`
	Command string    `json:"command"`
	Note    string    `json:"note,omitempty"`
	Added   time.Time `json:"added"`
}

// ConfigWriter persists a configuration change.
//
// A port rather than a direct call for the usual reason: the file lives in one
// place, the decision to change it is made in another, and the use case in
// between should be testable without a filesystem. It is also what makes
// `wut config set` and `wut shell capture T1` the same operation with two
// spellings, rather than two writers that will disagree eventually.
type ConfigWriter interface {
	// Save writes the whole configuration. Whole, not per-key: a partial
	// writer is how a file ends up holding a value nobody set.
	Save(cfg config.Config) error
	// Path is the file that would be written, for the message that follows.
	Path() string
}

// Clock exists so every time-dependent decision is testable.
type Clock interface {
	Now() time.Time
}

// SystemClock is the real one.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }
