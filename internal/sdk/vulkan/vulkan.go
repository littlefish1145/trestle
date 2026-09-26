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

	"trestle/internal/toolchain"
)

type Stage string

const (
	Vertex         Stage = "vert"
	Fragment       Stage = "frag"
	Compute        Stage = "comp"
	Geometry       Stage = "geom"
	TessControl    Stage = "tesc"
	TessEvaluation Stage = "tese"
)

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

func Detect() (SDK, error) {
	root := os.Getenv("VULKAN_SDK")
	if root == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: set VULKAN_SDK")
	}
	return DetectRoot(root)
}

func DetectRoot(root string) (SDK, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: Vulkan SDK root is required")
	}
	sdk := SDK{Root: root, IncludeDir: filepath.Join(root, "include")}
	if runtime.GOOS == "windows" {
		sdk.LibraryDirs = []string{filepath.Join(root, "lib")}
	} else {
		sdk.LibraryDirs = []string{filepath.Join(root, "lib")}
	}
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
	root = strings.TrimSpace(root)
	if root == "" {
		root = toolchain.WSLVariable(ctx, distribution, "VULKAN_SDK")
	}
	find := func(name string) string {
		requested := name
		if root != "" {
			requested = path.Join(root, "bin", name)
		}
		resolved, _ := toolchain.WSLExecutable(ctx, distribution, requested)
		return resolved
	}
	sdk := SDK{Root: root, Glslc: find("glslc"), Glslang: find("glslangValidator")}
	if sdk.Glslc == "" && sdk.Glslang == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_NOT_FOUND: glslc or glslangValidator was not found in WSL distribution %s", distribution)
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
	sdk.Runner, sdk.RunnerArgs, _ = toolchain.WSLRunner(distribution)
	exe, args := sdk.wrap(tool, []string{"--version"})
	if output, err := exec.CommandContext(ctx, exe, args...).Output(); err == nil {
		sdk.Version = firstLine(string(output))
	}
	return sdk, nil
}

func WithWSLDirectory(sdk SDK, directory string) SDK {
	if sdk.Runner == "" || strings.TrimSpace(directory) == "" {
		return sdk
	}
	sdk.RunnerArgs = append(append([]string{}, sdk.RunnerArgs...), "--cd", directory)
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
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

type ShaderSpec struct {
	Source     string
	Output     string
	Stage      Stage
	EntryPoint string
	Includes   []string
	Defines    []string
	TargetEnv  string
	Tool       string
}

type ShaderCommand struct {
	Exe  string
	Args []string
}

func toolBase(value string) string {
	return path.Base(filepath.ToSlash(value))
}

func (sdk SDK) Compile(spec ShaderSpec) (ShaderCommand, error) {
	tool := spec.Tool
	if tool == "" {
		tool = sdk.Glslc
	}
	if tool == "" {
		tool = sdk.Glslang
	}
	if tool == "" {
		return ShaderCommand{}, fmt.Errorf("E_VULKAN_SHADER_TOOL_NOT_FOUND")
	}
	if toolBase(tool) == "glslangValidator" {
		args := []string{"-S", string(spec.Stage), "-o", spec.Output}
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
		exe, args := sdk.wrap(tool, append(args, spec.Source))
		return ShaderCommand{Exe: exe, Args: args}, nil
	}
	args := []string{"-fshader-stage=" + string(spec.Stage), "-o", spec.Output}
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
	exe, args := sdk.wrap(tool, append(args, spec.Source))
	return ShaderCommand{Exe: exe, Args: args}, nil
}

func (sdk SDK) Link(name string, inputs []string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("shader link name is required")
	}
	if len(inputs) == 0 {
		return "", fmt.Errorf("shader link requires inputs")
	}
	return strings.Join(append([]string{"link"}, inputs...), " "), nil
}
