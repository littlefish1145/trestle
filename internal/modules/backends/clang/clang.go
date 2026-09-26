package clang

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"trestle/internal/modules"
	"trestle/internal/modules/p1689"
)

type Backend struct {
	Compiler string
	Scanner  string
	Standard string
	Target   string
	Options  []string
}

func (backend Backend) Scan(ctx context.Context, source string) (p1689.Document, error) {
	if backend.Scanner == "" {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: clang-scan-deps is not configured")
	}
	args := []string{"-format=p1689", "--", backend.Compiler, "-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-c", source}
	args = append(args, backend.Options...)
	command := exec.CommandContext(ctx, backend.Scanner, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %s", source, strings.TrimSpace(stderr.String()))
	}
	return p1689.Decode(bytes.NewReader(output))
}

func (backend Backend) CompileModule(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "--precompile", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	args = append(args, backend.Options...)
	return backend.Compiler, args, nil
}

func (backend Backend) CompileModuleObject(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-c", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	return backend.Compiler, append(args, backend.Options...), nil
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
