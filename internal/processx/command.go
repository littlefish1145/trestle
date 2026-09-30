package processx

import (
	"context"
	"os/exec"
	"time"
)

// Command cancels the process tree, not only the pipe-owning parent.
func Command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, args...)
	Prepare(command)
	command.WaitDelay = 2 * time.Second
	command.Cancel = func() error { Terminate(command); return nil }
	return command
}
