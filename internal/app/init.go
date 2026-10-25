package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"golang.org/x/term"
	"trestle/internal/config"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/source"
	"trestle/internal/toolchain"
	"trestle/internal/vscode"
)

// StarterMember is the first member directory scaffolded when the init wizard
// selects workspace mode.
const StarterMember = "app"

// initChoices holds everything the interactive init wizard collects from the
// user before a single file is written: the enabled toolchains, workspace
// mode, how the target set is populated (import beats scan), and whether the
// VS Code tasks.json is generated.
type initChoices struct {
	CUDA      bool
	Vulkan    bool
	Workspace bool
	Tasks     bool
	Import    string // "", "xmake", or "cmake"
	Scan      bool
}

// Init creates a new project at root by asking the user which toolchains to
// enable (CUDA, Vulkan), whether the project is a workspace root, whether an
// existing CMake or XMake configuration should be imported (or the sources
// scanned), and whether to generate .vscode/tasks.json. It then writes a
// trestle.toml, starter sources (with a CUDA or Vulkan example for the enabled
// toolchains), a .gitignore, and the editor configuration, and compiles the
// generated example once to prove it builds. Files left by the user are never
// overwritten.
func Init(root, name string, input, output *os.File) error {
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	if strings.TrimSpace(name) == "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		name = filepath.Base(absolute)
	}
	path := filepath.Join(root, config.DefaultFileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("E_CONFIG_EXISTS: %s already exists", path)
	} else if !os.IsNotExist(err) {
		return err
	}

	ctx := context.Background()
	echoDetections(ctx, output)

	choices, err := askInitChoices(root, input, output)
	if err != nil {
		return err
	}

	cfg, examples, err := buildInitConfig(root, name, choices, output)
	if err != nil {
		return err
	}
	if err := config.SaveExpected(path, cfg, "missing"); err != nil {
		return err
	}

	if choices.Workspace {
		if err := initWorkspaceMember(root, StarterMember, choices, output); err != nil {
			return err
		}
	}
	writeGitignore(root)

	switch choices.Import {
	case "xmake":
		fmt.Fprintf(output, "\nImporting xmake.lua targets...\n")
		if err := ImportXmake(ctx, path, progressTo(output)); err != nil {
			fmt.Fprintf(output, "%s Import failed: %v\n", status(false), err)
		}
	case "cmake":
		fmt.Fprintf(output, "\nImporting CMake targets...\n")
		if _, err := ImportCMake(ctx, path, progressTo(output)); err != nil {
			fmt.Fprintf(output, "%s Import failed: %v\n", status(false), err)
		}
	}

	if choices.Tasks {
		document := vscode.Generate(initVSCodeProject(root, path, choices.Workspace))
		if _, err := vscode.Write(filepath.Join(root, ".vscode", "tasks.json"), document, vscode.ModeCreate); err != nil {
			return err
		}
		fmt.Fprintf(output, "Generated %s\n", filepath.ToSlash(filepath.Join(".vscode", "tasks.json")))
	}
	fmt.Fprintf(output, "%s Generated %s\n", status(true), config.DefaultFileName)
	if examples {
		verifyExample(ctx, path, choices, output)
	}
	return nil
}

// echoDetections prints the environment scan the wizard's questions build on:
// the C/C++ compiler candidates and the Vulkan SDK.
func echoDetections(ctx context.Context, output *os.File) {
	candidates := toolchain.List(ctx)
	if len(candidates) > 0 {
		fmt.Fprintf(output, "%s %s\n", status(true), toolchainLabel(candidates[0]))
	} else {
		fmt.Fprintf(output, "%s supported compiler not detected\n", status(false))
	}
	_, vulkanErr := vulkan.Detect()
	fmt.Fprintf(output, "%s Vulkan SDK detected\n\n", status(vulkanErr == nil))
}

// askInitChoices runs the wizard. A terminal input drives the huh forms; any
// other input falls back to line prompts with the classic defaults (no extra
// toolchain, standalone project, tasks.json generated).
func askInitChoices(root string, input, output *os.File) (initChoices, error) {
	choices := initChoices{Tasks: true}
	importables := detectImportables(root)
	empty := true
	if entries, err := os.ReadDir(root); err == nil {
		empty = len(entries) == 0
	}
	if empty {
		// Nothing to import or scan in an untouched directory.
		importables = nil
	}

	if term.IsTerminal(int(input.Fd())) {
		return askInitChoicesForm(choices, importables, empty, input, output)
	}

	reader := bufio.NewReader(input)
	choices.CUDA = askDefaultNo(reader, output, "Enable the CUDA toolchain?")
	choices.Vulkan = askDefaultNo(reader, output, "Enable the Vulkan toolchain?")
	choices.Workspace = askDefaultNo(reader, output, "Configure this project as a workspace root?")
	if !choices.Workspace {
		if len(importables) > 0 {
			choices.Import = askImportChoice(reader, output, importables)
			if choices.Import == "" {
				choices.Scan = ask(reader, output, "Scan the directory for C/C++ sources?")
			}
		} else if !empty {
			choices.Scan = ask(reader, output, "Scan the directory for C/C++ sources?")
		}
	}
	choices.Tasks = ask(reader, output, "Generate .vscode/tasks.json?")
	return choices, nil
}

