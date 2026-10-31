package plan

import (
	"fmt"
	"path/filepath"
	"strings"

	"trestle/internal/config"
	"trestle/internal/model"
	"trestle/internal/modules"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

// buildState carries the bookkeeping shared between the helper stages Build
// calls in turn: the plan under construction and the per-target paths, object
// lists and runtime copies accumulated while walking the resolved project.
type buildState struct {
	cfg      config.Config
	project  model.ResolvedProject
	options  Options
	buildDir string
	result   BuildPlan

	outputs       map[model.TargetID]string
	objects       map[model.TargetID][]string
	cudaObjects   map[model.TargetID][]string
	runtimeCopies map[string]string
}

// checkToolchain requires a C++ compiler for every non-shader project.
// A project containing only shaders needs the Vulkan SDK alone.
func checkToolchain(project model.ResolvedProject, options Options) error {
	for _, target := range project.Targets {
		if target.Type != model.Shader && options.Toolchain.CXX == "" {
			return fmt.Errorf("E_TOOLCHAIN_MISSING: no C++ compiler is configured")
		}
	}

	return nil
}

// newBuildState initialises the plan and resolves every target output before any
// action is emitted. Link dependencies may sort after their consumers (for
// example ggml -> ggml-base), so computing outputs lazily would silently omit
// those link inputs.
func newBuildState(cfg config.Config, project model.ResolvedProject, options Options) (*buildState, error) {
	buildDir := options.BuildDirFor(cfg)
	state := &buildState{
		cfg:      cfg,
		project:  project,
		options:  options,
		buildDir: buildDir,
		result: BuildPlan{
			Variables: map[string]string{
				"trestle_profile": cfg.Build.Profile,
			},
			Defaults:       []string{"all"},
			RuntimeOutputs: map[model.TargetID][]string{},
		},
		outputs:       map[model.TargetID]string{},
		objects:       map[model.TargetID][]string{},
		cudaObjects:   map[model.TargetID][]string{},
		runtimeCopies: map[string]string{},
	}
	for _, target := range project.Targets {
		output, err := targetOutput(target, buildDir, options.Toolchain)
		if err != nil {
			return nil, err
		}
		state.outputs[target.ID] = relative(options.Root, buildDir, output)
	}
	return state, nil
}

// emitTarget emits the actions for one target: a shader step on its own, or a
// compile action per source followed by the archive or link artifact.
func (s *buildState) emitTarget(target model.ResolvedTarget) error {
	if target.Type == model.Shader {
		return s.emitShader(target)
	}
	for _, source := range target.Sources {
		if err := s.emitSource(target, source); err != nil {
			return err
		}
	}
	return s.emitArtifact(target)
}

// emitShader emits the Vulkan shader compilation action for a shader target,
// unless automatic shader compilation is disabled. Shader targets have no
// archive or link step.
func (s *buildState) emitShader(target model.ResolvedTarget) error {
	if !s.cfg.Build.AutoCompileShaders {
		return nil
	}
	if len(target.Sources) != 1 {
		return fmt.Errorf("E_VULKAN_SHADER_SOURCE: target %s must resolve to exactly one source, got %d", target.ID, len(target.Sources))
	}
	if s.options.Vulkan == nil {
		return fmt.Errorf("E_VULKAN_SDK_NOT_FOUND: shader target %s requires a Vulkan SDK", target.ID)
	}
	output, err := targetOutput(target, s.buildDir, s.options.Toolchain)
	if err != nil {
		return err
	}
	source := relative(s.options.Root, s.buildDir, target.Sources[0])
	relativeOutput := relative(s.options.Root, s.buildDir, output)
	depfile := relativeOutput + ".d"
	shader, err := s.options.Vulkan.Compile(vulkan.ShaderSpec{Source: source, Output: relativeOutput, Includes: relativeAll(s.options.Root, s.buildDir, target.CompileSelf.IncludeDirs), Defines: target.CompileSelf.Defines, Depfile: depfile, Stage: vulkan.Stage(target.ShaderStage), EntryPoint: target.ShaderEntry, TargetEnv: target.ShaderTargetEnv, Tool: target.ShaderTool})
	if err != nil {
		return err
	}
	s.outputs[target.ID] = relative(s.options.Root, s.buildDir, output)
	s.result.Actions = append(s.result.Actions, Action{ID: ActionID("shader", string(target.ID)), Rule: ActionID("shader", string(target.ID)), Command: Command{Exe: shader.Exe, Args: shader.Args}, Inputs: []string{source}, Outputs: []string{s.outputs[target.ID]}, Depfile: &DepfileSpec{Path: depfile}})
	return nil
}

// emitSource emits the compile actions for one source: an optional module
// interface action followed by the object compilation, then records the object
// for the target's later link or archive step.
func (s *buildState) emitSource(target model.ResolvedTarget, source string) error {
	object := filepath.Join(s.buildDir, "obj", string(target.ID), sourceID(source)+objectExtension(source, s.options.Toolchain))
	relativeSource := relative(s.options.Root, s.buildDir, source)
	relativeObject := relative(s.options.Root, s.buildDir, object)
	depfile := relativeObject + ".d"
	optionsForCompile := s.compileOptions(target, source)
	cStandard, cxxStandard := s.standards(target)
	optionsForCompile, err := s.emitModule(target, source, relativeSource, optionsForCompile)
	if err != nil {
		return err
	}
	exe, args, err := s.compileObject(target, source, relativeSource, relativeObject, depfile, optionsForCompile, cStandard, cxxStandard)
	if err != nil {
		return err
	}
	action := Action{
		ID:      ActionID("compile", string(target.ID), sourceID(source)),
		Rule:    ActionID("compile", string(target.ID), sourceID(source)),
		Command: Command{Exe: exe, Args: args},
		Inputs:  []string{relativeSource},
		Outputs: []string{relativeObject},
		Pool:    "compile_pool",
	}
	if s.options.Toolchain.Kind == toolchain.MSVC && !strings.EqualFold(filepath.Ext(source), ".cu") {
		action.Deps = "msvc"
	} else {
		action.Depfile = &DepfileSpec{Path: relativeObject + ".d"}
	}
	if moduleInfo, ok := s.options.Modules[source]; ok && moduleInfo.Artifact != "" {
		action.Implicit = append(action.Implicit, relative(s.options.Root, s.buildDir, moduleInfo.Artifact))
	}
	if moduleInfo, ok := s.options.Modules[source]; ok {
		for _, reference := range moduleInfo.Requires {
			action.Implicit = append(action.Implicit, reference.Path)
		}
	}
	if len(s.options.Toolchain.Env) > 0 {
		action.Command.Env = s.options.Toolchain.Env
	}
	s.result.Actions = append(s.result.Actions, action)
	s.objects[target.ID] = append(s.objects[target.ID], relativeObject)
	if strings.EqualFold(filepath.Ext(source), ".cu") {
		s.cudaObjects[target.ID] = append(s.cudaObjects[target.ID], relativeObject)
	}
	return nil
}

// compileOptions assembles the compiler flags for one source: the profile-wide
// flags, the target's language-specific flags, the target's compile options and,
// for GNU shared libraries, -fPIC unless one is already present.
func (s *buildState) compileOptions(target model.ResolvedTarget, source string) []string {
	optionsForCompile := append([]string{}, s.cfg.Build.CompileFlags...)
	if strings.EqualFold(filepath.Ext(source), ".c") {
		optionsForCompile = append(optionsForCompile, s.cfg.Build.CFlags...)
		optionsForCompile = append(optionsForCompile, target.CFlags...)
	} else {
		optionsForCompile = append(optionsForCompile, s.cfg.Build.CXXFlags...)
		optionsForCompile = append(optionsForCompile, target.CXXFlags...)
	}
	optionsForCompile = append(optionsForCompile, target.CompileSelf.Options...)
	if target.Type == model.SharedLibrary && s.options.Toolchain.Kind != toolchain.MSVC && !containsPICOption(optionsForCompile) {
		optionsForCompile = append(optionsForCompile, "-fPIC")
	}
	return optionsForCompile
}

// standards resolves the C and C++ language standards for a target, letting a
// target override the profile-wide defaults.
func (s *buildState) standards(target model.ResolvedTarget) (string, string) {
	cStandard, cxxStandard := s.cfg.Build.CStandard, s.cfg.Build.CXXStandard
	if target.CStandard != "" {
		cStandard = target.CStandard
	}
	if target.CXXStandard != "" {
		cxxStandard = target.CXXStandard
	}
	return cStandard, cxxStandard
}

// moduleBackend returns the module backend for a source, preferring a per-source
// backend over the plan-wide default.
func (s *buildState) moduleBackend(source string) modules.Support {
	backend := s.options.ModuleBackend
	if specific := s.options.ModuleBackends[source]; specific != nil {
		backend = specific
	}
	return backend
}

// emitModule adds the module backend's consumer arguments for a module source
// and, when the source exports an artifact, emits the module interface action.
// It returns the updated compile options.
func (s *buildState) emitModule(target model.ResolvedTarget, source, relativeSource string, optionsForCompile []string) ([]string, error) {
	moduleInfo, ok := s.options.Modules[source]
	if !ok {
		return optionsForCompile, nil
	}
	backend := s.moduleBackend(source)
	if backend == nil {
		return nil, fmt.Errorf("E_MODULE_BACKEND_MISSING: module source %s has no backend", source)
	}
	consumerArgs, err := backend.ConsumerArgs(moduleInfo.Requires)
	if err != nil {
		return nil, err
	}
	optionsForCompile = append(optionsForCompile, consumerArgs...)
	if moduleInfo.Artifact == "" {
		return optionsForCompile, nil
	}
	moduleOutput := relative(s.options.Root, s.buildDir, moduleInfo.Artifact)
	moduleInput := []string{relativeSource}
	moduleImplicit := relativeAll(s.options.Root, s.buildDir, moduleInfo.HeaderDeps)
	for _, reference := range moduleInfo.Requires {
		moduleImplicit = append(moduleImplicit, reference.Path)
	}
	moduleExe, moduleArgs, err := backend.CompileModule(relativeSource, moduleOutput, moduleInfo.Requires)
	if err != nil {
		return nil, err
	}
	s.result.Actions = append(s.result.Actions, Action{ID: ActionID("module", string(target.ID), sourceID(source)), Rule: ActionID("module", string(target.ID), sourceID(source)), Command: Command{Exe: moduleExe, Args: moduleArgs}, Inputs: moduleInput, Implicit: moduleImplicit, Outputs: []string{moduleOutput}, Pool: "compile_pool"})
	return optionsForCompile, nil
}

// compileObject resolves the compiler and arguments for one source's object,
// preferring a module-aware object compiler when the backend supports it and
// otherwise falling back to CUDA or the active C/C++ toolchain.
func (s *buildState) compileObject(target model.ResolvedTarget, source, relativeSource, relativeObject, depfile string, optionsForCompile []string, cStandard, cxxStandard string) (string, []string, error) {
	var exe string
	var args []string
	var err error
	if moduleInfo, ok := s.options.Modules[source]; ok {
		backend := s.moduleBackend(source)
		if objectCompiler, supported := backend.(modules.ObjectCompiler); supported {
			exe, args, err = objectCompiler.CompileModuleObject(relativeSource, relativeObject, moduleInfo.Requires)
		}
	}
	if exe == "" {
		if strings.EqualFold(filepath.Ext(source), ".cu") {
			if s.options.CUDA == nil {
				return "", nil, fmt.Errorf("E_CUDA_NOT_CONFIGURED: source %s requires a CUDA toolchain", source)
			}
			exe, args, err = s.options.CUDA.Compile(toolchain.CompileSpec{Source: relativeSource, Output: relativeObject, Includes: relativeAll(s.options.Root, s.buildDir, target.CompileSelf.IncludeDirs), Defines: target.CompileSelf.Defines, Options: optionsForCompile, CXXStandard: cxxStandard, Depfile: depfile})
		} else {
			compiler := s.options.Toolchain.CXX
			if strings.EqualFold(filepath.Ext(source), ".c") {
				compiler = s.options.Toolchain.CC
			}
			oldCompiler := s.options.Toolchain.CXX
			s.options.Toolchain.CXX = compiler
			exe, args, err = s.options.Toolchain.Compile(toolchain.CompileSpec{Source: relativeSource, Output: relativeObject, Includes: relativeAll(s.options.Root, s.buildDir, target.CompileSelf.IncludeDirs), Defines: target.CompileSelf.Defines, Options: optionsForCompile, CStandard: cStandard, CXXStandard: cxxStandard, Depfile: depfile})
			s.options.Toolchain.CXX = oldCompiler
		}
	}
	if err != nil {
		return "", nil, err
	}
	return exe, args, nil
}

// emitArtifact emits a target's link or archive action along with its CUDA
// device link, runtime copies and MSVC import-library phony.
func (s *buildState) emitArtifact(target model.ResolvedTarget) error {
	output, err := targetOutput(target, s.buildDir, s.options.Toolchain)
	if err != nil {
		return err
	}
	s.outputs[target.ID] = relative(s.options.Root, s.buildDir, output)
	linkInputs := append([]string{}, s.objects[target.ID]...)
	linkInputs, err = s.emitDeviceLink(target, linkInputs)
	if err != nil {
		return err
	}
	linkInputs = append(linkInputs, relativeTargets(s.options.Root, s.buildDir, s.project, s.project.LinkClosure[target.ID], s.outputs, s.options.Toolchain)...)
	action, err := s.linkOrArchive(target, linkInputs)
	if err != nil {
		return err
	}
	if len(s.options.Toolchain.Env) > 0 {
		action.Command.Env = s.options.Toolchain.Env
	}
	s.result.Actions = append(s.result.Actions, action)
	if err := s.emitRuntimeCopies(target, s.outputs[target.ID]); err != nil {
		return err
	}
	s.emitImportLibrary(target, s.outputs[target.ID])
	return nil
}

// emitDeviceLink emits a CUDA device-link action for a target's device objects
// and returns the link inputs with the device object appended, when the CUDA
// toolchain uses separate compilation.
func (s *buildState) emitDeviceLink(target model.ResolvedTarget, linkInputs []string) ([]string, error) {
	cudaInputs := s.cudaLinkInputs(target)
	if len(cudaInputs) > 0 && target.Type != model.StaticLibrary && s.options.CUDA != nil && s.options.CUDA.Mode == cuda.SeparateCompilation {
		deviceObject := filepath.Join(s.buildDir, "obj", string(target.ID), "device-link.o")
		deviceRelative := relative(s.options.Root, s.buildDir, deviceObject)
		exe, args, err := s.options.CUDA.DeviceLink(cudaInputs, deviceRelative)
		if err != nil {
			return nil, err
		}
		s.result.Actions = append(s.result.Actions, Action{ID: ActionID("device-link", string(target.ID)), Rule: ActionID("device-link", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: cudaInputs, Outputs: []string{deviceRelative}})
		linkInputs = append(linkInputs, deviceRelative)
	}
	return linkInputs, nil
}

// linkOrArchive builds the final artifact action for a target, emitting the
// MSVC export-definition action first when the target exports all symbols.
func (s *buildState) linkOrArchive(target model.ResolvedTarget, linkInputs []string) (Action, error) {
	var action Action
	switch target.Type {
	case model.StaticLibrary:
		archiveInputs := append([]string{}, s.objects[target.ID]...)
		exe, args, err := s.options.Toolchain.Archive(s.outputs[target.ID], archiveInputs)
		if err != nil {
			return Action{}, err
		}
		action = Action{ID: ActionID("archive", string(target.ID)), Rule: ActionID("archive", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: archiveInputs, Outputs: []string{s.outputs[target.ID]}}
	case model.SharedLibrary, model.Executable, model.Test:
		linkOptions := append([]string{}, s.cfg.Build.LinkFlags...)
		linkOptions = append(linkOptions, target.LinkSelf.Options...)
		libraries := make([]string, 0, len(target.LinkSelf.Items))
		for _, item := range target.LinkSelf.Items {
			if item.Path != "" {
				linkInputs = append(linkInputs, relative(s.options.Root, s.buildDir, item.Path))
			} else if item.Kind == "workspace" {
				// A cross-member dependency: prefer the artifact path computed
				// during generation, falling back to name-based linking.
				if output, ok := s.options.WorkspaceOutputs[item.Name]; ok {
					linkInputs = append(linkInputs, relative(s.options.Root, s.buildDir, output))
				} else {
					libraries = append(libraries, item.Name)
				}
			} else {
				libraries = append(libraries, item.Name)
			}
		}
		importLibrary := ""
		if target.Type == model.SharedLibrary && s.options.Toolchain.Kind == toolchain.MSVC {
			importLibrary = strings.TrimSuffix(s.outputs[target.ID], filepath.Ext(s.outputs[target.ID])) + ".lib"
		}
		definitionFile := ""
		if target.Type == model.SharedLibrary && s.options.Toolchain.Kind == toolchain.MSVC && target.ExportAllSymbols {
			definitionFile = strings.TrimSuffix(s.outputs[target.ID], filepath.Ext(s.outputs[target.ID])) + ".exports.def"
			s.result.Actions = append(s.result.Actions, Action{
				ID: ActionID("exports", string(target.ID)), Rule: ActionID("exports", string(target.ID)),
				Command: Command{Exe: "cmake", Args: []string{"-E", "__create_def", definitionFile, definitionFile + ".objs"}},
				Inputs:  s.objects[target.ID], Outputs: []string{definitionFile}, ResponseFile: true,
			})
			linkOptions = append(linkOptions, "/DEF:"+definitionFile)
		}
		libraryDirs := relativeAll(s.options.Root, s.buildDir, target.LinkSelf.LibraryDirs)
		if s.options.CUDA != nil && len(s.cudaLinkInputs(target)) > 0 {
			dir, runtimeLibs := s.options.CUDA.RuntimeLibraries()
			// Keep SDK paths absolute: deep Windows test/build roots can exceed
			// the linker path limit before a long ../ chain is normalized.
			libraryDirs = append(libraryDirs, filepath.ToSlash(dir))
			libraries = append(libraries, runtimeLibs...)
		}
		exe, args, err := s.options.Toolchain.Link(toolchain.LinkSpec{
			Inputs: linkInputs, Output: s.outputs[target.ID], Shared: target.Type == model.SharedLibrary,
			ImportLibrary: importLibrary, LibraryDirs: libraryDirs, Libraries: libraries, Options: linkOptions,
		})
		if err != nil {
			return Action{}, err
		}
		action = Action{ID: ActionID("link", string(target.ID)), Rule: ActionID("link", string(target.ID)), Command: Command{Exe: exe, Args: args}, Inputs: linkInputs, Outputs: []string{s.outputs[target.ID]}}
		if definitionFile != "" {
			action.Implicit = append(action.Implicit, definitionFile)
		}
	default:
		return Action{}, fmt.Errorf("unsupported target type %q", target.Type)
	}
	return action, nil
}

// emitRuntimeCopies emits a copy action for each runtime file the target needs
// beside its artifact, rejecting two sources that claim the same destination.
func (s *buildState) emitRuntimeCopies(target model.ResolvedTarget, output string) error {
	for _, runtimeFile := range target.LinkSelf.RuntimeFiles {
		if strings.TrimSpace(runtimeFile) == "" {
			continue
		}
		destination := filepath.ToSlash(filepath.Join(filepath.Dir(output), filepath.Base(runtimeFile)))
		s.result.RuntimeOutputs[target.ID] = appendUnique(s.result.RuntimeOutputs[target.ID], destination)
		source := relative(s.options.Root, s.buildDir, runtimeFile)
		if existing, ok := s.runtimeCopies[destination]; ok {
			if !strings.EqualFold(existing, source) {
				return fmt.Errorf("runtime file collision for %s: %s and %s", destination, existing, source)
			}
			continue
		}
		s.runtimeCopies[destination] = source
		s.result.Actions = append(s.result.Actions, Action{
			ID: ActionID("runtime", string(target.ID), filepath.Base(runtimeFile)), Rule: "runtime-copy",
			Command: Command{Exe: "cmake", Args: []string{"-E", "copy_if_different", source, destination}},
			Inputs:  []string{source}, Outputs: []string{destination},
		})
	}
	return nil
}

// emitImportLibrary emits the phony action that names an MSVC shared library's
// import library as a linkable target.
func (s *buildState) emitImportLibrary(target model.ResolvedTarget, output string) {
	if target.Type == model.SharedLibrary && s.options.Toolchain.Kind == toolchain.MSVC {
		importLibrary := strings.TrimSuffix(output, filepath.Ext(output)) + ".lib"
		s.result.Actions = append(s.result.Actions, Action{ID: ActionID("import-lib", string(target.ID)), Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: []string{output}, Outputs: []string{importLibrary}})
	}
}

// emitAllTarget appends the "all" phony target covering every target output and
// runtime copy.
func (s *buildState) emitAllTarget() {
	allOutputs := orderedOutputs(s.project, s.outputs)
	for _, target := range s.project.Targets {
		allOutputs = append(allOutputs, s.result.RuntimeOutputs[target.ID]...)
	}
	s.result.Actions = append(s.result.Actions, Action{ID: "all", Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: appendUnique(nil, allOutputs...), Outputs: []string{"all"}})
}

// emitTestTargets appends one stamp action per test target followed by the
// "test" phony target that depends on each stamp.
func (s *buildState) emitTestTargets() {
	for _, target := range s.project.Targets {
		if target.Type != model.Test {
			continue
		}
		testOutput := filepath.ToSlash(filepath.Join(pathRelativeTo(s.options.Root, s.buildDir), "test", string(target.ID)+".stamp"))
		command := testCommand(s.outputs[target.ID], testOutput)
		s.result.Actions = append(s.result.Actions, Action{
			ID: ActionID("test", string(target.ID)), Rule: ActionID("test", string(target.ID)), Command: command,
			Inputs:  []string{s.outputs[target.ID]},
			Outputs: []string{testOutput}, Pool: "console_pool",
		})
	}
	testInputs := make([]string, 0)
	for _, action := range s.result.Actions {
		if strings.HasPrefix(string(action.ID), "test:") {
			testInputs = append(testInputs, action.Outputs...)
		}
	}
	s.result.Actions = append(s.result.Actions, Action{ID: "test", Rule: "phony", Command: Command{Exe: "ninja"}, Inputs: testInputs, Outputs: []string{"test"}, Pool: "console_pool"})
}

func (s *buildState) cudaLinkInputs(target model.ResolvedTarget) []string {
	result := append([]string{}, s.cudaObjects[target.ID]...)
	for _, id := range s.project.LinkClosure[target.ID] {
		provider := s.project.ByID[id]
		if provider.Type != model.StaticLibrary {
			continue
		}
		for _, source := range provider.Sources {
			if strings.EqualFold(filepath.Ext(source), ".cu") {
				output := s.outputs[id]
				if output == "" {
					if workspaceOutput, ok := s.options.WorkspaceOutputs[string(id)]; ok {
						output = relative(s.options.Root, s.buildDir, workspaceOutput)
					}
				}
				if output != "" {
					result = append(result, output)
				}
				break
			}
		}
	}
	return result
}
