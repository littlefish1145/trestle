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

type Runner struct{}

type Options struct {
	Dir       string
	Env       []string
	Stdout    io.Writer
	Stderr    io.Writer
	RawOutput io.Writer
}

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
