package plan

import (
	"fmt"
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

type ModuleInfo struct {
	Artifact string
	Provides []string
	Requires []modules.Reference
}

type Options struct {
	Root          string
	BuildDir      string
	Toolchain     toolchain.Toolchain
	CUDA          *cuda.Toolchain
	Vulkan        *vulkan.SDK
	Modules       map[string]ModuleInfo
	ModuleBackend modules.Support
}

func Build(cfg config.Config, project model.ResolvedProject, options Options) (BuildPlan, error) {
	hasShader := false
	for _, target := range project.Targets {
		if target.Type == model.Shader {
			hasShader = true
		}
	}
	if options.Toolchain.CXX == "" && !hasShader {
		return BuildPlan{}, fmt.Errorf("E_TOOLCHAIN_MISSING: no C++ compiler is configured")
	}
	buildDir := filepath.Join(options.BuildDir, cfg.Build.Profile)
	result := BuildPlan{
		Variables: map[string]string{
			"trestle_profile": cfg.Build.Profile,
		},
		Defaults:       []string{"all"},
		RuntimeOutputs: map[model.TargetID][]string{},
	}
	objects := map[model.TargetID][]string{}
	cudaObjects := map[model.TargetID][]string{}
	outputs := map[model.TargetID]string{}
	runtimeCopies := map[string]string{}
	// Resolve every target output before emitting actions. Link dependencies may
	// sort after their consumers (for example ggml -> ggml-base), so computing
	// outputs lazily would silently omit those link inputs.
	for _, target := range project.Targets {
		output, err := targetOutput(target, buildDir, options.Toolchain)
		if err != nil {
			return BuildPlan{}, err
		}
		outputs[target.ID] = relative(options.Root, buildDir, output)
	}
	for _, target := range project.Targets {
		if target.Type == model.Shader {
			if !cfg.Build.AutoCompileShaders {
				continue
			}
			if options.Vulkan == nil {
				return BuildPlan{}, fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: shader target %s requires a Vulkan SDK", target.ID)
			}
			output, err := targetOutput(target, buildDir, options.Toolchain)
			if err != nil {
				return BuildPlan{}, err
			}
			shader, err := options.Vulkan.Compile(vulkan.ShaderSpec{Source: relative(options.Root, buildDir, target.Sources[0]), Output: relative(options.Root, buildDir, output), Stage: vulkan.Stage(target.ShaderStage), EntryPoint: target.ShaderEntry, TargetEnv: target.ShaderTargetEnv, Tool: target.ShaderTool})
			if err != nil {
				return BuildPlan{}, err
			}
			outputs[target.ID] = relative(options.Root, buildDir, output)
			result.Actions = append(result.Actions, Action{ID: ActionID("shader", string(target.ID)), Rule: ActionID("shader", string(target.ID)), Command: Command{Exe: shader.Exe, Args: shader.Args}, Inputs: []string{relative(options.Root, buildDir, target.Sources[0])}, Outputs: []string{outputs[target.ID]}})
			continue
		}
		for _, source := range target.Sources {
			object := filepath.Join(buildDir, "obj", string(target.ID), sourceID(source)+objectExtension(source, options.Toolchain))
			relativeSource := relative(options.Root, buildDir, source)
			relativeObject := relative(options.Root, buildDir, object)
			depfile := relativeObject + ".d"
			optionsForCompile := append([]string{}, cfg.Build.CompileFlags...)
			if strings.EqualFold(filepath.Ext(source), ".c") {
				optionsForCompile = append(optionsForCompile, cfg.Build.CFlags...)
			} else {
				optionsForCompile = append(optionsForCompile, cfg.Build.CXXFlags...)
			}
			optionsForCompile = append(optionsForCompile, target.CompileSelf.Options...)
			if target.Type == model.SharedLibrary && options.Toolchain.Kind != toolchain.MSVC && !strings.EqualFold(filepath.Ext(source), ".cu") && !containsPICOption(optionsForCompile) {
				optionsForCompile = append(optionsForCompile, "-fPIC")
			}
			cStandard, cxxStandard := cfg.Build.CStandard, cfg.Build.CXXStandard
			if target.CStandard != "" {
				cStandard = target.CStandard
			}
			if target.CXXStandard != "" {
				cxxStandard = target.CXXStandard
			}
			if moduleInfo, ok := options.Modules[source]; ok {
				if options.ModuleBackend == nil {
					return BuildPlan{}, fmt.Errorf("E_MODULE_BACKEND_MISSING: module source %s has no backend", source)
				}
				consumerArgs, err := options.ModuleBackend.ConsumerArgs(moduleInfo.Requires)
				if err != nil {
					return BuildPlan{}, err
				}
				optionsForCompile = append(optionsForCompile, consumerArgs...)
				moduleOutput := relative(options.Root, buildDir, moduleInfo.Artifact)
				moduleInput := []string{relativeSource}
				for _, reference := range moduleInfo.Requires {
					moduleInput = append(moduleInput, reference.Path)
				}
				moduleExe, moduleArgs, err := options.ModuleBackend.CompileModule(relativeSource, moduleOutput, moduleInfo.Requires)
				if err != nil {
					return BuildPlan{}, err
				}
				result.Actions = append(result.Actions, Action{ID: ActionID("module", string(target.ID), sourceID(source)), Rule: ActionID("module", string(target.ID), sourceID(source)), Command: Command{Exe: moduleExe, Args: moduleArgs}, Inputs: moduleInput, Outputs: []string{moduleOutput}, Pool: "compile_pool"})
			}
			var exe string
			var args []string
			var err error
			if moduleInfo, ok := options.Modules[source]; ok {
				if objectCompiler, supported := options.ModuleBackend.(modules.ObjectCompiler); supported {
					exe, args, err = objectCompiler.CompileModuleObject(relativeSource, relativeObject, moduleInfo.Requires)
				}
			}
			if exe == "" {
				if strings.EqualFold(filepath.Ext(source), ".cu") {
					if options.CUDA == nil {
						return BuildPlan{}, fmt.Errorf("E_CUDA_NOT_CONFIGURED: source %s requires a CUDA toolchain", source)
					}
					exe, args, err = options.CUDA.Compile(toolchain.CompileSpec{Source: relativeSource, Output: relativeObject, Includes: relativeAll(options.Root, buildDir, target.CompileSelf.IncludeDirs), Defines: target.CompileSelf.Defines, Options: optionsForCompile, CXXStandard: cxxStandard})
				} else {
					compiler := options.Toolchain.CXX
					if strings.EqualFold(filepath.Ext(source), ".c") {
						compiler = options.Toolchain.CC
					}
					oldCompiler := options.Toolchain.CXX
					options.Toolchain.CXX = compiler
					exe, args, err = options.Toolchain.Compile(toolchain.CompileSpec{Source: relativeSource, Output: relativeObject, Includes: relativeAll(options.Root, buildDir, target.CompileSelf.IncludeDirs), Defines: target.CompileSelf.Defines, Options: optionsForCompile, CStandard: cStandard, CXXStandard: cxxStandard, Depfile: depfile})
					options.Toolchain.CXX = oldCompiler
				}
			}
			if err != nil {
				return BuildPlan{}, err
			}
			action := Action{
				ID:      ActionID("compile", string(target.ID), sourceID(source)),
				Rule:    ActionID("compile", sourceID(source)),
				Command: Command{Exe: exe, Args: args},
				Inputs:  []string{relativeSource},
				Outputs: []string{relativeObject},
				Pool:    "compile_pool",
			}
			if options.Toolchain.Kind == toolchain.MSVC {
				action.Deps = "msvc"
			} else {
				action.Depfile = &DepfileSpec{Path: relativeObject + ".d"}
			}
			if moduleInfo, ok := options.Modules[source]; ok {
				action.Implicit = append(action.Implicit, relative(options.Root, buildDir, moduleInfo.Artifact))
			}
			if len(options.Toolchain.Env) > 0 {
				action.Command.Env = options.Toolchain.Env
			}
			result.Actions = append(result.Actions, action)
			objects[target.ID] = append(objects[target.ID], relativeObject)
			if strings.EqualFold(filepath.Ext(source), ".cu") {
				cudaObjects[target.ID] = append(cudaObjects[target.ID], relativeObject)
			}
		}
		output, err := targetOutput(target, buildDir, options.Toolchain)
		if err != nil {
			return BuildPlan{}, err
		}
		outputs[target.ID] = relative(options.Root, buildDir, output)
		linkInputs := append([]string{}, objects[target.ID]...)
		if len(cudaObjects[target.ID]) > 0 && target.Type != model.StaticLibrary && options.CUDA != nil && options.CUDA.Mode == cuda.SeparateCompilation {
			deviceObject := filepath.Join(buildDir, "obj", string(target.ID), "device-link.o")
			deviceRelative := relative(options.Root, buildDir, deviceObject)
			exe, args, err := options.CUDA.DeviceLink(cudaObjects[target.ID], deviceRelative)
			if err != nil {
				return BuildPlan{}, err
			}
			result.Actions = append(result.Actions, Action{ID: ActionID("device-link", string(target.ID)), Rule: ActionID("device-link", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: cudaObjects[target.ID], Outputs: []string{deviceRelative}})
			linkInputs = append(linkInputs, deviceRelative)
		}
		linkInputs = append(linkInputs, relativeTargets(options.Root, buildDir, project, project.LinkClosure[target.ID], outputs, options.Toolchain)...)
		var action Action
		switch target.Type {
		case model.StaticLibrary:
			archiveInputs := append([]string{}, objects[target.ID]...)
			exe, args, err := options.Toolchain.Archive(outputs[target.ID], archiveInputs)
			if err != nil {
				return BuildPlan{}, err
			}
			action = Action{ID: ActionID("archive", string(target.ID)), Rule: ActionID("archive", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: archiveInputs, Outputs: []string{outputs[target.ID]}}
		case model.SharedLibrary, model.Executable, model.Test:
			linkOptions := append([]string{}, cfg.Build.LinkFlags...)
			linkOptions = append(linkOptions, target.LinkSelf.Options...)
			libraries := make([]string, 0, len(target.LinkSelf.Items))
			for _, item := range target.LinkSelf.Items {
				if item.Path != "" {
					linkInputs = append(linkInputs, relative(options.Root, buildDir, item.Path))
				} else {
					libraries = append(libraries, item.Name)
				}
			}
			importLibrary := ""
			if target.Type == model.SharedLibrary && options.Toolchain.Kind == toolchain.MSVC {
				importLibrary = strings.TrimSuffix(outputs[target.ID], filepath.Ext(outputs[target.ID])) + ".lib"
			}
			definitionFile := ""
			if target.Type == model.SharedLibrary && options.Toolchain.Kind == toolchain.MSVC && target.ExportAllSymbols {
				definitionFile = strings.TrimSuffix(outputs[target.ID], filepath.Ext(outputs[target.ID])) + ".exports.def"
				result.Actions = append(result.Actions, Action{
					ID: ActionID("exports", string(target.ID)), Rule: ActionID("exports", string(target.ID)),
					Command: Command{Exe: "cmake", Args: []string{"-E", "__create_def", definitionFile, definitionFile + ".objs"}},
					Inputs:  objects[target.ID], Outputs: []string{definitionFile}, ResponseFile: true,
				})
				linkOptions = append(linkOptions, "/DEF:"+definitionFile)
			}
			exe, args, err := options.Toolchain.Link(toolchain.LinkSpec{
				Inputs: linkInputs, Output: outputs[target.ID], Shared: target.Type == model.SharedLibrary,
				ImportLibrary: importLibrary, LibraryDirs: relativeAll(options.Root, buildDir, target.LinkSelf.LibraryDirs), Libraries: libraries, Options: linkOptions,
			})
			if err != nil {
				return BuildPlan{}, err
			}
			action = Action{ID: ActionID("link", string(target.ID)), Rule: ActionID("link", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: linkInputs, Outputs: []string{outputs[target.ID]}}
			if definitionFile != "" {
				action.Implicit = append(action.Implicit, definitionFile)
			}
		default:
			return BuildPlan{}, fmt.Errorf("unsupported target type %q", target.Type)
		}
		if len(options.Toolchain.Env) > 0 {
			action.Command.Env = options.Toolchain.Env
		}
		result.Actions = append(result.Actions, action)
		for _, runtimeFile := range target.LinkSelf.RuntimeFiles {
			if strings.TrimSpace(runtimeFile) == "" {
				continue
			}
			destination := filepath.ToSlash(filepath.Join(filepath.Dir(outputs[target.ID]), filepath.Base(runtimeFile)))
			result.RuntimeOutputs[target.ID] = appendUnique(result.RuntimeOutputs[target.ID], destination)
			source := relative(options.Root, buildDir, runtimeFile)
			if existing, ok := runtimeCopies[destination]; ok {
				if !strings.EqualFold(existing, source) {
					return BuildPlan{}, fmt.Errorf("runtime file collision for %s: %s and %s", destination, existing, source)
				}
				continue
			}
			runtimeCopies[destination] = source
			result.Actions = append(result.Actions, Action{
				ID: ActionID("runtime", string(target.ID), filepath.Base(runtimeFile)), Rule: ActionID("runtime-copy"),
				Command: Command{Exe: "cmake", Args: []string{"-E", "copy_if_different", source, destination}},
				Inputs:  []string{source}, Outputs: []string{destination},
			})
		}
		if target.Type == model.SharedLibrary && options.Toolchain.Kind == toolchain.MSVC {
			importLibrary := strings.TrimSuffix(outputs[target.ID], filepath.Ext(outputs[target.ID])) + ".lib"
			result.Actions = append(result.Actions, Action{ID: ActionID("import-lib", string(target.ID)), Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: []string{outputs[target.ID]}, Outputs: []string{importLibrary}})
		}
	}
	allOutputs := orderedOutputs(project, outputs)
	for _, target := range project.Targets {
		allOutputs = append(allOutputs, result.RuntimeOutputs[target.ID]...)
	}
	result.Actions = append(result.Actions, Action{ID: "all", Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: appendUnique(nil, allOutputs...), Outputs: []string{"all"}})
	for _, target := range project.Targets {
		if target.Type != model.Test {
			continue
		}
		testOutput := filepath.ToSlash(filepath.Join(pathRelativeTo(options.Root, buildDir), "test", string(target.ID)+".stamp"))
		command := testCommand(outputs[target.ID], testOutput)
		result.Actions = append(result.Actions, Action{
			ID: ActionID("test", string(target.ID)), Rule: ActionID("test", string(target.ID)), Command: command,
			Inputs:  []string{outputs[target.ID]},
			Outputs: []string{testOutput}, Pool: "console_pool",
		})
	}
	testInputs := make([]string, 0)
	for _, action := range result.Actions {
		if strings.HasPrefix(string(action.ID), "test:") {
			testInputs = append(testInputs, action.Outputs...)
		}
	}
	result.Actions = append(result.Actions, Action{ID: "test", Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: testInputs, Outputs: []string{"test"}, Pool: "console_pool"})
	if err := result.Validate(); err != nil {
		return BuildPlan{}, fmt.Errorf("E_PLAN_INVALID: %w", err)
	}
	return result, nil
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
	switch target.Type {
	case model.Executable, model.Test:
		if tc.Kind == toolchain.MSVC || runtime.GOOS == "windows" {
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
		if tc.Kind == toolchain.MSVC {
			extension = ".dll"
		} else {
			extension = ".so"
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

func ActionID(parts ...string) ID { return ID(sanitize(strings.Join(parts, ":"))) }
