package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/platform/tty"
)

func newUICmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "ui [question]",
		Aliases: []string{"u"},
		Short:   "Search, read, and keep commands on one screen",
		Long: `One screen with three panes.

  ask         type a question, see answers and the reasons under each
  history     what your shell has run, and how it went
  knowledge   the tldr page behind the selected answer, offline

Tab moves between them. Enter in the ask pane accepts a command and prints it;
enter in the history pane loads that command into the question box, which is
what you want after something failed. Nothing is ever run for you.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd, env, strings.TrimSpace(strings.Join(args, " ")))
		},
	}
}

func runUI(cmd *cobra.Command, env *Env, initial string) error {
	term, err := tty.Open()
	if err != nil {
		return &exitError{
			code: ExitNotFound,
			err:  fmt.Errorf("wut ui needs a terminal"),
			hint: `ask without one: wut "how do I squash the last 3 commits"`,
		}
	}
	defer term.Close()

	ctx := cmd.Context()
	deps := env.App.Deps()

	ui := &render.UI{
		Term:    term,
		Style:   render.NewStyle(true, 0),
		Search:  searchFn(ctx, env),
		History: historyFn(ctx, env),
		Page:    pageFn(ctx, env),
	}
	// Saving is offered only when there is somewhere to save to. A key that
	// silently does nothing is worse than a key that is not advertised.
	if deps.UserData != nil {
		ui.Save = func(command string) error {
			_, err := deps.UserData.Add(command, "", nil)
			return err
		}
	}

	out, err := ui.Run(initial)
	if err != nil {
		return err
	}
	if out.Command == "" {
		return silent(ExitCancelled)
	}

	// The accepted command goes to stdout, never to a shell. WUT prints what
	// it would run and stops there; running it is the user's keystroke, in
	// their own shell, with their own history.
	fmt.Fprintln(os.Stdout, out.Command)
	return nil
}

// searchFn adapts the Ask use case to what the UI needs: a query in,
// candidates out, no context or transport visible.
func searchFn(ctx context.Context, env *Env) func(string) ([]candidate.Candidate, error) {
	return func(query string) ([]candidate.Candidate, error) {
		res, err := env.App.Ask(ctx, app.AskRequest{Question: query, Cwd: env.Cwd(), Limit: 12})
		if err != nil {
			return nil, err
		}
		return res.Candidates, nil
	}
}

func historyFn(ctx context.Context, env *Env) func() ([]render.Entry, error) {
	return func() ([]render.Entry, error) {
		events, err := env.App.Deps().Events.Recent(ctx, event.Filter{Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]render.Entry, 0, len(events))
		for _, e := range events {
			out = append(out, render.Entry{
				Command:  e.Raw,
				ExitCode: e.ExitCode,
				At:       e.At,
				Dir:      e.Cwd,
			})
		}
		return out, nil
	}
}

func pageFn(ctx context.Context, env *Env) func(string) (knowledge.Page, bool) {
	return func(name string) (knowledge.Page, bool) {
		page, ok, err := env.App.LookupPage(ctx, name)
		if err != nil {
			return knowledge.Page{}, false
		}
		return page, ok
	}
}
