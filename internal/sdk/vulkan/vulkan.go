package vulkan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
}

func Detect() (SDK, error) {
	root := os.Getenv("VULKAN_SDK")
	if root == "" {
		return SDK{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: set VULKAN_SDK")
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
	if filepath.Base(tool) == "glslangValidator" {
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
		return ShaderCommand{Exe: tool, Args: append(args, spec.Source)}, nil
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
	return ShaderCommand{Exe: tool, Args: append(args, spec.Source)}, nil
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
