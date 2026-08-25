//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon without a console window. Without this, `wut daemon
// start` flashes a console on every launch, which looks like a malfunction.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x08000000, // DETACHED_PROCESS | CREATE_NO_WINDOW
	}
}
