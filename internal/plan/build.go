package plan

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/model"
	"trestle/internal/modules"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

// ModuleInfo describes one C++ module source: the artifact it exports, the
// modules it provides and requires, and the headers it depends on.
type ModuleInfo struct {
	Artifact   string
	Provides   []string
	Requires   []modules.Reference
	HeaderDeps []string
}

// Options carries everything Build needs beyond the configuration and resolved
// project: the output roots, the toolchain, optional CUDA and Vulkan toolchains,
// and the per-source module backends.
type Options struct {
	Root           string
	BuildDir       string
	Toolchain      toolchain.Toolchain
	CUDA           *cuda.Toolchain
	Vulkan         *vulkan.SDK
	Modules        map[string]ModuleInfo
	ModuleBackend  modules.Support
	ModuleBackends map[string]modules.Support
	// WorkspaceOutputs maps a workspace target ID to the artifact another
	// member links against. Entries resolve at generation time, when the
	// toolchain that decides the artifact extension is known.
	WorkspaceOutputs map[string]string
	// ManifestDir overrides the output directory when it is not
	// BuildDir/<profile>, as with workspace members whose artifacts live in
	// build/<profile>/<member>.
	ManifestDir string
}

// BuildDirFor returns the directory build outputs and object files live in.
func (o Options) BuildDirFor(cfg config.Config) string {
	if o.ManifestDir != "" {
		return o.ManifestDir
	}
	return filepath.Join(o.BuildDir, cfg.Build.Profile)
}

// Build assembles the Ninja action graph for a resolved project: a compile
// action per source, module and shader steps where required, device linking for
// CUDA, then archive or link, runtime copies, and the test and "all" phony
// targets. It returns a validated plan or the first error encountered.
func Build(cfg config.Config, project model.ResolvedProject, options Options) (BuildPlan, error) {
	if err := checkToolchain(project, options); err != nil {
		return BuildPlan{}, err
	}
	state, err := newBuildState(cfg, project, options)
	if err != nil {
		return BuildPlan{}, err
	}
	for _, target := range project.Targets {
		if err := state.emitTarget(target); err != nil {
			return BuildPlan{}, err
		}
	}
	state.emitAllTarget()
	state.emitTestTargets()
	if err := state.result.Validate(); err != nil {
		return BuildPlan{}, fmt.Errorf("E_PLAN_INVALID: %w", err)
	}
	return state.result, nil
}

// WorkspaceOutput returns the path of a workspace member's artifact as a
// dependent member links it: the full target output including the platform
// extension, using the MSVC import library for shared members.
func WorkspaceOutput(target model.ResolvedTarget, buildDir string, tc toolchain.Toolchain) (string, error) {
	output, err := targetOutput(target, buildDir, tc)
	if err != nil {
		return "", err
	}
	if target.Type == model.SharedLibrary && tc.Kind == toolchain.MSVC {
		return strings.TrimSuffix(output, filepath.Ext(output)) + ".lib", nil
	}
	return output, nil
}

func containsPICOption(options []string) bool {
	for _, option := range options {
		if strings.EqualFold(option, "-fpic") || strings.EqualFold(option, "-fPIC") {
			return true
		}
	}
	return false
}

func testCommand(executable, stamp string) Command {
	if runtime.GOOS == "windows" {
		return Command{Exe: "cmd", Args: []string{"/C", fmt.Sprintf("if not exist \"test\" mkdir \"test\" && \"%s\" >NUL 2>&1 && echo.>\"%s\"", executable, stamp)}}
	}
	return Command{Exe: "sh", Args: []string{"-c", fmt.Sprintf("mkdir -p \"$(dirname %q)\" && %q >/dev/null 2>&1 && : > %q", stamp, executable, stamp)}}
}

func targetOutput(target model.ResolvedTarget, buildDir string, tc toolchain.Toolchain) (string, error) {
	name := target.OutputName
	if name == "" {
		name = string(target.ID)
	}
	extension := ""
	targetOS, _ := toolchain.TargetPlatform(tc)
	switch target.Type {
	case model.Executable, model.Test:
		if targetOS == "windows" {
			extension = ".exe"
		}
	case model.Shader:
		extension = ".spv"
	case model.StaticLibrary:
		if tc.Kind == toolchain.MSVC {
			extension = ".lib"
		} else {
			extension = ".a"
		}
	case model.SharedLibrary:
		if targetOS == "windows" {
			extension = ".dll"
		} else {
			extension = ".so"
			if targetOS == "darwin" {
				extension = ".dylib"
			}
		}
	}
	return filepath.Join(buildDir, "bin", name+extension), nil
}

func sourceID(source string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	return sanitize(base + "-" + fsx.Hash(filepath.Clean(source))[:10])
}

func sanitize(value string) string {
	var result strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			result.WriteRune(char)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
}

func objectExtension(source string, tc toolchain.Toolchain) string {
	if tc.Kind == toolchain.MSVC {
		return ".obj"
	}
	return ".o"
}

func relative(root, buildDir, path string) string {
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") {
		return pathpkg.Clean(path)
	}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(buildDir, path); err == nil {
			return filepath.ToSlash(rel)
		}
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(filepath.Join(pathRelativeTo(root, buildDir), filepath.Clean(path)))
}

func pathRelativeTo(root, path string) string {
	rel, err := filepath.Rel(path, root)
	if err != nil {
		return filepath.Clean(root)
	}
	return filepath.ToSlash(rel)
}

func relativeAll(root, buildDir string, paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		result = append(result, relative(root, buildDir, path))
	}
	return result
}

func relativeTargets(root, buildDir string, project model.ResolvedProject, targets []model.TargetID, outputs map[model.TargetID]string, tc toolchain.Toolchain) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		if output := outputs[target]; output != "" {
			if dependency, ok := project.ByID[target]; ok && dependency.Type == model.SharedLibrary && tc.Kind == toolchain.MSVC {
				output = strings.TrimSuffix(output, filepath.Ext(output)) + ".lib"
			}
			result = append(result, output)
		}
	}
	return result
}

func orderedOutputs(project model.ResolvedProject, outputs map[model.TargetID]string) []string {
	result := make([]string, 0, len(project.Targets))
	for _, target := range project.Targets {
		if output := outputs[target.ID]; output != "" {
			result = append(result, output)
		}
	}
	return result
}

func appendUnique(values []string, candidates ...string) []string {
	seen := make(map[string]bool, len(values)+len(candidates))
	for _, value := range values {
		seen[strings.ToLower(filepath.Clean(value))] = true
	}
	for _, candidate := range candidates {
		key := strings.ToLower(filepath.Clean(candidate))
		if seen[key] {
			continue
		}
		seen[key] = true
		values = append(values, candidate)
	}
	return values
}

// ActionID joins its parts into a sanitized ID, turning a target or source name
// into a Ninja-safe rule or action name.
func ActionID(parts ...string) ID { return ID(sanitize(strings.Join(parts, ":"))) }
