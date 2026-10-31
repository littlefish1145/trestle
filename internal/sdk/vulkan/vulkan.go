// Package vulkan locates a Vulkan SDK and builds the shader compiler command
// lines used to translate GLSL sources into SPIR-V.
package vulkan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"trestle/internal/processx"

	"trestle/internal/toolchain"
)

// Stage is a Vulkan shader stage, spelled as the glslang stage suffix.
type Stage string

// The shader stages trestle can compile, named by their glslang suffixes.
const (
	Vertex         Stage = "vert"
	Fragment       Stage = "frag"
	Compute        Stage = "comp"
	Geometry       Stage = "geom"
	TessControl    Stage = "tesc"
	TessEvaluation Stage = "tese"
)

// SDK is a discovered Vulkan installation: its root, version, include and
// library directories, and the shader tools found within it.
type SDK struct {
	Root        string
	Version     string
	IncludeDir  string
	LibraryDirs []string
	Glslc       string
	Glslang     string
	Runner      string
	RunnerArgs  []string
}

// Detect locates the Vulkan SDK named by the VULKAN_SDK environment variable.
func Detect() (SDK, error) {
	root := os.Getenv("VULKAN_SDK")
	if root == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: set VULKAN_SDK")
	}
	return DetectRoot(root)
}

// DetectRoot locates a Vulkan SDK at an explicit root directory.
func DetectRoot(root string) (SDK, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: Vulkan SDK root is required")
	}
	sdk := SDK{Root: root, IncludeDir: filepath.Join(root, "include")}
	sdk.LibraryDirs = []string{filepath.Join(root, "lib")}
	sdk.Glslc = findTool(root, "glslc")
	sdk.Glslang = findTool(root, "glslangValidator")
	if sdk.Glslc == "" && sdk.Glslang == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_NOT_FOUND: glslc or glslangValidator not found under %s", root)
	}
	return sdk, nil
}

// DetectWSL resolves shader tools installed either by a Vulkan SDK archive or
// by the distribution package manager.
func DetectWSL(ctx context.Context, distribution, root string) (SDK, error) {
	if err := ctx.Err(); err != nil {
		return SDK{}, err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = toolchain.WSLVariable(ctx, distribution, "VULKAN_SDK")
	}
	var probeErrors []string
	find := func(name string) string {
		requested := name
		if root != "" {
			requested = path.Join(root, "bin", name)
		}
		resolved, err := toolchain.WSLExecutable(ctx, distribution, requested)
		if err != nil {
			probeErrors = append(probeErrors, err.Error())
		}
		return resolved
	}
	sdk := SDK{Root: root, Glslc: find("glslc"), Glslang: find("glslangValidator")}
	if err := ctx.Err(); err != nil {
		return SDK{}, err
	}
	if sdk.Glslc == "" && sdk.Glslang == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_NOT_FOUND: glslc or glslangValidator was not found in WSL distribution %s: %s", distribution, strings.Join(probeErrors, "; "))
	}
	tool := sdk.Glslc
	if tool == "" {
		tool = sdk.Glslang
	}
	if sdk.Root == "" {
		sdk.Root = path.Dir(path.Dir(tool))
	}
	sdk.IncludeDir = path.Join(sdk.Root, "include")
	sdk.LibraryDirs = []string{path.Join(sdk.Root, "lib")}
	runner, runnerArgs, err := toolchain.WSLRunner(distribution)
	if err != nil {
		return SDK{}, err
	}
	sdk.Runner, sdk.RunnerArgs = runner, runnerArgs
	exe, args := sdk.wrap(tool, []string{"--version"})
	output, err := processx.Command(ctx, exe, args...).Output()
	if ctx.Err() != nil {
		return SDK{}, ctx.Err()
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			output = append(output, exitErr.Stderr...)
		}
		return SDK{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_FAILED: %s --version: %w: %s", tool, err, strings.TrimSpace(string(output)))
	}
	sdk.Version = firstLine(string(output))
	return sdk, nil
}

// WithWSLDirectory returns a copy of the SDK that runs its tools from directory
// inside the WSL distribution.
func WithWSLDirectory(sdk SDK, directory string) SDK {
	if sdk.Runner == "" || strings.TrimSpace(directory) == "" {
		return sdk
	}
	args := make([]string, 0, len(sdk.RunnerArgs)+2)
	for i := 0; i < len(sdk.RunnerArgs); i++ {
		if sdk.RunnerArgs[i] == "--cd" && i+1 < len(sdk.RunnerArgs) {
			i++
			continue
		}
		args = append(args, sdk.RunnerArgs[i])
	}
	sdk.RunnerArgs = append(args, "--cd", directory)
	return sdk
}

func (sdk SDK) wrap(executable string, args []string) (string, []string) {
	if sdk.Runner == "" {
		return executable, args
	}
	wrapped := append([]string{}, sdk.RunnerArgs...)
	wrapped = append(wrapped, "--exec", executable)
	wrapped = append(wrapped, args...)
	return sdk.Runner, wrapped
}

func firstLine(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "unknown"
	}
	return strings.TrimSpace(lines[0])
}

func findTool(root, name string) string {
	candidate := filepath.Join(root, "Bin", name)
	if runtime.GOOS != "windows" {
		candidate = filepath.Join(root, "bin", name)
	}
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		if executable, err := exec.LookPath(candidate); err == nil {
			return executable
		}
	}
	return ""
}

