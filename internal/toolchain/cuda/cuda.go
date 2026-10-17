// Package cuda detects an NVIDIA CUDA toolkit and builds the nvcc command lines
// needed to compile and device-link kernels, paired with a host C++ toolchain.
package cuda

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"trestle/internal/toolchain"
)

// CompileMode selects how nvcc treats device code across translation units.
type CompileMode uint8

// The nvcc compilation strategies: one whole-program pass, or per-file separate
// compilation followed by a device link.
const (
	WholeProgram CompileMode = iota
	SeparateCompilation
)

// Capabilities reports which CUDA features are usable for a toolchain.
type Capabilities struct {
	NVCC                bool
	HostPairing         bool
	WholeProgram        bool
	SeparateCompilation bool
}

// Toolkit is a CUDA installation root together with the version it provides.
type Toolkit struct {
	Root    string
	Version string
}

// NVCC is a located nvcc executable and the version banner it reports.
type NVCC struct {
	Path    string
	Version string
}

// Toolchain pairs an nvcc installation with the host C++ toolchain it compiles
// against, plus the target architectures and compilation mode in effect.
type Toolchain struct {
	NVCC          NVCC
	Host          toolchain.Toolchain
	Toolkit       Toolkit
	Architectures []string
	Mode          CompileMode
}

// Detect discovers the CUDA toolkit under root, or under the CUDA_PATH and
// CUDA_HOME environment variables when root is empty, and pairs it with an
// auto-detected host compiler.
func Detect(ctx context.Context, root string) (Toolchain, error) {
	return detect(ctx, root, nil)
}

// DetectWithHost is Detect with an explicit host toolchain instead of an
// auto-detected one.
func DetectWithHost(ctx context.Context, root string, host toolchain.Toolchain) (Toolchain, error) {
	return detect(ctx, root, &host)
}

// DetectWSL resolves an nvcc installation inside a WSL distribution and pairs
// it with the already selected WSL host compiler.
func DetectWSL(ctx context.Context, distribution, root string, host toolchain.Toolchain) (Toolchain, error) {
	if host.Runner == "" {
		return Toolchain{}, fmt.Errorf("E_CUDA_WSL_HOST: WSL CUDA requires a WSL C/C++ toolchain")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = toolchain.WSLVariable(ctx, distribution, "CUDA_HOME")
	}
	if root == "" {
		root = toolchain.WSLVariable(ctx, distribution, "CUDA_PATH")
	}
	requested := "nvcc"
	if root != "" {
		requested = path.Join(root, "bin", "nvcc")
	}
	nvcc, err := toolchain.WSLExecutable(ctx, distribution, requested)
	if err != nil {
		return Toolchain{}, fmt.Errorf("E_CUDA_NVCC: %s was not found in WSL distribution %s", requested, distribution)
	}
	if root == "" {
		root = path.Dir(path.Dir(nvcc))
	}
	exe, args := host.Wrap(nvcc, []string{"--version"})
	versionOutput, _ := exec.CommandContext(ctx, exe, args...).Output()
	version := firstLine(string(versionOutput))
	return Toolchain{NVCC: NVCC{Path: nvcc, Version: version}, Host: host, Toolkit: Toolkit{Root: root, Version: version}, Architectures: []string{defaultArchitecture()}}, nil
}

func detect(ctx context.Context, root string, configuredHost *toolchain.Toolchain) (Toolchain, error) {
	root = toolkitRoot(root)
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
	host, err := resolveHost(ctx, configuredHost)
	if err != nil {
		return Toolchain{}, err
	}
	return Toolchain{NVCC: NVCC{Path: nvcc, Version: version}, Host: host, Toolkit: Toolkit{Root: root, Version: version}, Architectures: []string{defaultArchitecture()}}, nil
}

// toolkitRoot returns root when it is set, otherwise the CUDA_PATH or CUDA_HOME
// environment variable.
func toolkitRoot(root string) string {
	if root == "" {
		root = os.Getenv("CUDA_PATH")
	}
	if root == "" {
		root = os.Getenv("CUDA_HOME")
	}
	return root
}

