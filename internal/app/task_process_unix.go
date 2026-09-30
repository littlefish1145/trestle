//go:build !windows

package app

import (
	"os/exec"
	"trestle/internal/processx"
)

func prepareTaskProcess(command *exec.Cmd)   { processx.Prepare(command) }
func terminateTaskProcess(command *exec.Cmd) { processx.Terminate(command) }
