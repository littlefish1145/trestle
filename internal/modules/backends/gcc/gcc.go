// Package gcc implements the C++ modules backend for GCC. It scans with
// -fdeps-format=p1689r5 and compiles module interfaces through -fmodules-ts.
package gcc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"trestle/internal/processx"

	"trestle/internal/modules"
	"trestle/internal/modules/p1689"
)

// Backend implements the modules.Support contract for GCC, using -fmodules-ts
// and the p1689r5 dependency format.
type Backend struct {
	Compiler string
	Standard string
	Options  []string
}

// Scan runs the compiler in dependency-scanning mode and decodes the p1689r5
// file it writes.
func (backend Backend) Scan(ctx context.Context, source string) (p1689.Document, error) {
	directory, err := os.MkdirTemp("", "trestle-gcc-module-")
	if err != nil {
		return p1689.Document{}, err
	}
	defer os.RemoveAll(directory)
	dependencyFile := filepath.Join(directory, "deps.json")
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-fmodules-ts", "-fdeps-format=p1689r5", "-fdeps-file=" + dependencyFile, "-c", source, "-o", filepath.Join(directory, "scan.o")}
	args = append(args, backend.Options...)
	command := processx.Command(ctx, backend.Compiler, args...)
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

// CompileModule builds source into a module interface object at output,
// referencing the already-built modules in references.
func (backend Backend) CompileModule(source, output string, references []modules.Reference) (string, []string, error) {
	args := []string{"-std=" + strings.TrimPrefix(backend.Standard, "c++"), "-fmodules-ts", "-c", source, "-o", output}
	consumerArgs, err := backend.ConsumerArgs(references)
	if err != nil {
		return "", nil, err
	}
	args = append(args, consumerArgs...)
	return backend.Compiler, append(args, backend.Options...), nil
}

// ConsumerArgs renders references as -fmodule-reference flags so GCC can find
// imported module interfaces.
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
