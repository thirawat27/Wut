package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Spawn starts a background daemon by re-executing this binary.
//
// It lives here rather than in internal/cli on purpose. Starting a process is
// the single most dangerous thing this program can do, so it is confined to
// the two packages an architecture test allows — the fact prober and this one.
// Keeping it out of the CLI means no code path that touches user input is even
// able to reach os/exec.
//
// What is started is fixed: this executable, with the literal arguments
// "daemon" and "run". Nothing derived from anything a user typed is involved.
func Spawn(dir string, wait time.Duration) (int, error) {
	client := NewClient(dir)
	if client.Available() {
		return 0, ErrAlreadyRunning
	}

	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// The child must not try to reach a daemon of its own.
	cmd.Env = append(os.Environ(), "WUT_NO_DAEMON=")
	detach(cmd)

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	// Wait for it to bind, so "started" means started rather than "asked to".
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if client.Available() {
			return pid, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid, fmt.Errorf("the daemon did not start within %s", wait)
}

// ErrAlreadyRunning reports a daemon that was already up.
var ErrAlreadyRunning = errors.New("a daemon is already running")