// askInitChoicesForm drives the terminal wizard: one form for toolchain and
// mode, then import/scan and task generation follow-ups that depend on earlier
// answers.
func askInitChoicesForm(choices initChoices, importables []string, empty bool, input, output *os.File) (initChoices, error) {
	var picked []string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Trestle init — enable toolchains").
				Description("Space to toggle, enter to continue.").
				Options(huh.NewOption("CUDA", "cuda"), huh.NewOption("Vulkan", "vulkan")).
				Value(&picked),
			huh.NewConfirm().Title("Configure as a workspace root?").Affirmative("Workspace").Negative("Standalone").
				Value(&choices.Workspace),
		),
	).WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
	if err := form.Run(); err != nil {
		return choices, err
	}
	for _, item := range picked {
		switch item {
		case "cuda":
			choices.CUDA = true
		case "vulkan":
			choices.Vulkan = true
		}
	}

	switch {
	case len(importables) == 0 || choices.Workspace:
		// A workspace root is a pure container: no import, no scan.
		if !choices.Workspace {
			if err := askScanForm(&choices, empty, input, output); err != nil {
				return choices, err
			}
		}
	case len(importables) == 1:
		accept := true
		group := huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf("Found %s — import it? (preferred over scanning)", importables[0])).
			Value(&accept))
		if err := (huh.NewForm(group).WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))).Run(); err != nil {
			return choices, err
		}
		if accept {
			choices.Import = importKind(importables[0])
		} else if err := askScanForm(&choices, empty, input, output); err != nil {
			return choices, err
		}
	default:
		pick := "xmake"
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Found build files — which should be imported? (preferred over scanning)").
				Options(huh.NewOption("xmake.lua", "xmake"), huh.NewOption("CMakeLists.txt", "cmake")).
				Value(&pick),
		)).WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
		if err := form.Run(); err != nil {
			return choices, err
		}
		choices.Import = pick
	}

	tasks := choices.Tasks
	if err := (huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Generate .vscode/tasks.json?").Value(&tasks))).
		WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))).Run(); err != nil {
		return choices, err
	}
	choices.Tasks = tasks
	return choices, nil
}

// askScanForm asks whether existing sources should define the initial targets.
// A directory holding nothing skips the question.
func askScanForm(choices *initChoices, empty bool, input, output *os.File) error {
	if empty {
		return nil
	}
	scan := false
	group := huh.NewGroup(huh.NewConfirm().Title("Scan the directory for C/C++ sources?").
		Affirmative("Scan").Negative("Starter project").Value(&scan))
	if err := (huh.NewForm(group).WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))).Run(); err != nil {
		return err
	}
	choices.Scan = scan
	return nil
}

