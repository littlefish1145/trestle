//go:build !windows

package cmake

import (
	"context"
	"os/exec"

	"trestle/internal/processx"
)

func workspaceCommand(ctx context.Context, exe string, env, args []string) *exec.Cmd {
	command := processx.Command(ctx, exe, args...)
	command.Env = env
	return command
}
