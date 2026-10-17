// Package msvc implements the C++ modules backend for MSVC. It scans with
// /scanDependencies and compiles module interfaces into IFC files with
// /interface.
package msvc

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

// Backend implements the modules.Support contract for MSVC, using
// /scanDependencies and the IFC output produced by /interface.
type Backend struct {
	Compiler string
	Standard string
	Options  []string
}

// Scan runs cl /scanDependencies and decodes the JSON dependency file it
// writes.
func (backend Backend) Scan(ctx context.Context, source string) (p1689.Document, error) {
	directory, err := os.MkdirTemp("", "trestle-msvc-module-")
	if err != nil {
		return p1689.Document{}, err
	}
	defer os.RemoveAll(directory)
	scanFile := filepath.Join(directory, "scan.json")
	objectFile := filepath.Join(directory, "scan.obj")
	args := []string{"/nologo", "/std:c++20", "/scanDependencies", scanFile, "/c", source, "/Fo" + objectFile}
	args = append(args, backend.Options...)
	command := processx.Command(ctx, backend.Compiler, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return p1689.Document{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %s", source, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(scanFile)
	if err != nil {
		return p1689.Document{}, err
	}
	return p1689.Decode(bytes.NewReader(data))
}

// CompileModule compiles a module interface with /interface, writing the IFC
// beside output and attaching every reference as a /reference option.
func (backend Backend) CompileModule(source, output string, references []modules.Reference) (string, []string, error) {
	ifc := filepath.Join(filepath.Dir(output), filepath.Base(output)+".ifc")
	args := []string{"/nologo", "/std:c++20", "/interface", "/c", source, "/Fo" + output, "/ifcOutput", ifc}
	for _, reference := range references {
		args = append(args, "/reference", reference.LogicalName+"="+reference.Path)
	}
	return backend.Compiler, append(args, backend.Options...), nil
}

// ConsumerArgs renders references as /reference options so consumers can
// import the named interfaces.
func (backend Backend) ConsumerArgs(references []modules.Reference) ([]string, error) {
	result := make([]string, 0, len(references)*2)
	for _, reference := range references {
		if reference.LogicalName == "" || reference.Path == "" {
			return nil, fmt.Errorf("E_MODULE_REFERENCE: module reference requires logical name and path")
		}
		result = append(result, "/reference", reference.LogicalName+"="+reference.Path)
	}
	return result, nil
}
