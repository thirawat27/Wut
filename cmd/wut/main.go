// Command wut is the whole binary: CLI today, daemon transport later, one
// artifact either way.
//
// This file is the single place adapters are constructed. Everything below it
// receives what it needs and constructs nothing — which is the property the
// prototype lacked, where twelve files built their own storage handle and
// seventeen read a global config singleton.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/thirawat27/wut/internal/adapter/configstore"
	adapterfacts "github.com/thirawat27/wut/internal/adapter/facts"
	"github.com/thirawat27/wut/internal/adapter/knowledge/tldr"
	"github.com/thirawat27/wut/internal/adapter/model/generate"
	"github.com/thirawat27/wut/internal/adapter/nullport"
	shelladapter "github.com/thirawat27/wut/internal/adapter/shell"
	eventstore "github.com/thirawat27/wut/internal/adapter/store/events"
	"github.com/thirawat27/wut/internal/adapter/store/index"
	"github.com/thirawat27/wut/internal/adapter/store/userdata"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/cli"
	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/platform/paths"
	"github.com/thirawat27/wut/internal/port"
)

// version is the release this source is. The release pipeline stamps the same
// value with -ldflags; a build from source reports it too, so a bug report
// never arrives saying "dev".
var version = "1.0.0"

func main() {
	os.Exit(run())
}

// run is separated from main so every deferred cleanup runs before the process
// exits. The prototype called os.Exit from inside a Cobra hook, which skipped
// both.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dirs, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wut: cannot resolve config directories: %v\n", err)
		return cli.ExitError
	}

	store := configstore.New(dirs)
	cfg, err := store.Load()
	if err != nil {
		// A broken config file must not make WUT unusable — it would take the
		// one command that could fix it down with it. Say what is wrong,
		// continue on defaults.
		fmt.Fprintf(os.Stderr, "wut: %v\n", err)
		fmt.Fprintf(os.Stderr, "wut: continuing with defaults; fix it at %s\n", store.Path())
		cfg = config.Default()
	}

	application := app.New(buildDeps(cfg, dirs, store))

	root, _ := cli.Root(application, version)
	root.SetContext(ctx)

	if err := root.Execute(); err != nil {
		return report(err)
	}
	return cli.ExitOK
}

// buildDeps assembles the adapters.
//
// Subsystems that are not installed get their null implementation rather than
// nil. A fresh install has no knowledge index and no model, and that is a
// normal state the whole system is expected to work in, not an error to guard
// against at every call site.
func buildDeps(cfg config.Config, dirs paths.Dirs, cfgStore *configstore.Store) app.Deps {
	deps := app.Deps{
		Config:       cfg,
		Dirs:         dirs,
		Facts:        adapterfacts.NewProvider(),
		Knowledge:    nullport.Knowledge{Reason: "run wut db sync"},
		Events:       nullport.Events{},
		Generator:    nullport.Generator{},
		Embedder:     nullport.Embedder{},
		Syncer:       tldr.NewFetcher(),
		UserData:     userdata.New(dirs.Config),
		ConfigWriter: cfgStore,
		Clock:        port.SystemClock{},
		Version:      version,
	}

	// The event store is the one adapter that can fail to construct — a bad
	// redaction pattern in the user's config, or an unwritable state
	// directory. Falling back to the null store keeps WUT answering questions
	// that never needed history, and doctor reports why.
	if store, err := eventstore.New(dirs.State, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "wut: event store unavailable: %v\n", err)
	} else {
		deps.Events = store
		deps.Shell = shelladapter.New(homeDir(), shelladapter.Params{
			SessionsDir: store.SessionsDir(),
			Alias:       cfg.Shell.Alias,
		})
	}

	// The optional wording model. Configured off by default: most users never
	// install one, and the template path has to be good enough to ship alone.
	if cfg.Model.Tier2 == "ollama" || cfg.Model.Tier2 == "auto" {
		deps.Generator = generate.NewOllama(cfg.Model.Ollama, cfg.Model.Tier2ID)
	}

	// A missing index is the normal state on a fresh install and must stay
	// silent — the notes on each command already say "run wut db sync". A
	// *damaged* one is different: the user thinks they have an index, so say
	// so once and keep going without it.
	indexPath := filepath.Join(dirs.Data, "knowledge", "tldr.idx")
	if reader, err := index.Open(indexPath); err == nil {
		deps.Knowledge = reader
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "wut: %v\n", err)
		fmt.Fprintf(os.Stderr, "wut: continuing without it; rebuild with: wut db sync\n")
	}
	return deps
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// report prints an error in the right shape and returns its exit code.
func report(err error) int {
	code, hint := cli.CodeFor(err)
	if msg := err.Error(); msg != "" {
		fmt.Fprintf(os.Stderr, "wut: %v\n", err)
		if hint != "" {
			fmt.Fprintf(os.Stderr, "     %s\n", hint)
		}
	}
	if errors.Is(err, context.Canceled) {
		return cli.ExitCancelled
	}
	return code
}