// askDefaultNo answers a question with no unless the input names yes
// explicitly, so scripted invocations and test streams keep the classic
// init behavior.
func askDefaultNo(reader *bufio.Reader, output *os.File, question string) bool {
	fmt.Fprintf(output, "%s\n> No\n", question)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// askImportChoice answers the single-importable question outside a terminal:
// the default accepts the found build file, "n" falls back to scanning.
func askImportChoice(reader *bufio.Reader, output *os.File, importables []string) string {
	question := fmt.Sprintf("Found %s — import it? (preferred over scanning)", strings.Join(importables, " and "))
	if ask(reader, output, question) {
		return importKind(importables[0])
	}
	return ""
}

// importKind maps a build-file name to the import implementation used for it.
func importKind(fileName string) string {
	if strings.EqualFold(fileName, "xmake.lua") {
		return "xmake"
	}
	return "cmake"
}

// detectImportables lists the importable build files in root, xmake first to
// mirror the `trestle import` precedence.
func detectImportables(root string) []string {
	var found []string
	if _, err := os.Stat(filepath.Join(root, "xmake.lua")); err == nil {
		found = append(found, "xmake.lua")
	}
	if _, err := os.Stat(filepath.Join(root, "CMakeLists.txt")); err == nil {
		found = append(found, "CMakeLists.txt")
	}
	return found
}

// buildInitConfig assembles the project's trestle.toml contents from the
// wizard's answers. The starter target set is replaced by scanned targets when
// scanning was accepted, and workspace mode swaps the starter target for a
// [workspace] section with one member. The second result reports whether the
// starter example (rather than scanned targets) was generated.
func buildInitConfig(root, name string, choices initChoices, output *os.File) (config.Config, bool, error) {
	cfg := config.Default(name)
	applyInitToolchains(&cfg, choices, output)
	switch {
	case choices.Workspace:
		cfg.Workspace = &config.Workspace{Members: []string{StarterMember}}
		cfg.Targets = map[string]config.Target{}
		cfg.Build.DefaultTargets = nil
		return cfg, true, nil
	case choices.Import != "":
		return cfg, false, nil
	}
	if scanTargets(root, name, choices.Scan, &cfg, output) {
		return cfg, false, nil
	}
	if err := applyExamples(&cfg, root, choices, output); err != nil {
		return cfg, false, err
	}
	return cfg, true, nil
}

// applyInitToolchains records the wizard's toolchain choices in a config. CUDA
// keeps the portable "auto" version selector; Vulkan needs a concrete SDK root,
// so a failed detection leaves it off with a warning.
func applyInitToolchains(cfg *config.Config, choices initChoices, output *os.File) {
	if choices.CUDA {
		cfg.Toolchain.CUDA = "auto"
		cfg.Toolchain.CUDAExecution = "native"
	}
	if choices.Vulkan {
		if sdk, err := vulkan.Detect(); err == nil {
			cfg.Toolchain.Vulkan = sdk.Root
			cfg.Toolchain.VulkanExecution = "native"
		} else {
			fmt.Fprintf(output, "%s Vulkan SDK not detected; enable it later with `trestle configure -vulkan <sdk-root>`\n", status(false))
		}
	}
}

// scanTargets replaces the starter target set with targets derived from the
// analyzed sources when scanning is accepted, reporting whether it did.
func scanTargets(root, name string, scan bool, cfg *config.Config, output *os.File) bool {
	if !scan {
		return false
	}
	project, err := source.Analyze(root)
	if err != nil {
		fmt.Fprintf(output, "%s scan failed: %v (keeping the starter target)\n", status(false), err)
		return false
	}
	if len(project.CompilationUnits()) == 0 {
		fmt.Fprintf(output, "%s no C/C++ sources found; keeping the starter target\n", status(false))
		return false
	}
	cfg.Targets = projectTargets(root, name, project)
	if _, ok := cfg.Targets["app"]; !ok {
		cfg.Build.DefaultTargets = nil
	}
	fmt.Fprintf(output, "%s scanned sources into %d target(s)\n", status(true), len(cfg.Targets))
	return true
}

// initWorkspaceMember scaffolds the single starter member of a fresh workspace:
// a directory with its own trestle.toml, starter sources, and a .gitignore.
func initWorkspaceMember(root, member string, choices initChoices, output *os.File) error {
	dir := filepath.Join(root, member)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	memberPath := filepath.Join(dir, config.DefaultFileName)
	if _, err := os.Stat(memberPath); err == nil {
		return fmt.Errorf("E_CONFIG_EXISTS: %s already exists", memberPath)
	}
	memberCfg := config.Default(member)
	applyInitToolchains(&memberCfg, choices, output)
	if err := applyExamples(&memberCfg, dir, choices, output); err != nil {
		return err
	}
	if err := config.SaveExpected(memberPath, memberCfg, "missing"); err != nil {
		return err
	}
	writeGitignore(dir)
	fmt.Fprintf(output, "Created workspace member %s\n", member)
	return nil
}

// Example sources generated by the wizard. The CUDA sample is a static library
// so it compiles with nvcc alone: linking an executable would also need the
// CUDA runtime library, which trestle leaves to the project's library list.
const (
	starterMainSource = `#include <iostream>

int main() {
    std::cout << "Hello from Trestle!" << std::endl;
    return 0;
}
`

	cudaExampleSource = `// CUDA example generated by trestle init.
// It builds as a static library with nvcc; add "cudart" to a target's
// libraries when an executable must launch the kernel.
#include <cuda_runtime.h>

__global__ void addVectors(const float *left, const float *right, float *result, int count) {
    int index = blockIdx.x * blockDim.x + threadIdx.x;
    if (index < count) {
        result[index] = left[index] + right[index];
    }
}

void launchAddVectors(const float *left, const float *right, float *result, int count) {
    int block = 256;
    int grid = (count + block - 1) / block;
    addVectors<<<grid, block>>>(left, right, result, count);
}
`

	vertexShaderSource = `#version 450

vec2 positions[3] = vec2[](
    vec2(0.0, -0.5),
    vec2(0.5, 0.5),
    vec2(-0.5, 0.5)
);

vec3 colors[3] = vec3[](
    vec3(1.0, 0.0, 0.0),
    vec3(0.0, 1.0, 0.0),
    vec3(0.0, 0.0, 1.0)
);

layout(location = 0) out vec3 fragmentColor;

void main() {
    gl_Position = vec4(positions[gl_VertexIndex], 0.0, 1.0);
    fragmentColor = colors[gl_VertexIndex];
}
`

	fragmentShaderSource = `#version 450

layout(location = 0) in vec3 fragmentColor;
layout(location = 0) out vec4 outColor;

void main() {
    outColor = vec4(fragmentColor, 1.0);
}
`
)

// applyExamples writes the starter sources and, when the wizard enabled CUDA or
// Vulkan, adds an example target for each so `trestle build` compiles them.
func applyExamples(cfg *config.Config, dir string, choices initChoices, output *os.File) error {
	if err := writeIfMissing(dir, "src/main.cpp", starterMainSource, output); err != nil {
		return err
	}
	if choices.CUDA {
		if err := writeIfMissing(dir, "src/cuda/vector_add.cu", cudaExampleSource, output); err != nil {
			return err
		}
		cfg.Targets["cuda_example"] = config.Target{Type: "static", OutputName: "cuda_example", Sources: []string{"src/cuda/vector_add.cu"}}
		cfg.Build.DefaultTargets = appendUniqueString(cfg.Build.DefaultTargets, "cuda_example")
	}
	// Shader targets only work with a detected SDK, which applyInitToolchains
	// requires before storing the Vulkan root.
	if choices.Vulkan && cfg.Toolchain.Vulkan != "" {
		if err := writeIfMissing(dir, "shaders/triangle.vert", vertexShaderSource, output); err != nil {
			return err
		}
		if err := writeIfMissing(dir, "shaders/triangle.frag", fragmentShaderSource, output); err != nil {
			return err
		}
		cfg.Targets["triangle_vert"] = config.Target{Type: "shader", OutputName: "triangle.vert", ShaderStage: "vert", Sources: []string{"shaders/triangle.vert"}}
		cfg.Targets["triangle_frag"] = config.Target{Type: "shader", OutputName: "triangle.frag", ShaderStage: "frag", Sources: []string{"shaders/triangle.frag"}}
		cfg.Build.DefaultTargets = appendUniqueString(cfg.Build.DefaultTargets, "triangle_vert", "triangle_frag")
	}
	return nil
}

// writeIfMissing writes the relative file under dir when it does not exist yet,
// reporting the project-relative path it created.
func writeIfMissing(dir, relative, content string, output *os.File) error {
	path := filepath.Join(dir, filepath.FromSlash(relative))
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(output, "Created %s\n", relative)
	return nil
}

// appendUniqueString appends the values not already present, preserving order.
func appendUniqueString(values []string, extra ...string) []string {
	for _, value := range extra {
		found := false
		for _, existing := range values {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			values = append(values, value)
		}
	}
	return values
}

// verifyExample compiles the generated project so init proves the example is
// buildable, reporting a failure as a warning instead of failing init.
func verifyExample(ctx context.Context, path string, choices initChoices, output *os.File) {
	if choices.Import != "" {
		return
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		fmt.Fprintf(output, "\nSkipping example verification: ninja was not found in PATH.\n")
		return
	}
	if len(toolchain.List(ctx)) == 0 {
		fmt.Fprintf(output, "\nSkipping example verification: no supported compiler detected.\n")
		return
	}
	fmt.Fprintf(output, "\nVerifying the generated example compiles...\n")
	var err error
	if choices.Workspace {
		err = BuildWorkspace(ctx, path, WorkspaceBuildOptions{All: true}, progressTo(output))
	} else {
		err = BuildTargetsWithProgress(ctx, path, nil, true, progressTo(output))
	}
	if err != nil {
		fmt.Fprintf(output, "%s Example did not compile: %v\n", status(false), err)
		fmt.Fprintln(output, "Resolve the toolchain issue reported above, then run `trestle build --all`.")
		return
	}
	fmt.Fprintf(output, "%s Example compiled successfully\n", status(true))
}

// writeGitignore writes the build-artifact ignore list once.
func writeGitignore(root string) {
	gitignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		_ = os.WriteFile(gitignore, []byte("build/\ndist/\n.trestle/\n"), 0o644)
	}
}

// initVSCodeProject reduces the configuration for the tasks.json generator. A
// workspace root qualifies every member's targets through FromWorkspace.
func initVSCodeProject(root, path string, workspace bool) vscode.Project {
	cfg, err := config.Load(path)
	if err != nil {
		return vscode.Project{}
	}
	if workspace && cfg.Workspace != nil {
		if wc, err := config.ParseWorkspace(path); err == nil && wc != nil {
			return vscode.FromWorkspace(root, wc)
		}
	}
	return vscode.FromConfig(root, cfg)
}

func progressTo(output *os.File) func(string) {
	return func(line string) {
		fmt.Fprintln(output, line)
	}
}
