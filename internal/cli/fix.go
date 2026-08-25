package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/daemon"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// maxPipedStderr caps what the T2 path will read. Output arrives from an
// arbitrary program, so it is bounded before it reaches memory.
const maxPipedStderr = 256 << 10

func newFixCmd(env *Env) *cobra.Command {
	var (
		shellMode bool
		noConfirm bool
		limit     int
	)

	cmd := &cobra.Command{
		Use:   "fix [command]",
		Short: "Correct a command",
		Long: `Correct a command.

With no argument, WUT corrects the last command that failed in this shell,
which is what bare "wut" does. Give it a command to correct that one instead.

You can also pipe the error text in, which lets WUT use rules that need to see
what went wrong:

    npm run biuld 2>&1 | wut fix`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.FixRequest{
				Command: strings.Join(args, " "),
				Cwd:     env.Cwd(),
				Session: env.Session(),
				Stderr:  readPipedStderr(),
				Limit:   limit,
			}
			if shellMode {
				return runShellMode(cmd, env, req, noConfirm)
			}
			res, err := viaDaemon(env,
				func(c *daemon.Client) (app.Result, error) { return c.Fix(cmd.Context(), req) },
				func() (app.Result, error) { return env.App.Fix(cmd.Context(), req) },
			)
			if err != nil {
				return err
			}
			return presentInteractive(env, res, headerFor(res))
		},
	}

	f := cmd.Flags()
	f.BoolVar(&shellMode, "shell", false, "emit only the accepted command on stdout, for the shell function")
	f.BoolVar(&noConfirm, "no-confirm", false, "in shell mode, take the best candidate without asking")
	f.IntVar(&limit, "limit", 5, "maximum candidates to consider")
	return cmd
}

func headerFor(res app.Result) string {
	if res.Query == "" {
		return ""
	}
	return "for: " + res.Query
}

// readPipedStderr reads output the user piped in explicitly.
//
// Only when stdin is *not* a terminal: reading from a terminal here would hang
// waiting for input the user was never asked for.
func readPipedStderr() string {
	if tty.IsStdinTerminal() {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxPipedStderr))
	if err != nil {
		return ""
	}
	return string(data)
}

// runShellMode is the path the shell function calls.
//
// The contract, and the reason every branch below refuses rather than guesses:
// stdout is a pipe, and whatever lands on it is about to be executed by the
// user's shell. So the picker draws on the controlling terminal, only the
// accepted command reaches stdout, and if there is no terminal to draw on
// nothing is emitted at all — handing back a command nobody saw is the one
// failure mode this design exists to prevent.
func runShellMode(cmd *cobra.Command, env *Env, req app.FixRequest, noConfirm bool) error {
	res, err := env.App.Fix(cmd.Context(), req)
	if err != nil {
		return err
	}
	if res.Empty() {
		// Notes go to stderr: stdout is reserved for the command.
		for _, n := range res.Notes {
			fmt.Fprintln(os.Stderr, n)
		}
		return silent(ExitNotFound)
	}

	if noConfirm {
		top := res.Candidates[0]
		if top.Risk.Blocking() {
			fmt.Fprintf(os.Stderr, "wut: refusing to auto-accept a %s command (%s)\n", top.Risk.Level, top.Risk.Rule)
			return silent(ExitRefused)
		}
		fmt.Fprintln(os.Stdout, top.Command)
		return nil
	}

	term, err := tty.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wut: no terminal to show the suggestion on, so nothing was emitted")
		return silent(ExitNotFound)
	}
	defer term.Close()

	p := &render.Picker{
		Term:   term,
		Style:  render.NewStyle(true, 80),
		Header: headerFor(res),
		// Never in shell mode: acceptance here runs the command immediately.
		AllowRisky: false,
	}
	choice, err := p.Run(res.Candidates)
	switch {
	case errors.Is(err, render.ErrCancelled):
		return silent(ExitCancelled)
	case errors.Is(err, render.ErrRefused):
		fmt.Fprintf(os.Stderr, "wut: %v\n", err)
		return silent(ExitRefused)
	case err != nil:
		return err
	}

	if choice.Action == render.ActionEdit {
		// Edit means "put it on my command line, do not run it". The shell
		// function reads this marker on stderr and pre-fills the line editor.
		fmt.Fprintln(os.Stderr, "wut-edit")
		fmt.Fprintln(os.Stdout, choice.Candidate.Command)
		return nil
	}
	fmt.Fprintln(os.Stdout, choice.Candidate.Command)
	return nil
}