// ShaderSpec describes one shader to compile into a SPIR-V module.
type ShaderSpec struct {
	Source     string
	Output     string
	Stage      Stage
	EntryPoint string
	Includes   []string
	Defines    []string
	TargetEnv  string
	Tool       string
	Depfile    string
}

// ShaderCommand is a resolved program and argument vector ready to execute.
type ShaderCommand struct {
	Exe  string
	Args []string
}

func toolBase(value string) string {
	return strings.TrimSuffix(strings.ToLower(path.Base(strings.ReplaceAll(value, `\`, "/"))), ".exe")
}

// shaderTool returns the shader compiler to run for spec, preferring a tool
// named by the spec, then the SDK's glslc, then its glslangValidator. It returns
// an empty string when none is available.
func (sdk SDK) shaderTool(spec ShaderSpec) string {
	tool := spec.Tool
	switch tool {
	case "glslc", "glslc.exe":
		return sdk.Glslc
	case "glslangValidator", "glslangValidator.exe":
		return sdk.Glslang
	}
	if tool == "" {
		tool = sdk.Glslc
	}
	if tool == "" {
		tool = sdk.Glslang
	}

	return tool
}

// glslangArguments builds the glslangValidator arguments for spec. The source
// file is appended by Compile after the command is wrapped, so it is not
// included here.
func glslangArguments(spec ShaderSpec) []string {
	args := []string{"-V", "-S", string(spec.Stage), "-o", spec.Output}
	if spec.Depfile != "" {
		args = append(args, "--depfile", spec.Depfile)
	}
	if spec.EntryPoint != "" {
		args = append(args, "-e", spec.EntryPoint)
	}
	if spec.TargetEnv != "" {
		args = append(args, "--target-env", spec.TargetEnv)
	}
	for _, include := range spec.Includes {
		args = append(args, "-I"+include)
	}
	for _, define := range spec.Defines {
		args = append(args, "-D"+define)
	}
	return args
}

// glslcArguments builds the glslc arguments for spec. The source file is
// appended by Compile after the command is wrapped, so it is not included here.
func glslcArguments(spec ShaderSpec) []string {
	args := []string{"-fshader-stage=" + string(spec.Stage), "-o", spec.Output}
	if spec.Depfile != "" {
		args = append(args, "-MD", "-MF", spec.Depfile)
	}
	if spec.EntryPoint != "" {
		args = append(args, "-fentry-point="+spec.EntryPoint)
	}
	if spec.TargetEnv != "" {
		args = append(args, "--target-env="+spec.TargetEnv)
	}
	for _, include := range spec.Includes {
		args = append(args, "-I"+include)
	}
	for _, define := range spec.Defines {
		args = append(args, "-D"+define)
	}
	return args
}

// Compile builds the shader-compiler command for spec, preferring glslc and
// falling back to glslangValidator.
func (sdk SDK) Compile(spec ShaderSpec) (ShaderCommand, error) {
	if !ValidStage(spec.Stage) {
		return ShaderCommand{}, fmt.Errorf("E_VULKAN_SHADER_STAGE: unsupported stage %q", spec.Stage)
	}
	if strings.TrimSpace(spec.Source) == "" || strings.TrimSpace(spec.Output) == "" {
		return ShaderCommand{}, fmt.Errorf("E_VULKAN_SHADER_PATH: source and output are required")
	}
	tool := sdk.shaderTool(spec)
	if tool == "" {
		return ShaderCommand{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_NOT_FOUND")
	}
	// glslc emits unescaped dependency paths. Do not silently generate an
	// invalid Ninja depfile when a known dependency path contains whitespace.
	if spec.Depfile != "" && toolBase(tool) != "glslangvalidator" && unsafeDepfilePaths(spec) {
		if spec.Tool == "" && sdk.Glslang != "" {
			tool = sdk.Glslang
		} else {
			return ShaderCommand{}, fmt.Errorf("E_VULKAN_DEPFILE_PATH: glslc does not escape dependency paths containing whitespace; select glslangValidator for this target")
		}
	}
	args := glslcArguments(spec)
	if toolBase(tool) == "glslangvalidator" {
		args = glslangArguments(spec)
	}
	exe, args := sdk.wrap(tool, append(args, spec.Source))
	return ShaderCommand{Exe: exe, Args: args}, nil
}

// Link returns the command that links the given shader modules into a single
// module named name.
func (sdk SDK) Link(name string, inputs []string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("shader link name is required")
	}
	if len(inputs) == 0 {
		return "", fmt.Errorf("shader link requires inputs")
	}
	return strings.Join(append([]string{"link"}, inputs...), " "), nil
}

// ValidStage accepts graphics, compute, mesh and ray-tracing stage suffixes.
func ValidStage(stage Stage) bool {
	switch stage {
	case Vertex, Fragment, Compute, Geometry, TessControl, TessEvaluation, "task", "mesh", "rgen", "rint", "rahit", "rchit", "rmiss", "rcall":
		return true
	}
	return false
}

func unsafeDepfilePaths(spec ShaderSpec) bool {
	for _, value := range append([]string{spec.Source, spec.Output, spec.Depfile}, spec.Includes...) {
		if strings.ContainsAny(value, " \t\r\n#") {
			return true
		}
	}
	return false
}
