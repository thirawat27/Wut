// Package tty gives access to the *controlling terminal*, which is not the
// same thing as stdin and stdout.
//
// This distinction is the single mechanism that makes the repair flow work.
// When the shell function runs
//
//	fixed=$(wut --shell)
//
// stdout is a pipe carrying the accepted command back to the shell, which then
// runs it in the current process so `cd` and `export` take effect. The picker
// therefore cannot draw on stdout — it draws here, on the terminal the user is
// actually looking at.
//
// With no controlling terminal (CI, a pipe, an editor task runner) Open fails,
// and the caller must decline rather than hand back a command nobody saw.
package tty

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/term"
)

// ErrNoTerminal reports that this process has no controlling terminal.
var ErrNoTerminal = errors.New("no controlling terminal")

// Terminal is an open pair of handles onto the controlling terminal.
type Terminal struct {
	In  *os.File
	Out *os.File

	sameFile bool
	oldState *term.State
}

// Open acquires the controlling terminal, or returns ErrNoTerminal.
func Open() (*Terminal, error) {
	in, out, same, err := openPlatform()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoTerminal, err)
	}
	return &Terminal{In: in, Out: out, sameFile: same}, nil
}

// Available reports whether a controlling terminal can be opened, without
// keeping it. Used by `wut doctor` and by the decision to draw a picker at
// all.
func Available() bool {
	t, err := Open()
	if err != nil {
		return false
	}
	_ = t.Close()
	return true
}

// Close releases the handles, restoring cooked mode first if needed.
func (t *Terminal) Close() error {
	t.Restore()
	var firstErr error
	if t.In != nil {
		if err := t.In.Close(); err != nil {
			firstErr = err
		}
	}
	if t.Out != nil && !t.sameFile {
		if err := t.Out.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// MakeRaw switches the terminal to raw mode so single keypresses arrive
// without waiting for Enter. Restore must be called, and is also called by
// Close, because a process that dies in raw mode leaves the user's shell
// unusable.
func (t *Terminal) MakeRaw() error {
	if t.oldState != nil {
		return nil
	}
	st, err := term.MakeRaw(int(t.In.Fd()))
	if err != nil {
		return err
	}
	t.oldState = st
	return nil
}

// Restore returns the terminal to cooked mode. It is safe to call twice.
func (t *Terminal) Restore() {
	if t.oldState == nil || t.In == nil {
		return
	}
	_ = term.Restore(int(t.In.Fd()), t.oldState)
	t.oldState = nil
}

// Size returns the terminal dimensions, with a usable fallback when they
// cannot be read.
func (t *Terminal) Size() (width, height int) {
	w, h, err := term.GetSize(int(t.Out.Fd()))
	if err != nil || w <= 0 {
		return 80, 24
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

// Write sends bytes to the terminal, never to stdout.
func (t *Terminal) Write(p []byte) (int, error) { return t.Out.Write(p) }

// WriteString is the convenience form.
func (t *Terminal) WriteString(s string) { _, _ = t.Out.WriteString(s) }

// StdoutWidth reports the width of stdout, for non-interactive rendering.
func StdoutWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	if runtime.GOOS == "windows" {
		return 80
	}
	return 80
}

// IsStdoutTerminal reports whether stdout is a terminal, which decides styling
// and whether a spinner is appropriate.
func IsStdoutTerminal() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// IsStdinTerminal reports whether stdin is a terminal.
func IsStdinTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
