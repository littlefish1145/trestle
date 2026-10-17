//go:build !windows

package processx

import (
	"os/exec"
	"syscall"
)

// Prepare puts the child in its own process group so the whole group can be
// signalled at once.
func Prepare(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Terminate sends SIGKILL to the child's process group and then to the process
// itself.
func Terminate(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	_ = command.Process.Kill()
}
