package processx

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// Prepare is a no-op on Windows; the process tree is torn down by Terminate.
func Prepare(_ *exec.Cmd) {}

// Terminate kills the process and its children with taskkill, then falls back
// to killing the root process directly.
func Terminate(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	_ = command.Process.Kill()
}
