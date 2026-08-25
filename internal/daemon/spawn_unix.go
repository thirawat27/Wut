//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own process group so closing the terminal that
// started it does not take it down with the shell.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
