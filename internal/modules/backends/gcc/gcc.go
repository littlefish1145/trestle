package gcc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trestle/internal/modules"
	"trestle/internal/modules/p1689"
)

type Backend struct {
	Compiler string
	Standard string
	Options  []string
}

func (backend Backend) Scan(ctx context.Context, source string) (p1689.Document, error) {
	directory, err := os.MkdirTemp("", "trestle-gcc-module-")
	if err != nil {
		return p1689.Document{}, err
	}
	defer os.RemoveAll(directory)
	dependencyFile := filepath.Join(directory, "deps.json")
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-fmodules-ts", "-fdeps-format=p1689r5", "-fdeps-file=" + dependencyFile, "-c", source, "-o", filepath.Join(directory, "scan.o")}
	args = append(args, backend.Options...)
	command := exec.CommandContext(ctx, backend.Compiler, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %s", source, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(dependencyFile)
	if err != nil {
		return p1689.Document{}, err
	}
	return p1689.Decode(bytes.NewReader(data))
}

func (backend Backend) CompileModule(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-fmodules-ts", "-c", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	return backend.Compiler, append(args, backend.Options...), nil
}

func (backend Backend) ConsumerArgs(references []modules.Reference) ([]string, error) {
	result := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.LogicalName == "" || reference.Path == "" {
			return nil, fmt.Errorf("E_MODULE_REFERENCE: module reference requires logical name and path")
		}
		result = append(result, "-fmodule-reference="+reference.LogicalName+"="+reference.Path)
	}
	return result, nil
}
