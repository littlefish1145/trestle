package cuda

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"trestle/internal/toolchain"
)

type CompileMode uint8

const (
	WholeProgram CompileMode = iota
	SeparateCompilation
)

type Capabilities struct {
	NVCC                bool
	HostPairing         bool
	WholeProgram        bool
	SeparateCompilation bool
}

type Toolkit struct {
	Root    string
	Version string
}

type NVCC struct {
	Path    string
	Version string
}

type Toolchain struct {
	NVCC          NVCC
	Host          toolchain.Toolchain
	Toolkit       Toolkit
	Architectures []string
	Mode          CompileMode
}

func Detect(ctx context.Context, root string) (Toolchain, error) {
	return detect(ctx, root, nil)
}

func DetectWithHost(ctx context.Context, root string, host toolchain.Toolchain) (Toolchain, error) {
	return detect(ctx, root, &host)
}

func detect(ctx context.Context, root string, configuredHost *toolchain.Toolchain) (Toolchain, error) {
	if root == "" {
		root = os.Getenv("CUDA_PATH")
	}
	if root == "" {
		root = os.Getenv("CUDA_HOME")
	}
	if root == "" {
		return Toolchain{}, fmt.Errorf("E_CUDA_NOT_FOUND: set CUDA_PATH or CUDA_HOME")
	}
	nvcc := filepath.Join(root, "bin", "nvcc")
	if runtime.GOOS == "windows" {
		nvcc += ".exe"
	}
	if _, err := os.Stat(nvcc); err != nil {
		return Toolchain{}, fmt.Errorf("E_CUDA_NVCC: %s not found", nvcc)
	}
	versionOutput, _ := exec.CommandContext(ctx, nvcc, "--version").CombinedOutput()
	version := firstLine(string(versionOutput))
	host := toolchain.Toolchain{}
	var err error
	if configuredHost != nil {
		host = *configuredHost
	} else {
		host, err = toolchain.NewDetector().Detect(ctx, "auto")
		if err != nil {
			host, err = toolchain.NewDetector().Detect(ctx, "clang++")
			if err != nil {
				return Toolchain{}, err
			}
		}
	}
	return Toolchain{NVCC: NVCC{Path: nvcc, Version: version}, Host: host, Toolkit: Toolkit{Root: root, Version: version}, Architectures: []string{defaultArchitecture()}}, nil
}

func defaultArchitecture() string {
	if runtime.GOARCH == "arm64" {
		return "sm_80"
	}
	return "sm_75"
}

func firstLine(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(lines[0])
}

func (tc Toolchain) Capabilities(ctx context.Context) Capabilities {
	result := Capabilities{NVCC: tc.NVCC.Path != "", WholeProgram: tc.Mode == WholeProgram, SeparateCompilation: tc.Mode == SeparateCompilation}
	if err := ProbeHost(ctx, tc); err == nil {
		result.HostPairing = true
	}
	return result
}

func (tc Toolchain) Compile(spec toolchain.CompileSpec) (string, []string, error) {
	args := []string{"-ccbin", tc.Host.CXX, "-x", "cu"}
	if spec.CXXStandard != "" {
		// nvcc accepts the same spelling as CMake/MSVC configuration values
		// (for example c++17), not the shortened numeric form (17).
		args = append(args, "-std="+spec.CXXStandard)
	}
	args = append(args, includeArgs(spec.Includes)...)
	for _, define := range spec.Defines {
		args = append(args, "-D"+define)
	}
	for _, architecture := range tc.Architectures {
		number := strings.TrimPrefix(architecture, "sm_")
		args = append(args, "-gencode=arch=compute_"+number+",code=sm_"+number)
	}
	if tc.Mode == SeparateCompilation {
		args = append(args, "-dc")
	}
	args = append(args, "-c", spec.Source, "-o", spec.Output)
	args = append(args, spec.Options...)
	return tc.NVCC.Path, args, nil
}

func includeArgs(includes []string) []string {
	result := make([]string, 0, len(includes))
	for _, include := range includes {
		result = append(result, "-I"+include)
	}
	return result
}

func (tc Toolchain) DeviceLink(objects []string, output string) (string, []string, error) {
	args := []string{"-ccbin", tc.Host.CXX, "-dlink"}
	args = append(args, objects...)
	args = append(args, "-o", output)
	return tc.NVCC.Path, args, nil
}

func (tc Toolchain) HostLink(objects []string, output string, shared bool) (string, []string, error) {
	return tc.Host.Link(toolchain.LinkSpec{Inputs: objects, Output: output, Shared: shared})
}

func ProbeHost(ctx context.Context, tc Toolchain) error {
	dir, err := os.MkdirTemp("", "trestle-cuda-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	source := filepath.Join(dir, "probe.cu")
	output := filepath.Join(dir, "probe.o")
	if err := os.WriteFile(source, []byte("__global__ void kernel() {}\nint main() { return 0; }\n"), 0o644); err != nil {
		return err
	}
	exe, args, err := tc.Compile(toolchain.CompileSpec{Source: source, Output: output, CXXStandard: "c++20"})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, exe, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("E_TOOLCHAIN_CUDA_HOST_INCOMPATIBLE: nvcc -ccbin %s failed: %s", tc.Host.CXX, strings.TrimSpace(string(output)))
	}
	return nil
}
