package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/thirawat27/wut/internal/port"
)

// IndexPath is where the knowledge index lives.
//
// One fixed name rather than a content-hashed one plus a pointer file: the
// atomic rename in the writer already makes replacement safe, and a pointer
// file is one more thing that can be left dangling on Windows, where symlinks
// are not guaranteed.
func (a *App) IndexPath() string {
	return filepath.Join(a.deps.Dirs.Data, "knowledge", "tldr.idx")
}

// SyncKnowledge rebuilds the index.
//
// The existing index is untouched until the new one is written, so a failed
// sync leaves the user exactly where they were. That matters more than it
// sounds: the most likely time to run `wut db sync` is on a flaky connection
// in an unfamiliar place, and losing a working index there would be worse than
// never having offered the command.
func (a *App) SyncKnowledge(ctx context.Context, opts port.SyncOptions) (port.SyncResult, error) {
	if a.deps.Syncer == nil {
		return port.SyncResult{}, fmt.Errorf("no knowledge source is configured")
	}
	return a.deps.Syncer.Sync(ctx, a.IndexPath(), opts)
}

// KnowledgeStatus reports what is installed.
func (a *App) KnowledgeStatus() KnowledgeStatusReport {
	st := a.deps.Knowledge.Stats()
	rep := KnowledgeStatusReport{
		Ready:     st.Ready,
		Pages:     st.Pages,
		Examples:  st.Examples,
		Vectors:   st.Vectors,
		Release:   st.Release,
		Path:      a.IndexPath(),
		SizeBytes: st.SizeBytes,
	}
	if !st.BuiltAt.IsZero() {
		rep.BuiltAt = st.BuiltAt
		rep.Age = time.Since(st.BuiltAt)
		rep.Stale = a.deps.Config.Knowledge.SyncInterval > 0 &&
			rep.Age > a.deps.Config.Knowledge.SyncInterval
	}
	return rep
}

// KnowledgeStatusReport is what `wut db status` prints.
type KnowledgeStatusReport struct {
	Ready     bool          `json:"ready"`
	Pages     int           `json:"pages"`
	Examples  int           `json:"examples"`
	Vectors   int           `json:"vectors"`
	Release   string        `json:"release,omitempty"`
	Path      string        `json:"path"`
	SizeBytes int64         `json:"size_bytes"`
	BuiltAt   time.Time     `json:"built_at,omitempty"`
	Age       time.Duration `json:"age,omitempty"`
	Stale     bool          `json:"stale"`
}
