//go:build unix

package api

import (
	"os/exec"
	"syscall"
)

// detach puts the process in a session of its own. That takes away its
// controlling terminal, so that ssh cannot prompt on the terminal swarmlens is
// drawing on, and keeps it out of the terminal's signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
