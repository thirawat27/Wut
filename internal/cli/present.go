package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// exitError carries an exit code up to main, so nothing below main ever calls
// os.Exit and every deferred cleanup still runs.
//
// the prototype called os.Exit inside a Cobra pre-run hook, which skipped the
// post-run hook and every pending defer, including closing the database.
type exitError struct {
	code int
	err  error
	hint string
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// CodeFor extracts the exit code an error carries, defaulting to ExitError.
// Exported because main is the only place allowed to exit.
func CodeFor(err error) (int, string) {
	if err == nil {
		return ExitOK, ""
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, ee.hint
	}
	// Cobra reports a bad flag or a bad argument count as a plain error, which
	// would exit 1 — "internal error" — for what is squarely a usage error.
	// Exit codes are part of the contract, and a script that branches on 2
	// must see 2 when the user mistyped a flag.
	if isUsageError(err) {
		return ExitUsage, "run: wut <command> --help"
	}
	return ExitError, ""
}

// isUsageError recognises the errors Cobra produces for a malformed command
// line. It matches on the message because Cobra does not give them a type.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{
		"unknown flag",
		"unknown shorthand flag",
		"unknown command",
		"flag needs an argument",
		"invalid argument",
		"accepts ",
		"requires at least",
		"requires exactly",
		"bad flag syntax",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// silent is an error that sets an exit code without printing anything. It is
// how "nothing found" is reported: the note has already been shown, and a
// second line saying "error: not found" would be noise.
func silent(code int) error { return &exitError{code: code} }

// present renders a result in whichever mode was asked for, and returns the
// exit code as an error.
func present(env *Env, res app.Result, header string) error {
	if env.JSON() {
		j := render.NewJSON(os.Stdout)
		if err := j.Result(res.Kind, res.Query, res.Candidates, res.Notes...); err != nil {
			return err
		}
		if res.Empty() {
			return silent(ExitNotFound)
		}
		return nil
	}

	t := env.Text()
	t.Result(header, res.Candidates)
	for _, n := range res.Notes {
		t.Note("%s", n)
	}
	if res.Empty() {
		return silent(ExitNotFound)
	}
	return nil
}

// presentInteractive draws the picker when there is a terminal and more than
// one candidate, and falls back to a plain listing otherwise.
//
// The picker is a convenience here. In shell mode it is the whole mechanism —
// see runShellMode.
func presentInteractive(env *Env, res app.Result, header string) error {
	if env.JSON() || len(res.Candidates) < 2 || !tty.IsStdoutTerminal() {
		return present(env, res, header)
	}

	term, err := tty.Open()
	if err != nil {
		return present(env, res, header)
	}
	defer term.Close()

	p := &render.Picker{
		Term:       term,
		Style:      env.Style(),
		Header:     header,
		AllowRisky: true, // printing a risky command is not running it
	}
	choice, err := p.Run(res.Candidates)
	switch {
	case errors.Is(err, render.ErrCancelled):
		return silent(ExitCancelled)
	case err != nil:
		return present(env, res, header)
	}

	// Outside shell mode the picker's job ends at "which one" — print it, and
	// let the user copy or edit it. Nothing is executed here or anywhere else.
	fmt.Fprintln(os.Stdout, choice.Candidate.Command)
	return nil
}
