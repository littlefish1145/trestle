package clang

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"trestle/internal/processx"

	"trestle/internal/modules"
	"trestle/internal/modules/p1689"
)

type Backend struct {
	Compiler   string
	Scanner    string
	Standard   string
	Target     string
	Options    []string
	Runner     string
	RunnerArgs []string
}

func (backend Backend) wrap(args []string) (string, []string) {
	if backend.Runner == "" {
		return backend.Compiler, args
	}
	wrapped := append(append(append([]string{}, backend.RunnerArgs...), "--exec", backend.Compiler), args...)
	return backend.Runner, wrapped
}

func (backend Backend) Scan(ctx context.Context, source string) (p1689.Document, error) {
	if backend.Scanner == "" {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: clang-scan-deps is not configured")
	}
	args := []string{"-format=p1689", "--", backend.Compiler, "-std=" + backend.Standard, "-c", source}
	args = append(args, backend.Options...)
	executable := backend.Scanner
	if backend.Runner != "" {
		executable = backend.Runner
		args = append(append(append([]string{}, backend.RunnerArgs...), "--exec", backend.Scanner), args...)
	}
	command := processx.Command(ctx, executable, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %w: %s", source, err, strings.TrimSpace(stderr.String()))
	}
	return p1689.Decode(bytes.NewReader(output))
}

func (backend Backend) CompileModule(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + backend.Standard, "--precompile", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	args = append(args, backend.Options...)
	exe, args := backend.wrap(args)
	return exe, args, nil
}

func (backend Backend) CompileModuleObject(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + backend.Standard, "-MMD", "-MF", output + ".d", "-c", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	exe, args := backend.wrap(append(args, backend.Options...))
	return exe, args, nil
}

func (backend Backend) ConsumerArgs(references []modules.Reference) ([]string, error) {
	result := make([]string, 0, len(references)*2)
	for _, reference := range references {
		if reference.LogicalName == "" || reference.Path == "" {
			return nil, fmt.Errorf("E_MODULE_REFERENCE: module reference requires logical name and path")
		}
		result = append(result, "-fmodule-file="+reference.LogicalName+"="+reference.Path)
	}
	return result, nil
}
