// Package runner executes the Ninja build tool and turns its status output into
// progress events a caller can render.
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"trestle/internal/processx"

	"trestle/internal/diag"
)

// Runner runs an external build command, currently Ninja, honouring the context
// so a build can be cancelled.
type Runner struct{}

// Options controls how a build command is launched: its working directory, the
// extra environment, where its streams go, and an optional raw output sink.
type Options struct {
	Dir       string
	Env       []string
	Stdout    io.Writer
	Stderr    io.Writer
	RawOutput io.Writer
}

// Run looks up executable on PATH and runs it with args, returning a diagnostic
// when the tool is missing or the build fails.
func (Runner) Run(ctx context.Context, executable string, args []string, options Options) error {
	path, err := exec.LookPath(executable)
	if err != nil {
		return diag.New("E_NINJA_NOT_FOUND", diag.StageBuild, fmt.Sprintf("%q is required but was not found", executable), err)
	}
	command := processx.Command(ctx, path, args...)
	command.Dir = options.Dir
	command.Env = append(os.Environ(), options.Env...)
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return diag.New("E_NINJA_FAILED", diag.StageBuild, "ninja "+strings.Join(args, " "), err)
	}
	return nil
}