// resolveHost returns the configured host toolchain when one was supplied,
// otherwise it auto-detects one, falling back from the platform default to
// clang++ before reporting the detection error.
func resolveHost(ctx context.Context, configuredHost *toolchain.Toolchain) (toolchain.Toolchain, error) {
	if configuredHost != nil {
		return *configuredHost, nil
	}
	host, err := toolchain.NewDetector().Detect(ctx, "auto")
	if err == nil {
		return host, nil
	}
	host, err = toolchain.NewDetector().Detect(ctx, "clang++")
	if err != nil {
		return toolchain.Toolchain{}, err
	}
	return host, nil
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

// Capabilities probes the toolchain to report which CUDA features are
// available, including whether nvcc accepts the host compiler.
func (tc Toolchain) Capabilities(ctx context.Context) Capabilities {
	result := Capabilities{NVCC: tc.NVCC.Path != "", WholeProgram: tc.Mode == WholeProgram, SeparateCompilation: tc.Mode == SeparateCompilation}
	if err := ProbeHost(ctx, tc); err == nil {
		result.HostPairing = true
	}
	return result
}

// Compile builds the nvcc argument vector that compiles one CUDA source file
// for the toolchain's target architectures.
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
	args = append(args, architectureArgs(tc.Architectures)...)
	if tc.Mode == SeparateCompilation {
		args = append(args, "-dc")
	}
	args = append(args, "-c", spec.Source, "-o", spec.Output)
	args = append(args, spec.Options...)
	exe, args := tc.Host.Wrap(tc.NVCC.Path, args)
	return exe, args, nil
}

// architectureArgs returns the nvcc -gencode flags targeting each sm_ version in
// architectures, preserving their order.
func architectureArgs(architectures []string) []string {
	result := make([]string, 0, len(architectures))
	for _, architecture := range architectures {
		number := strings.TrimPrefix(architecture, "sm_")
		result = append(result, "-gencode=arch=compute_"+number+",code=sm_"+number)
	}
	return result
}

func includeArgs(includes []string) []string {
	result := make([]string, 0, len(includes))
	for _, include := range includes {
		result = append(result, "-I"+include)
	}
	return result
}

// DeviceLink builds the nvcc -dlink argument vector that links separately
// compiled device code into a single object.
func (tc Toolchain) DeviceLink(objects []string, output string) (string, []string, error) {
	args := []string{"-ccbin", tc.Host.CXX, "-dlink"}
	args = append(args, objects...)
	args = append(args, "-o", output)
	exe, args := tc.Host.Wrap(tc.NVCC.Path, args)
	return exe, args, nil
}

// HostLink links the given objects with the host toolchain, producing a shared
// library when shared is set.
func (tc Toolchain) HostLink(objects []string, output string, shared bool) (string, []string, error) {
	return tc.Host.Link(toolchain.LinkSpec{Inputs: objects, Output: output, Shared: shared})
}

// ProbeHost compiles a tiny kernel with nvcc -ccbin to verify that the host
// compiler is usable with the toolkit, returning an error otherwise.
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
	compileSource, compileOutput, err := probePaths(ctx, tc.Host, source, output)
	if err != nil {
		return err
	}
	probeToolchain := tc
	if probeToolchain.Host.Runner != "" {
		probeToolchain.Host.RunnerArgs = withoutRunnerDirectory(probeToolchain.Host.RunnerArgs)
	}
	exe, args, err := probeToolchain.Compile(toolchain.CompileSpec{Source: compileSource, Output: compileOutput, CXXStandard: "c++20"})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, exe, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("E_TOOLCHAIN_CUDA_HOST_INCOMPATIBLE: nvcc -ccbin %s failed: %s", tc.Host.CXX, strings.TrimSpace(string(output)))
	}
	return nil
}

// probePaths translates the probe source and output paths into the host
// toolchain's filesystem, converting them to WSL paths when the host runs
// through a runner. It returns the paths unchanged otherwise.
func probePaths(ctx context.Context, host toolchain.Toolchain, source, output string) (string, string, error) {
	if host.Runner == "" {
		return source, output, nil
	}
	distribution := runnerDistribution(host.RunnerArgs)
	compileSource, err := toolchain.WSLPath(ctx, distribution, source)
	if err != nil {
		return "", "", err
	}
	compileOutput, err := toolchain.WSLPath(ctx, distribution, output)
	if err != nil {
		return "", "", err
	}
	return compileSource, compileOutput, nil
}

func runnerDistribution(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-d" {
			return args[index+1]
		}
	}
	return ""
}

func withoutRunnerDirectory(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "--cd" && index+1 < len(args) {
			index++
			continue
		}
		result = append(result, args[index])
	}
	return result
}
