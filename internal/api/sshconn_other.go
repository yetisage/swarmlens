//go:build !unix

package api

import "os/exec"

// detach has nothing to do where there are no sessions; BatchMode keeps ssh
// from prompting there.
func detach(*exec.Cmd) {}
