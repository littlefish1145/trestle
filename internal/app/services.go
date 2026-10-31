// Package app implements the trestle operations behind the command line and the
// interactive console: project setup, toolchain and package configuration,
// manifest generation, building, testing, and packaging.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trestle/internal/config"
	"trestle/internal/deps"
	"trestle/internal/deps/conan"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/fsx"
	"trestle/internal/graph"
	cmakeimport "trestle/internal/importer/cmake"
	xmakeimport "trestle/internal/importer/xmake"
	"trestle/internal/model"
	"trestle/internal/ninja"
	"trestle/internal/plan"
	"trestle/internal/policy"
	"trestle/internal/processx"
	"trestle/internal/runner"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/state"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

// BuildResult reports what a generation run produced: the manifest path, the
// profile, per-target output paths, extra runtime files, and the environment the
// build needs.
type BuildResult struct {
	ConfigPath     string
	Manifest       string
	Profile        string
	Outputs        map[model.TargetID]string
	RuntimeOutputs map[model.TargetID][]string
	Environment    map[string]string
}

// ConfigureOptions carries the settings resolved from the command-line flags and
// applied by Configure. Empty string fields, and a false ModulesEnabled, leave
// the corresponding configuration value untouched.
type ConfigureOptions struct {
	Path           string
	Compiler       string
	C              string
	Profile        string
	VcpkgRoot      string
	Triplet        string
	MSVC           string
	CUDAVersion    string
	CUDAMode       string
	Setup          string
	ModuleScanner  string
	ModulesEnabled bool
}

// Configure resolves the requested compiler, profile, package, CUDA, and module
// settings into a portable trestle.toml, filling in an auto-detected vcpkg
// triplet when needed.
func Configure(options ConfigureOptions) error {
	cfg, err := ensureProjectConfig(options.Path)
	if err != nil {
		return err
	}
	if options.Compiler != "" {
		cfg.Toolchain.CXX = toolchain.PortableSelector(options.Compiler)
	}
	if options.C != "" {
		cfg.Toolchain.C = toolchain.PortableSelector(options.C)
	}
	if options.Profile != "" {
		cfg.Build.Profile = options.Profile
	}
	if options.MSVC != "" {
		cfg.Toolchain.MSVC = strings.TrimSpace(options.MSVC)
	}
	if options.Setup != "" {
		// A vcvars path still names one machine. Record the toolset version it
		// belongs to instead so the committed configuration stays portable.
		if version, ok := toolchain.MSVCToolsetVersion(options.Setup); ok && cfg.Toolchain.MSVC == "" {
			cfg.Toolchain.MSVC = version + "~" + version
		}
		cfg.Toolchain.Setup = ""
	}
	tc, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return err
	}
	if options.VcpkgRoot != "" {
		cfg.Vcpkg.Root = options.VcpkgRoot
	}
	if cfg.Vcpkg.Root == "" {
		cfg.Vcpkg.Root = os.Getenv("VCPKG_ROOT")
	}
	if options.Triplet != "" {
		cfg.Vcpkg.Triplet = options.Triplet
	}
	if options.CUDAVersion != "" {
		version, root, resolveErr := configureCUDA(context.Background(), cfg, options.CUDAVersion)
		if resolveErr != nil {
			return resolveErr
		}
		cfg.Toolchain.CUDA, cfg.Toolchain.CUDARoot = version, root
	}
	if options.CUDAMode != "" {
		cfg.Toolchain.CUDAMode = options.CUDAMode
	}
	if options.ModuleScanner != "" {
		cfg.Toolchain.ModuleScanner = options.ModuleScanner
	}
	if options.ModulesEnabled {
		cfg.Build.Modules = true
	}
	if cfg.Vcpkg.Root != "" && (cfg.Vcpkg.Triplet == "" || cfg.Vcpkg.Triplet == "auto") {
		available, err := vcpkg.AvailableTriplets(cfg.Vcpkg.Root)
		if err != nil {
			return err
		}
		osName, arch := toolchain.TargetPlatform(tc)
		selected, err := vcpkg.SelectAutoTriplet(vcpkg.TripletRequest{OS: osName, Arch: arch, Compiler: string(tc.Kind), ABI: toolchain.TargetABI(tc), Available: available})
		if err != nil {
			return err
		}
		cfg.Vcpkg.Triplet = selected
	}
	if err := config.Save(options.Path, cfg); err != nil {
		return err
	}
	fmt.Printf("toolchain: %s (%s)\n", tc.Kind, tc.Version)
	if install, ok := configuredMSVC(context.Background(), cfg); ok {
		fmt.Printf("msvc: %s\n", install.Version)
	}
	fmt.Printf("c: %s\ncxx: %s\n", cfg.Toolchain.C, cfg.Toolchain.CXX)
	return nil
}

// configureCUDA turns a user-supplied version range or toolkit path into the
// portable pair stored in trestle.toml.
func configureCUDA(ctx context.Context, cfg config.Config, requested string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	resolve := func(spec string) error {
		probe := cfg
		probe.Toolchain.CUDA, probe.Toolchain.CUDARoot = spec, ""
		_, err := toolchain.ResolveCUDARoot(ctx, toolchainRequest(probe))
		return err
	}
	if toolchain.IsLocalPath(requested) {
		// An explicit path still resolves to a version range when the layout
		// names one, so only the range is committed when possible.
		if version, ok := toolchain.ToolkitVersionFromPath(requested); ok {
			if err := resolve(version + "~" + version); err != nil {
				return "", "", err
			}
			return version + "~" + version, "", nil
		}
		return "auto", requested, nil
	}
	if err := resolve(requested); err != nil {
		return "", "", err
	}
	return requested, "", nil
}

// configuredMSVC reports which installed toolset a constrained msvc range
// selected, so configure can confirm the pin was honoured.
func configuredMSVC(ctx context.Context, cfg config.Config) (toolchain.MSVCInstall, bool) {
	install, err := toolchain.ResolveMSVC(toolchain.DiscoverInventory(ctx, toolchain.CacheDir(cfg.Toolchain.CacheDir, cfg.Root())), cfg.Toolchain.MSVC)
	return install, err == nil
}

func detectConfigured(ctx context.Context, cfg config.Config) (toolchain.Toolchain, error) {
	request := toolchainRequest(cfg)
	if runtime.GOOS == "windows" && cfg.Toolchain.Mode != "wsl" && (cfg.Toolchain.CUDA != "" || cfg.Toolchain.CUDARoot != "") && (request.CXX == "" || request.CXX == "auto") {
		request.CXX = "cl"
		if install, ok := configuredMSVC(ctx, cfg); ok {
			request.CXX = install.Compiler
			if request.C == "" || request.C == "auto" {
				request.C = install.Compiler
			}
		}
	}
	return toolchain.DetectConfigured(ctx, request)
}

// toolchainRequest converts the configuration into the portable specification
// that toolchain.DetectConfigured resolves. Machine-local paths are only passed
// through when the project set them explicitly as an escape hatch.
func toolchainRequest(cfg config.Config) toolchain.Request {
	return toolchain.Request{
		Mode:            cfg.Toolchain.Mode,
		WSLDistribution: cfg.Toolchain.WSLDistribution,
		C:               cfg.Toolchain.C,
		CXX:             cfg.Toolchain.CXX,
		Archiver:        cfg.Toolchain.Archiver,
		Linker:          cfg.Toolchain.Linker,
		Setup:           cfg.Toolchain.Setup,
		MSVC:            cfg.Toolchain.MSVC,
		CUDA:            cfg.Toolchain.CUDA,
		CUDARoot:        cfg.Toolchain.CUDARoot,
		CacheDir:        toolchain.CacheDir(cfg.Toolchain.CacheDir, cfg.Root()),
	}
}

// cudaRoot resolves [toolchain].cuda into the toolkit directory that holds nvcc.
func cudaRoot(ctx context.Context, cfg config.Config) (string, error) {
	return toolchain.ResolveCUDARoot(ctx, toolchainRequest(cfg))
}

// SetProfile stores the build profile, which must be "debug" or "release".
func SetProfile(path, profile string) error {
	if profile != "debug" && profile != "release" {
		return fmt.Errorf("profile must be debug or release")
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Build.Profile = profile
	return config.Save(path, cfg)
}

// SetCompileFlags replaces the project-wide compile flags with the
// whitespace-separated values in flags.
func SetCompileFlags(path, flags string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Build.CompileFlags = strings.Fields(flags)
	return config.Save(path, cfg)
}

// SetCompiler records a native C++ compiler selector, clearing any WSL or
// environment setup and re-resolving the toolchain so a bad choice is rejected
// before it is saved.
func SetCompiler(path, compiler string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	compiler = strings.TrimSpace(compiler)
	if compiler == "" {
		return fmt.Errorf("compiler path is required")
	}
	cfg.Toolchain.CXX = toolchain.PortableSelector(compiler)
	cfg.Toolchain.Mode = "native"
	cfg.Toolchain.WSLDistribution = ""
	// The resolved executables are rediscovered on every run, so only the
	// constraint that describes them is stored.
	cfg.Toolchain.Archiver = "auto"
	cfg.Toolchain.Linker = "auto"
	if version, ok := toolchain.MSVCToolsetVersion(compiler); ok {
		cfg.Toolchain.MSVC = version + "~" + version
	}
	cfg.Toolchain.Setup = ""
	if _, err := detectConfigured(context.Background(), cfg); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

// SetWSLCompiler records a compiler that runs inside the named WSL distribution.
func SetWSLCompiler(path, distribution, compiler string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Toolchain.Mode = "wsl"
	cfg.Toolchain.WSLDistribution = strings.TrimSpace(distribution)
	cfg.Toolchain.CXX = strings.TrimSpace(compiler)
	cfg.Toolchain.Setup = ""
	cfg.Toolchain.Archiver = "auto"
	cfg.Toolchain.Linker = "auto"
	if _, err := detectConfigured(context.Background(), cfg); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

// SetEnvironment stores an environment setup script, or clears it when given
// "inherit" or "none"; a non-empty script is resolved so its concrete C,
// archiver, and linker tools are recorded.
func SetEnvironment(path, setup string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	setup = strings.TrimSpace(setup)
	if strings.EqualFold(setup, "inherit") || strings.EqualFold(setup, "none") {
		setup = ""
	}
	if setup != "" {
		if _, err := os.Stat(setup); err != nil {
			return fmt.Errorf("environment setup script: %w", err)
		}
	}
	cfg.Toolchain.Setup = setup
	if setup != "" {
		tc, err := detectConfigured(context.Background(), cfg)
		if err != nil {
			return err
		}
		cfg.Toolchain.C, cfg.Toolchain.Archiver, cfg.Toolchain.Linker = tc.CC, tc.Archiver, tc.Linker
	}
	return config.Save(path, cfg)
}

// SetCUDA enables or disables native CUDA for the project, recording the version
// range or toolkit root in root.
func SetCUDA(path string, enabled bool, root string) error {
	return SetCUDAConnection(path, enabled, root, "native", "")
}

// SetCUDAConnection enables or disables CUDA with an explicit execution
// environment, resolving a WSL toolkit through the distribution or a native
// toolkit to its version range.
func SetCUDAConnection(configPath string, enabled bool, root, execution, distribution string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !enabled {
		cfg.Toolchain.CUDA = ""
		cfg.Toolchain.CUDARoot = ""
		cfg.Toolchain.CUDAExecution = "native"
		cfg.Toolchain.CUDAWSLDistribution = ""
		return config.Save(configPath, cfg)
	}
	request := strings.TrimSpace(root)
	execution = strings.ToLower(strings.TrimSpace(execution))
	if execution == "" {
		execution = "native"
	}
	distribution = strings.TrimSpace(distribution)
	switch execution {
	case "wsl":
		request, root, distribution, err = resolveWSLConnection(cfg, distribution, request)
		if err != nil {
			return err
		}
	case "native":
		version, toolkit, resolveErr := configureCUDA(context.Background(), cfg, request)
		if resolveErr != nil {
			return resolveErr
		}
		request, root = version, toolkit
	default:
		return fmt.Errorf("CUDA execution mode %q is unsupported", execution)
	}
	cfg.Toolchain.CUDA = request
	cfg.Toolchain.CUDARoot = root
	cfg.Toolchain.CUDAExecution = execution
	cfg.Toolchain.CUDAWSLDistribution = distribution
	return config.Save(configPath, cfg)
}

// resolveWSLConnection resolves a requested WSL CUDA toolkit into the portable
// version range or root the configuration stores, returning the effective
// distribution alongside them. A WSL toolkit root is a Linux path on this
// machine, so the version range is kept when the layout names one, ensuring
// nothing machine-specific is stored.
func resolveWSLConnection(cfg config.Config, distribution, request string) (string, string, string, error) {
	if cfg.Toolchain.Mode != "wsl" {
		return "", "", "", fmt.Errorf("E_CUDA_WSL_HOST: select a WSL C/C++ compiler before connecting WSL CUDA")
	}
	if distribution == "" {
		distribution = cfg.Toolchain.WSLDistribution
	}
	if cfg.Toolchain.WSLDistribution != "" && distribution != "" && !strings.EqualFold(cfg.Toolchain.WSLDistribution, distribution) {
		return "", "", "", fmt.Errorf("E_CUDA_WSL_DISTRIBUTION: CUDA is in %s but the compiler is in %s", distribution, cfg.Toolchain.WSLDistribution)
	}
	host, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return "", "", "", err
	}
	detected, err := cuda.DetectWSL(context.Background(), distribution, request, host)
	if err != nil {
		return "", "", "", err
	}
	if version, ok := toolkitVersionOfRoot(detected.Toolkit.Root, detected.Toolkit.Version); ok {
		return version + "~" + version, "", distribution, nil
	}
	return "auto", detected.Toolkit.Root, distribution, nil
}

// toolkitVersionOfRoot recovers a CUDA version from a WSL toolkit root, whose
// layout only carries a version once nvcc has been executed.
func toolkitVersionOfRoot(root, reported string) (string, bool) {
	if version, ok := toolchain.ToolkitVersionFromPath(root); ok {
		return version, true
	}
	if version, ok := toolchain.ExtractVersion(reported); ok {
		return version, true
	}
	return "", false
}

// SetVulkanConnection enables or disables the Vulkan SDK, detecting the SDK root
// either natively or inside a WSL distribution.
func SetVulkanConnection(configPath string, enabled bool, root, execution, distribution string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !enabled {
		cfg.Toolchain.Vulkan = ""
		cfg.Toolchain.VulkanExecution = "native"
		cfg.Toolchain.VulkanWSLDistribution = ""
		return config.Save(configPath, cfg)
	}
	execution = strings.ToLower(strings.TrimSpace(execution))
	if execution == "" {
		execution = "native"
	}
	root = strings.TrimSpace(root)
	distribution = strings.TrimSpace(distribution)
	if execution == "wsl" {
		if distribution == "" {
			distribution = cfg.Toolchain.WSLDistribution
		}
		detected, detectErr := vulkan.DetectWSL(context.Background(), distribution, root)
		if detectErr != nil {
			return detectErr
		}
		root = detected.Root
	} else if execution == "native" {
		if root == "" {
			root = os.Getenv("VULKAN_SDK")
		}
		detected, detectErr := vulkan.DetectRoot(root)
		if detectErr != nil {
			return detectErr
		}
		root = detected.Root
	} else {
		return fmt.Errorf("Vulkan execution mode %q is unsupported", execution)
	}
	cfg.Toolchain.Vulkan = root
	cfg.Toolchain.VulkanExecution = execution
	cfg.Toolchain.VulkanWSLDistribution = distribution
	return config.Save(configPath, cfg)
}

// SetTestGroup assigns a test job to a group so it can be selected together with
// other jobs.
func SetTestGroup(path, job, group string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	target, ok := cfg.Targets[job]
	if !ok || target.Type != "test" {
		return fmt.Errorf("test job %q was not found", job)
	}
	target.TestGroup = strings.TrimSpace(group)
	cfg.Targets[job] = target
	return config.Save(path, cfg)
}

func importCMake(ctx context.Context, path string, progress func(string)) (cmakeimport.Result, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return cmakeimport.Result{}, err
	}
	options := cmakeimport.Options{}
	// detectConfigured honours the msvc pin and setup scripts, so a configured
	// cl that is not on PATH still resolves. Falling back to CMake's own
	// compiler pick would configure the import with an unconfigured clang++ and
	// fail its standard-library try-compiles, so a detection failure is fatal
	// in native mode. WSL mode keeps CMake's default pick because the Windows
	// cmake process cannot drive a WSL compiler.
	if cfg.Toolchain.Mode != "wsl" {
		selected, detectErr := detectConfigured(ctx, cfg)
		if detectErr != nil {
			return cmakeimport.Result{}, fmt.Errorf("import cmake needs a working toolchain: %w", detectErr)
		}
		options.CCompiler = selected.CC
		options.CXXCompiler = selected.CXX
		options.Environment = selected.Env
		if cfg.Toolchain.C != "" && cfg.Toolchain.C != "auto" {
			options.CCompiler = cfg.Toolchain.C
		}
	}
	return cmakeimport.ImportWithOptions(ctx, filepath.Dir(path), path, progress, options)
}

// ImportXmake imports targets from an xmake.lua project into trestle.toml.
func ImportXmake(ctx context.Context, path string, progress func(string)) error {
	return runOperation(ctx, path, "import xmake", progress, func(ctx context.Context, emit func(string)) error {
		ctx, release, err := acquireResource(ctx, filepath.Join(filepath.Dir(path), ".trestle", "import"), "import xmake", false)
		if err != nil {
			return err
		}
		defer release()
		return xmakeimport.Import(ctx, filepath.Dir(path), path, emit)
	})
}

// SetFlags replaces the project-wide compile and link flags, leaving each group
// unchanged when its argument is empty.
func SetFlags(path, compile, link string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if compile != "" {
		cfg.Build.CompileFlags = strings.Fields(compile)
	}
	if link != "" {
		cfg.Build.LinkFlags = strings.Fields(link)
	}
	return config.Save(path, cfg)
}

// ConfigureCompilerOptions updates the toolchain execution mode, WSL
// distribution, and C and C++ flag lists, ignoring empty arguments.
func ConfigureCompilerOptions(path, mode, distribution, cflags, cxxflags string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if mode != "" {
		cfg.Toolchain.Mode = strings.ToLower(strings.TrimSpace(mode))
	}
	if distribution != "" {
		cfg.Toolchain.WSLDistribution = strings.TrimSpace(distribution)
	}
	if cflags != "" {
		cfg.Build.CFlags = strings.Fields(cflags)
	}
	if cxxflags != "" {
		cfg.Build.CXXFlags = strings.Fields(cxxflags)
	}
	return config.Save(path, cfg)
}

// SetLanguageFlags replaces the flags for one group: "c", "cxx", or "link".
func SetLanguageFlags(path, language, flags string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	values := strings.Fields(flags)
	switch language {
	case "c":
		cfg.Build.CFlags = values
	case "cxx":
		cfg.Build.CXXFlags = values
	case "link":
		cfg.Build.LinkFlags = values
	default:
		return fmt.Errorf("unknown flag group %q", language)
	}
	return config.Save(path, cfg)
}

// SetDefaultTargets records the targets built when no target is requested,
// rejecting unknown names.
func SetDefaultTargets(path string, targets []string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	for _, name := range targets {
		if _, ok := cfg.Targets[name]; !ok {
			return fmt.Errorf("target %q was not found", name)
		}
	}
	cfg.Build.DefaultTargets = append([]string{}, targets...)
	return config.Save(path, cfg)
}

// ApplyCompilerPreset copies a stored compiler preset into the live toolchain and
// flag settings.
func ApplyCompilerPreset(path, name string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	preset, ok := cfg.CompilerPresets[name]
	if !ok {
		return fmt.Errorf("compiler preset %q was not found", name)
	}
	applyCompilerPreset(&cfg, name, preset)
	return config.Save(path, cfg)
}

func applyCompilerPreset(cfg *config.Config, name string, preset config.CompilerPreset) {
	if preset.C != "" {
		cfg.Toolchain.C = preset.C
	}
	if preset.CXX != "" {
		cfg.Toolchain.CXX = preset.CXX
	}
	if preset.Archiver != "" {
		cfg.Toolchain.Archiver = preset.Archiver
	}
	if preset.Linker != "" {
		cfg.Toolchain.Linker = preset.Linker
	}
	if preset.Setup != "" {
		cfg.Toolchain.Setup = preset.Setup
	}
	if preset.Mode != "" {
		cfg.Toolchain.Mode = preset.Mode
	}
	if preset.WSLDistribution != "" {
		cfg.Toolchain.WSLDistribution = preset.WSLDistribution
	}
	cfg.Build.CompileFlags = append([]string{}, preset.CompileFlags...)
	cfg.Build.CFlags = append([]string{}, preset.CFlags...)
	cfg.Build.CXXFlags = append([]string{}, preset.CXXFlags...)
	cfg.Build.LinkFlags = append([]string{}, preset.LinkFlags...)
	cfg.Toolchain.Preset = name
}

// SaveCompilerPreset stores the current compiler and flag settings under name for
// later reuse.
func SaveCompilerPreset(path, name string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("preset name is required")
	}
	cfg.CompilerPresets[name] = config.CompilerPreset{C: cfg.Toolchain.C, CXX: cfg.Toolchain.CXX, Archiver: cfg.Toolchain.Archiver, Linker: cfg.Toolchain.Linker, Setup: cfg.Toolchain.Setup, Mode: cfg.Toolchain.Mode, WSLDistribution: cfg.Toolchain.WSLDistribution, CompileFlags: append([]string{}, cfg.Build.CompileFlags...), CFlags: append([]string{}, cfg.Build.CFlags...), CXXFlags: append([]string{}, cfg.Build.CXXFlags...), LinkFlags: append([]string{}, cfg.Build.LinkFlags...)}
	return config.Save(path, cfg)
}

// DeleteCompilerPreset removes a stored compiler preset and clears any references
// to it from the toolchain and release optimization settings.
func DeleteCompilerPreset(path, name string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if _, ok := cfg.CompilerPresets[name]; !ok {
		return fmt.Errorf("compiler preset %q was not found", name)
	}
	delete(cfg.CompilerPresets, name)
	if cfg.Toolchain.Preset == name {
		cfg.Toolchain.Preset = ""
	}
	if cfg.Package.Optimization == "custom:"+name {
		cfg.Package.Optimization = "balanced"
	}
	return config.Save(path, cfg)
}

// SetProjectSetting edits one project, build, toolchain, package, target, or
// preset field addressed by key, validating the result before saving.
func SetProjectSetting(path, key, value string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := applyProjectSetting(&cfg, key, value); err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

// applyProjectSetting routes one setting assignment to the section that owns the
// key, rejecting keys that are not editable.
func applyProjectSetting(cfg *config.Config, key, value string) error {
	switch {
	case strings.HasPrefix(key, "build."):
		return applyBuildSetting(cfg, key, value)
	case strings.HasPrefix(key, "toolchain."):
		return applyToolchainSetting(cfg, key, value)
	case strings.HasPrefix(key, "vcpkg."):
		return applyVcpkgSetting(cfg, key, value)
	case strings.HasPrefix(key, "target:"):
		return applyTargetSetting(cfg, key, value)
	case strings.HasPrefix(key, "preset:"):
		return applyPresetSetting(cfg, key, value)
	}
	switch key {
	case "project.name":
		cfg.Project.Name = value
	case "package.output":
		cfg.Package.Output = value
	default:
		return fmt.Errorf("setting %q is not editable", key)
	}
	return nil
}

// applyBuildSetting applies one build.* setting, rejecting unknown fields.
func applyBuildSetting(cfg *config.Config, key, value string) error {
	values := strings.Fields(value)
	switch key {
	case "build.build_dir":
		cfg.Build.BuildDir = value
	case "build.c_standard":
		cfg.Build.CStandard = value
	case "build.cxx_standard":
		cfg.Build.CXXStandard = value
	case "build.compile_flags":
		cfg.Build.CompileFlags = values
	case "build.c_flags":
		cfg.Build.CFlags = values
	case "build.cxx_flags":
		cfg.Build.CXXFlags = values
	case "build.link_flags":
		cfg.Build.LinkFlags = values
	case "build.default_targets":
		cfg.Build.DefaultTargets = splitSettingList(value)
	case "build.compile_commands":
		cfg.Build.CompileCommands = value
	default:
		return fmt.Errorf("setting %q is not editable", key)
	}
	return nil
}

// applyToolchainSetting applies one toolchain.* setting, rejecting unknown
// fields.
func applyToolchainSetting(cfg *config.Config, key, value string) error {
	switch key {
	case "toolchain.mode":
		cfg.Toolchain.Mode = value
	case "toolchain.wsl_distribution":
		cfg.Toolchain.WSLDistribution = value
	case "toolchain.c":
		cfg.Toolchain.C = toolchain.PortableSelector(value)
	case "toolchain.cxx":
		cfg.Toolchain.CXX = toolchain.PortableSelector(value)
	case "toolchain.msvc":
		cfg.Toolchain.MSVC = value
	case "toolchain.archiver":
		cfg.Toolchain.Archiver = value
	case "toolchain.linker":
		cfg.Toolchain.Linker = value
	case "toolchain.setup":
		cfg.Toolchain.Setup = value
	case "toolchain.cache_dir":
		cfg.Toolchain.CacheDir = value
	case "toolchain.cuda":
		cfg.Toolchain.CUDA = value
	case "toolchain.cuda_root":
		cfg.Toolchain.CUDARoot = value
	case "toolchain.cuda_mode":
		cfg.Toolchain.CUDAMode = value
	case "toolchain.cuda_architectures":
		cfg.Toolchain.CUDAArchitectures = splitSettingList(value)
	case "toolchain.cuda_execution":
		cfg.Toolchain.CUDAExecution = value
	case "toolchain.cuda_wsl_distribution":
		cfg.Toolchain.CUDAWSLDistribution = value
	case "toolchain.vulkan":
		cfg.Toolchain.Vulkan = value
	case "toolchain.vulkan_execution":
		cfg.Toolchain.VulkanExecution = value
	case "toolchain.vulkan_wsl_distribution":
		cfg.Toolchain.VulkanWSLDistribution = value
	default:
		return fmt.Errorf("setting %q is not editable", key)
	}
	return nil
}

// applyVcpkgSetting applies one vcpkg.* setting, rejecting unknown fields.
func applyVcpkgSetting(cfg *config.Config, key, value string) error {
	switch key {
	case "vcpkg.root":
		cfg.Vcpkg.Root = value
	case "vcpkg.triplet":
		cfg.Vcpkg.Triplet = value
	case "vcpkg.crt_linkage":
		cfg.Vcpkg.CRTLinkage = value
	case "vcpkg.library_linkage":
		cfg.Vcpkg.LibraryLinkage = value
	default:
		return fmt.Errorf("setting %q is not editable", key)
	}
	return nil
}

// applyTargetSetting applies one target:<name>:<field> setting, rejecting a
// malformed key, an unknown target, or a non-editable field.
func applyTargetSetting(cfg *config.Config, key, value string) error {
	rest := strings.TrimPrefix(key, "target:")
	cut := strings.LastIndex(rest, ":")
	if cut < 1 {
		return fmt.Errorf("invalid target setting %q", key)
	}
	name, field := rest[:cut], rest[cut+1:]
	target, ok := cfg.Targets[name]
	if !ok {
		return fmt.Errorf("target %q was not found", name)
	}
	values := strings.Fields(value)
	switch field {
	case "type":
		target.Type = value
	case "sources":
		target.Sources = splitSettingList(value)
	case "output_name":
		target.OutputName = value
	case "c_standard":
		target.CStandard = value
	case "cxx_standard":
		target.CXXStandard = value
	case "include_dirs":
		target.IncludeDirs = splitSettingList(value)
	case "private_include_dirs":
		target.PrivateIncludeDirs = splitSettingList(value)
	case "defines":
		target.Defines = splitSettingList(value)
	case "private_defines":
		target.PrivateDefines = splitSettingList(value)
	case "compile_options":
		target.CompileOptions = values
	case "c_flags":
		target.CFlags = values
	case "cxx_flags":
		target.CXXFlags = values
	case "library_dirs":
		target.LibraryDirs = splitSettingList(value)
	case "libraries":
		target.Libraries = splitSettingList(value)
	case "link_options":
		target.LinkOptions = values
	case "export_all_symbols":
		parsed, e := strconv.ParseBool(value)
		if e != nil {
			return e
		}
		target.ExportAllSymbols = parsed
	default:
		return fmt.Errorf("target setting %q is not editable", field)
	}
	cfg.Targets[name] = target
	return nil
}

// applyPresetSetting applies one preset:<name>:<field> setting, rejecting a
// malformed key, an unknown preset, or a non-editable field.
func applyPresetSetting(cfg *config.Config, key, value string) error {
	rest := strings.TrimPrefix(key, "preset:")
	cut := strings.LastIndex(rest, ":")
	if cut < 1 {
		return fmt.Errorf("invalid preset setting %q", key)
	}
	name, field := rest[:cut], rest[cut+1:]
	preset, ok := cfg.CompilerPresets[name]
	if !ok {
		return fmt.Errorf("compiler preset %q was not found", name)
	}
	values := strings.Fields(value)
	switch field {
	case "c":
		preset.C = value
	case "cxx":
		preset.CXX = value
	case "archiver":
		preset.Archiver = value
	case "linker":
		preset.Linker = value
	case "setup":
		preset.Setup = value
	case "mode":
		preset.Mode = value
	case "wsl_distribution":
		preset.WSLDistribution = value
	case "compile_flags":
		preset.CompileFlags = values
	case "c_flags":
		preset.CFlags = values
	case "cxx_flags":
		preset.CXXFlags = values
	case "link_flags":
		preset.LinkFlags = values
	default:
		return fmt.Errorf("preset setting %q is not editable", field)
	}
	cfg.CompilerPresets[name] = preset
	return nil
}

// splitSettingList splits a comma- or semicolon-separated setting value into its
// trimmed, non-empty items.
func splitSettingList(value string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// OptimizationPresetNames lists the built-in release optimizations followed by
// each compiler preset as a "custom:" entry.
func OptimizationPresetNames(cfg config.Config) []string {
	result := []string{"balanced", "speed", "size"}
	names := make([]string, 0, len(cfg.CompilerPresets))
	for name := range cfg.CompilerPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result = append(result, "custom:"+name)
	}
	return result
}

// ConfigureRelease records the release optimization, targets, and output path,
// validating each before saving.
func ConfigureRelease(path, preset string, targets []string, output string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if preset != "" {
		valid := false
		for _, name := range OptimizationPresetNames(cfg) {
			if name == preset {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("release optimization %q was not found", preset)
		}
		cfg.Package.Optimization = preset
	}
	if len(targets) > 0 {
		for _, name := range targets {
			if _, ok := cfg.Targets[name]; !ok {
				return fmt.Errorf("target %q was not found", name)
			}
		}
		cfg.Package.Targets = append([]string{}, targets...)
	}
	if output != "" {
		cfg.Package.Output = output
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

func applyReleaseOptimization(cfg *config.Config) error {
	name := cfg.Package.Optimization
	if name == "" {
		name = "balanced"
	}
	cfg.Build.Profile = "release"
	if strings.HasPrefix(name, "custom:") {
		presetName := strings.TrimPrefix(name, "custom:")
		preset, ok := cfg.CompilerPresets[presetName]
		if !ok {
			return fmt.Errorf("release optimization preset %q was not found", presetName)
		}
		applyCompilerPreset(cfg, presetName, preset)
		return nil
	}
	msvc := strings.Contains(strings.ToLower(cfg.Toolchain.CXX), "cl.exe") || strings.Contains(strings.ToLower(cfg.Toolchain.CXX), "clang-cl")
	msvcLink := msvc || runtime.GOOS == "windows" && cfg.Toolchain.Mode != "wsl" && cfg.Toolchain.Setup != "" && strings.Contains(strings.ToLower(cfg.Toolchain.CXX), "clang")
	cfg.Build.CompileFlags = filterReleaseFlags(cfg.Build.CompileFlags)
	cfg.Build.LinkFlags = filterReleaseFlags(cfg.Build.LinkFlags)
	compileFlags, linkFlags, err := releaseOptimizationFlags(name, msvc, msvcLink, runtime.GOOS == "darwin" && cfg.Toolchain.Mode != "wsl")
	if err != nil {
		return err
	}
	cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, compileFlags...)
	cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, linkFlags...)
	return nil
}

// filterReleaseFlags drops optimization and debug flags that a built-in release
// optimization re-adds explicitly, leaving an unrelated user flag untouched.
func filterReleaseFlags(flags []string) []string {
	var out []string
	for _, flag := range flags {
		lower := strings.ToLower(flag)
		if strings.HasPrefix(lower, "/o") || strings.HasPrefix(lower, "-o") || lower == "/gl" || lower == "-flto" || lower == "/ltcg" || lower == "-ffunction-sections" || lower == "-fdata-sections" || lower == "-dndebug" || lower == "/dndebug" || lower == "-wl,--gc-sections" || lower == "-wl,-dead_strip" || strings.HasPrefix(lower, "-wl,/opt:") || lower == "/ob3" {
			continue
		}
		out = append(out, flag)
	}
	return out
}

// releaseOptimizationFlags returns the compile and link flags a built-in release
// optimization adds, honouring the MSVC and clang-cl variants of each.
func releaseOptimizationFlags(name string, msvc, msvcLink, darwinNative bool) ([]string, []string, error) {
	switch name {
	case "balanced":
		if msvc {
			return []string{"/O2", "/DNDEBUG"}, []string{"/OPT:REF", "/OPT:ICF"}, nil
		}
		return []string{"-O2", "-DNDEBUG"}, nil, nil
	case "speed":
		if msvc {
			return []string{"/O2", "/Ob3", "/GL", "/DNDEBUG"}, []string{"/LTCG", "/OPT:REF", "/OPT:ICF"}, nil
		}
		return []string{"-O3", "-flto", "-DNDEBUG"}, []string{"-flto"}, nil
	case "size":
		if msvc {
			return []string{"/O1", "/GL", "/DNDEBUG"}, []string{"/LTCG", "/OPT:REF", "/OPT:ICF"}, nil
		}
		compile := []string{"-Os", "-ffunction-sections", "-fdata-sections", "-DNDEBUG"}
		switch {
		case msvcLink:
			return compile, []string{"-Wl,/OPT:REF", "-Wl,/OPT:ICF"}, nil
		case darwinNative:
			return compile, []string{"-Wl,-dead_strip"}, nil
		default:
			return compile, []string{"-Wl,--gc-sections"}, nil
		}
	default:
		return nil, nil, fmt.Errorf("unknown release optimization %q", name)
	}
}

func releaseWithProgress(ctx context.Context, path string, progress func(string)) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := applyReleaseOptimization(&cfg); err != nil {
		return err
	}
	base := cfg
	cfg, err = policy.ApplyAt(ctx, cfg, filepath.Dir(path))
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, configurationKey{}, cfg)
	ctx, release, err := buildResources(ctx, cfg, "release")
	if err != nil {
		return err
	}
	defer release()
	ctx, releaseOutput, err := acquireResource(ctx, cfg.Package.Output, "release archive", false)
	if err != nil {
		return err
	}
	defer releaseOutput()
	if err := config.Save(path, base); err != nil {
		return err
	}
	targets := cfg.Package.Targets
	if len(targets) == 0 {
		targets = cfg.Build.DefaultTargets
	}
	if progress != nil {
		progress("Release optimization: " + cfg.Package.Optimization)
		progress("Release targets: " + strings.Join(targets, ", "))
	}
	if err := BuildTargetsWithProgress(ctx, path, targets, false, progress); err != nil {
		return err
	}
	return packageSelectedWithContext(ctx, path, targets, progress)
}

// AddPackage records a package as a dependency of targetName, or of the most
// likely target when targetName is empty.
func AddPackage(path, name, targetName string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := addPackageToConfig(&cfg, name, targetName); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

func addPackageToConfig(cfg *config.Config, name, targetName string) error {
	return addProviderPackageToConfig(cfg, name, targetName, "vcpkg")
}

// addProviderPackageToConfig records an installed package under the project's
// chosen target. The provider selects how the raw name is parsed: vcpkg ports
// are "name[@version][features...]"-style strings already stripped upstream,
// while conan references are "name/version[@user/channel][features]".
func addProviderPackageToConfig(cfg *config.Config, name, targetName, provider string) error {
	name = strings.TrimSpace(name)
	version := ""
	if at := strings.LastIndex(name, "@"); at > 0 {
		version = name[at+1:]
		name = name[:at]
	}
	features := []string{}
	if opening := strings.Index(name, "["); opening > 0 && strings.HasSuffix(name, "]") {
		for _, feature := range strings.Split(name[opening+1:len(name)-1], ",") {
			if feature = strings.TrimSpace(feature); feature != "" {
				features = append(features, feature)
			}
		}
		name = name[:opening]
	}
	if name == "" {
		return fmt.Errorf("package name is required")
	}
	upsert := func() {
		if _, exists := cfg.Packages[name]; !exists {
			pkg := config.Package{Provider: provider, Port: name, Version: version, Features: features}
			if provider != "conan" {
				pkg.Triplet = cfg.Vcpkg.Triplet
			}
			cfg.Packages[name] = pkg
		} else if version != "" {
			pkg := cfg.Packages[name]
			pkg.Version = version
			if len(features) > 0 {
				pkg.Features = features
			}
			cfg.Packages[name] = pkg
		} else if len(features) > 0 {
			pkg := cfg.Packages[name]
			pkg.Features = features
			cfg.Packages[name] = pkg
		}
	}
	// A workspace root may be a pure container with no targets of its own:
	// packages declared there are shared with every member, so there is no
	// target dependency to wire.
	if len(cfg.Targets) == 0 && strings.TrimSpace(targetName) == "" {
		upsert()
		return nil
	}
	if targetName == "" {
		targetName = packageTarget(*cfg, name)
	}
	target, ok := cfg.Targets[targetName]
	if !ok {
		return fmt.Errorf("target %q not found", targetName)
	}
	upsert()
	for _, dependency := range target.Dependencies {
		if dependency.Package == name {
			return nil
		}
	}
	target.Dependencies = append(target.Dependencies, config.Dependency{Package: name, Scope: "private"})
	cfg.Targets[targetName] = target
	return nil
}

func packageTarget(cfg config.Config, packageName string) string {
	packageNames := map[string]bool{strings.ToLower(packageName): true}
	if withoutVersionSuffix := strings.TrimRight(strings.ToLower(packageName), "0123456789"); withoutVersionSuffix != "" {
		packageNames[withoutVersionSuffix] = true
	}
	var matches []string
	for name, target := range cfg.Targets {
		for _, library := range target.Libraries {
			base := strings.TrimPrefix(strings.ToLower(filepath.Base(library)), "lib")
			base = strings.TrimSuffix(base, filepath.Ext(base))
			if packageNames[base] {
				matches = append(matches, name)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return defaultTarget(cfg)
}

// SearchPackages looks the query up through whichever providers are usable:
// the conan client first when a conan installation resolves, then the vcpkg
// registry/local tree. Both result sets share the neutral PackageEntry view.
//
// A "name:version" or "vcpkg:name:version" term resolves to that exact version
// instead of a general search, so the user lands directly on the wanted release.
func SearchPackages(ctx context.Context, path, query string) ([]deps.PackageEntry, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	parsed := deps.ParseQuery(query)
	if parsed.Exact() {
		return resolveExactVersion(ctx, cfg, filepath.Dir(path), parsed)
	}
	return searchPackages(ctx, cfg, filepath.Dir(path), parsed)
}

func searchPackages(ctx context.Context, cfg config.Config, root string, parsed deps.PackageQuery) ([]deps.PackageEntry, error) {
	query := parsed.SearchTerm()
	var entries []deps.PackageEntry
	var failures []string
	if conanClient, conanErr := conanClientFor(cfg); conanErr == nil {
		// Search the local cache and every configured remote, so the page can
		// offer both installed recipes and upstream versions to install.
		remotes, remotesErr := conanClient.Remotes(ctx)
		if remotesErr != nil {
			failures = append(failures, "conan remotes: "+remotesErr.Error())
		}
		installed, localErr := conanClient.Search(ctx, query)
		if localErr == nil {
			entries = append(entries, deps.ConvertConanEntries(installed)...)
		} else {
			failures = append(failures, "conan: "+localErr.Error())
		}
		if len(remotes) > 0 {
			remote, remoteErr := conanClient.SearchRemotes(ctx, query, remotes)
			if remoteErr == nil {
				entries = append(entries, deps.ConvertConanEntries(remote)...)
			} else {
				failures = append(failures, "conan remote: "+remoteErr.Error())
			}
		}
	}
	registry := vcpkg.WebRegistry{CachePath: filepath.Join(root, ".trestle", "cache", "vcpkg-index.json")}
	client, clientErr := vcpkg.NewClient(cfg.Vcpkg.Root)
	if ports, cached := registry.SearchCached(query, 0); cached {
		if clientErr == nil {
			for index := range ports {
				ports[index].Installed = client.IsInstalled(ports[index].Name)
			}
		}
		entries = append(entries, deps.ConvertEntries(ports)...)
	} else if clientErr == nil {
		ports, localErr := client.SearchPorts(ctx, query)
		if localErr == nil {
			go func() {
				refreshCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_ = registry.Refresh(refreshCtx)
			}()
			entries = append(entries, deps.ConvertEntries(ports)...)
		} else {
			failures = append(failures, "vcpkg: "+localErr.Error())
		}
	} else {
		ports, webErr := registry.Search(ctx, query, 0)
		if webErr != nil {
			failures = append(failures, fmt.Sprintf("vcpkg online search failed (%v); local fallback unavailable (%v)", webErr, clientErr))
		} else {
			entries = append(entries, deps.ConvertEntries(ports)...)
		}
	}
	if len(entries) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return entries, nil
}

// resolveExactVersion answers a "name:version" query with that one version
// marked as the requested one. vcpkg is consulted through its version database,
// which is the only place historical releases are recorded; conan spells the
// version into the reference itself, so a remote lookup finds it directly.
func resolveExactVersion(ctx context.Context, cfg config.Config, root string, parsed deps.PackageQuery) ([]deps.PackageEntry, error) {
	var entries []deps.PackageEntry
	var failures []string
	if parsed.Provider != "conan" {
		client, err := vcpkg.NewClient(cfg.Vcpkg.Root)
		if err != nil {
			failures = append(failures, "vcpkg: "+err.Error())
		} else {
			found, historyErr := client.ResolveVersion(ctx, parsed.Name, parsed.Version)
			if historyErr != nil {
				failures = append(failures, "vcpkg: "+historyErr.Error())
			} else {
				entries = append(entries, deps.ConvertPortVersionEntries(found)...)
			}
		}
	}
	if parsed.Provider != "vcpkg" {
		client, err := conanClientFor(cfg)
		if err == nil {
			remotes, _ := client.Remotes(ctx)
			found, searchErr := client.SearchRemotes(ctx, parsed.SearchTerm(), remotes)
			if searchErr == nil {
				entries = append(entries, deps.ConvertConanEntries(found)...)
			} else {
				failures = append(failures, "conan: "+searchErr.Error())
			}
		}
	}
	if len(entries) == 0 {
		if len(failures) > 0 {
			return nil, fmt.Errorf("%s", strings.Join(failures, "; "))
		}
		return nil, fmt.Errorf("E_PACKAGE_VERSION: %s has no version %s", parsed.Name, parsed.Version)
	}
	deps.MarkRequestedVersion(entries, parsed.Version)
	return entries, nil
}

// conanClientFor builds a conan client from the [conan] config when the
// installation resolves; a missing conan is not an error for searches.
func conanClientFor(cfg config.Config) (conan.Client, error) {
	buildRoot := cfg.Conan.BuildRoot
	if buildRoot == "" && cfg.Build.BuildDir != "" {
		buildRoot = filepath.Dir(cfg.Build.BuildDir)
	}
	client, err := conan.NewClient(cfg.Conan.Root, buildRoot)
	if err != nil {
		return conan.Client{}, err
	}
	if cfg.Conan.Profile != "" {
		client.Profile = cfg.Conan.Profile
	}
	client.Settings = cfg.Conan.Settings
	client.Options = cfg.Conan.Options
	return client, nil
}

// InstallPackage downloads and builds a package with the provider implied by
// the reference (conan when it looks like name/version[@user/channel] or an
// existing conan record; vcpkg otherwise), then records it as a dependency of
// the project's default target.
func installPackage(ctx context.Context, path, name string, progress func(string)) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("package name is required")
	}
	if usesConanProvider(cfg, name) {
		return installConanPackage(ctx, path, cfg, name, progress)
	}
	return installVcpkgPackage(ctx, path, cfg, name, progress)
}

// usesConanProvider reports whether the requested reference targets conan: an
// explicit conan record, a versioned reference (name/version or @user/channel
// — vcpkg names never contain "/"), or a conan-only bracket-feature ref.
func usesConanProvider(cfg config.Config, name string) bool {
	if strings.HasPrefix(strings.ToLower(name), "conan:") {
		return true
	}
	if slash := strings.Index(name, "/"); slash > 0 {
		return true
	}
	if pkg, ok := cfg.Packages[name]; ok && pkg.Provider == "conan" {
		return true
	}
	return false
}

func installConanPackage(ctx context.Context, path string, cfg config.Config, reference string, progress func(string)) error {
	reference = strings.TrimPrefix(strings.TrimPrefix(reference, "conan:"), "conan ")
	client, err := conanClientFor(cfg)
	if err != nil {
		return err
	}
	if err := client.InstallWithProgress(ctx, reference, progress); err != nil {
		return err
	}
	if err := addProviderPackageToConfig(&cfg, reference, "", "conan"); err != nil {
		return err
	}
	if cfg.Conan.Root == "" {
		cfg.Conan.Root = client.Root
	}
	if cfg.Conan.Profile == "" && client.Profile != "" {
		cfg.Conan.Profile = filepath.Base(client.Profile)
	}
	return config.Save(path, cfg)
}

func installVcpkgPackage(ctx context.Context, path string, cfg config.Config, name string, progress func(string)) error {
	client, err := vcpkg.NewClient(cfg.Vcpkg.Root)
	if err != nil {
		return err
	}
	client.Environment = toolchain.EnvironmentForVcpkg(ctx, cfg.Toolchain.Setup, cfg.Toolchain.Archiver, cfg.Toolchain.CXX, cfg.Toolchain.C)
	triplet := cfg.Vcpkg.Triplet
	if triplet == "" || triplet == "auto" {
		tc, err := detectConfigured(ctx, cfg)
		if err != nil {
			return err
		}
		available, err := vcpkg.AvailableTriplets(client.Root)
		if err != nil {
			return err
		}
		osName, arch := toolchain.TargetPlatform(tc)
		triplet, err = vcpkg.SelectAutoTriplet(vcpkg.TripletRequest{OS: osName, Arch: arch, Compiler: string(tc.Kind), ABI: toolchain.TargetABI(tc), Available: available})
		if err != nil {
			return err
		}
	}
	if cfg.Toolchain.Mode == "wsl" {
		return fmt.Errorf("E_VCPKG_TARGET_MISMATCH: install Linux dependencies using Linux Trestle inside the WSL distribution; Windows vcpkg cannot install the configured WSL target")
	}
	if err := client.InstallWithProgress(ctx, []string{name}, triplet, progress); err != nil {
		return err
	}
	cfg.Vcpkg.Root, cfg.Vcpkg.Triplet = client.Root, triplet
	if err := addPackageToConfig(&cfg, name, ""); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

func defaultTarget(cfg config.Config) string {
	if _, ok := cfg.Targets["app"]; ok {
		return "app"
	}
	names := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if cfg.Targets[name].Type == "executable" {
			return name
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return "app"
}

// RemovePackage drops a package and removes it from every target's dependency
// list.
func RemovePackage(path, name string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	delete(cfg.Packages, name)
	for targetName, target := range cfg.Targets {
		dependencies := target.Dependencies[:0]
		for _, dependency := range target.Dependencies {
			if dependency.Package != name {
				dependencies = append(dependencies, dependency)
			}
		}
		target.Dependencies = dependencies
		cfg.Targets[targetName] = target
	}
	return config.Save(path, cfg)
}

// SetPackageVersion pins a configured package to a specific version.
func SetPackageVersion(path, name, version string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	pkg, ok := cfg.Packages[name]
	if !ok {
		return fmt.Errorf("package %q is not configured", name)
	}
	pkg.Version = strings.TrimSpace(version)
	cfg.Packages[name] = pkg
	return config.Save(path, cfg)
}

// Vcpkg searches the vcpkg registry, or installs the given ports with the
// requested triplet when install is set.
func Vcpkg(ctx context.Context, root, query string, ports []string, triplet string, install bool) error {
	return runOperation(ctx, config.DefaultFileName, "vcpkg", func(line string) { fmt.Println(line) }, func(ctx context.Context, emit func(string)) error {
		if install {
			client, err := vcpkg.NewClient(root)
			if err != nil {
				return err
			}
			if cfg, err := config.Load(config.DefaultFileName); err == nil {
				if cfg.Toolchain.Mode == "wsl" {
					return fmt.Errorf("E_VCPKG_TARGET_MISMATCH: install Linux dependencies with Linux Trestle inside the distribution")
				}
				client.Environment = toolchain.EnvironmentForVcpkg(ctx, cfg.Toolchain.Setup, cfg.Toolchain.Archiver, cfg.Toolchain.CXX, cfg.Toolchain.C)
				if triplet == "" || triplet == "auto" {
					triplet = cfg.Vcpkg.Triplet
				}
			}
			if triplet == "" || triplet == "auto" {
				return fmt.Errorf("E_VCPKG_TRIPLET: specify --triplet for the actual target OS, architecture and compiler ABI")
			}
			return client.InstallWithProgress(ctx, ports, triplet, emit)
		}
		results, err := (vcpkg.WebRegistry{}).Search(ctx, query, 0)
		if err != nil {
			return err
		}
		for _, result := range results {
			emit(fmt.Sprintf("%-24s %-14s %s", result.Name, result.Version, result.Description))
		}
		return nil
	})
}

// Toolchains reports the discovered toolchains and the MSVC/CUDA version ranges
// a project can pin. The native scan is cached in the project's toolchain cache
// directory, so pass refresh to pick up a newly installed toolset immediately.
func Toolchains(ctx context.Context, configPath, cacheOverride string, refresh bool) error {
	cacheDir := strings.TrimSpace(cacheOverride)
	if cacheDir == "" && configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			cacheDir = toolchain.CacheDir(cfg.Toolchain.CacheDir, cfg.Root())
		}
	} else if cacheDir != "" {
		cacheDir = toolchain.CacheDir(cacheDir, "")
	}
	if refresh && cacheDir != "" {
		if err := toolchain.Invalidate(cacheDir); err != nil {
			return err
		}
	}
	components := toolchain.Discover(ctx)
	if len(components) == 0 {
		return fmt.Errorf("E_TOOLCHAIN_NOT_FOUND: no supported development components were found")
	}
	for _, component := range components {
		status := "✗"
		if component.Ready {
			status = "✓"
		}
		fmt.Printf("%s %-10s %-20s %s\n", status, component.Family, component.Name, component.Version)
		if component.Path != "" {
			fmt.Printf("  %s\n", component.Path)
		} else if component.Detail != "" {
			fmt.Printf("  %s\n", component.Detail)
		}
	}
	if cacheDir != "" {
		fmt.Printf("inventory cache: %s\n", cacheDir)
	}
	if len(components) == 0 {
		return nil
	}
	inventory := toolchain.DiscoverInventoryWithTTL(ctx, cacheDir, 0)
	printSelectable("msvc", msvcVersions(inventory))
	printSelectable("cuda", cudaVersions(inventory))
	return nil
}

func printSelectable(field string, versions []string) {
	if len(versions) == 0 {
		return
	}
	fmt.Printf("  [toolchain].%s accepts %s\n", field, strings.Join(versions, ", "))
}

func msvcVersions(inventory toolchain.Inventory) []string {
	result := make([]string, 0, len(inventory.MSVC))
	for _, install := range inventory.MSVC {
		result = append(result, install.Version)
	}
	return result
}

func cudaVersions(inventory toolchain.Inventory) []string {
	result := make([]string, 0, len(inventory.CUDA))
	for _, install := range inventory.CUDA {
		result = append(result, install.Version)
	}
	return result
}

// Generate writes the Ninja manifest and supporting metadata for the project at
// path.
func Generate(path string) (BuildResult, error) {
	return GenerateWithContext(context.Background(), path)
}

// GenerateWithContext is Generate with a caller-supplied context, reusing a
// resolved configuration already carried by ctx when present.
func GenerateWithContext(ctx context.Context, path string) (BuildResult, error) {
	if cfg, ok := ctx.Value(configurationKey{}).(config.Config); ok {
		return generateWithConfig(ctx, path, cfg)
	}
	cfg, err := ensureProjectConfig(path)
	if err != nil {
		return BuildResult{}, err
	}
	cfg, err = policy.ApplyAt(ctx, cfg, filepath.Dir(path))
	if err != nil {
		return BuildResult{}, err
	}
	return generateWithConfig(ctx, path, cfg)
}

func generateWithConfig(ctx context.Context, path string, cfg config.Config) (BuildResult, error) {
	ctx, release, err := buildResources(ctx, cfg, "generate")
	if err != nil {
		return BuildResult{}, err
	}
	defer release()
	if err := checkPackagePlatform(cfg); err != nil {
		return BuildResult{}, err
	}
	// A workspace member reuses the workspace-wide resolution so cross-member
	// dependencies resolve, and keeps its manifest beside the other members'
	// under build/<profile>/<member>.
	var project model.ResolvedProject
	memberBuildDir := ""
	if workspace, ok := ctx.Value(workspaceProjectKey{}).(model.WorkspaceProject); ok {
		memberName, _ := ctx.Value(workspaceMemberKey{}).(string)
		member, found := workspace.Member(memberName)
		if !found {
			return BuildResult{}, fmt.Errorf("E_WORKSPACE_INVALID: member %q is not part of the resolved workspace", memberName)
		}
		project = member.Project
		memberBuildDir = member.BuildDir
	} else {
		project, err = graph.ResolveAt(ctx, cfg, filepath.Dir(path))
		if err != nil {
			return BuildResult{}, err
		}
	}
	hasNonShader := projectHasNonShader(project)
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return BuildResult{}, err
	}
	buildRoot, err := filepath.Abs(cfg.Build.BuildDir)
	if err != nil {
		return BuildResult{}, err
	}
	manifestDir := filepath.Join(buildRoot, cfg.Build.Profile)
	if memberBuildDir != "" {
		// A workspace member keeps its artifacts and manifests under
		// build/<profile>/<member>, beside the other members. plan.Build appends
		// the profile itself, so its BuildDir option is the member directory's
		// parent (build/<profile>), while the manifest lives in the member
		// directory.
		buildRoot = filepath.Dir(memberBuildDir)
		manifestDir = memberBuildDir
	}
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return BuildResult{}, err
	}
	var tc toolchain.Toolchain
	if hasNonShader {
		var err error
		tc, err = detectConfigured(ctx, cfg)
		if err != nil {
			return BuildResult{}, err
		}
		if err := checkPackageTarget(cfg, tc); err != nil {
			return BuildResult{}, err
		}
	}
	tc, err = applyConfiguredExecutables(ctx, cfg, tc, root)
	if err != nil {
		return BuildResult{}, err
	}
	if cfg.Toolchain.Mode == "wsl" && hasNonShader {
		linuxDir, pathErr := toolchain.WSLPath(ctx, cfg.Toolchain.WSLDistribution, manifestDir)
		if pathErr != nil {
			return BuildResult{}, pathErr
		}
		tc = toolchain.WithWSLDirectory(tc, linuxDir)
	}
	cfg = applyMSVCRuntimeFlag(cfg, tc)
	// Clang builds targeting the MSVC ABI can be older than the installed MSVC
	// STL accepts. Its documented escape hatch keeps the common pair (for
	// example Clang 18 with MSVC 14.44) buildable instead of failing with STL1000.
	if flag := toolchain.ClangMSVCSTLCompatibilityFlag(tc); flag != "" {
		present := false
		for _, existing := range cfg.Build.CompileFlags {
			if existing == flag {
				present = true
				break
			}
		}
		if !present {
			cfg.Build.CompileFlags = append(append([]string{}, cfg.Build.CompileFlags...), flag)
		}
	}
	cudaToolchain, err := detectCUDAToolchain(ctx, cfg, tc)
	if err != nil {
		return BuildResult{}, err
	}
	vulkanSDK, err := detectVulkanSDK(ctx, cfg, project, manifestDir)
	if err != nil {
		return BuildResult{}, err
	}
	moduleInfos, moduleBackend, moduleBackends, err := scanModules(ctx, cfg, project, tc, root, buildRoot, manifestDir)
	if err != nil {
		return BuildResult{}, err
	}
	options := plan.Options{Root: root, BuildDir: buildRoot, ManifestDir: manifestDir, Toolchain: tc, CUDA: cudaToolchain, Vulkan: vulkanSDK, Modules: moduleInfos, ModuleBackend: moduleBackend, ModuleBackends: moduleBackends}
	if memberBuildDir != "" {
		// Cross-member link inputs need the provider artifact paths, which the
		// toolchain above has now pinned down.
		options.WorkspaceOutputs = workspaceOutputPaths(ctx, tc)
	}
	buildPlan, err := plan.Build(cfg, project, options)
	if err != nil {
		return BuildResult{}, err
	}
	if err := translateWSLArguments(ctx, cfg.Toolchain.WSLDistribution, &buildPlan); err != nil {
		return BuildResult{}, err
	}
	manifest, err := ninja.Emit(buildPlan)
	if err != nil {
		return BuildResult{}, err
	}
	manifestPath := filepath.Join(manifestDir, "build.ninja")
	if _, err := plan.Outputs(cfg, project, options); err != nil {
		return BuildResult{}, err
	}
	if err := writeGenerationArtifacts(ctx, root, manifestDir, manifestPath, cfg, project, tc, buildPlan, manifest); err != nil {
		return BuildResult{}, err
	}
	outputs, err := plan.Outputs(cfg, project, options)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{ConfigPath: path, Manifest: manifestPath, Profile: cfg.Build.Profile, Outputs: outputs, RuntimeOutputs: buildPlan.RuntimeOutputs, Environment: tc.Env}, nil
}

type compileCommandEntry struct {
	Directory string   `json:"directory"`
	File      string   `json:"file"`
	Output    string   `json:"output,omitempty"`
	Arguments []string `json:"arguments"`
}

type commandMetadata struct {
	Version int                 `json:"version"`
	Project string              `json:"project"`
	Profile string              `json:"profile"`
	Targets map[string][]string `json:"targets"`
	Tests   map[string][]string `json:"tests"`
}

func writeCommandMetadata(ctx context.Context, root, manifestDir string, cfg config.Config, buildPlan plan.BuildPlan) error {
	lock, err := fsx.WaitLock(ctx, filepath.Join(root, ".trestle", "metadata"), "publish command metadata")
	if err != nil {
		return err
	}
	defer lock.Close()
	entries := compileCommandEntries(buildPlan, manifestDir)
	sort.Slice(entries, func(i, j int) bool { return entries[i].File < entries[j].File })
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := fsx.AtomicWrite(filepath.Join(manifestDir, "compile_commands.json"), data); err != nil {
		return err
	}
	compileCommands := cfg.Build.CompileCommands
	if compileCommands == "" {
		compileCommands = filepath.Join(root, "compile_commands.json")
	}
	compileLock, err := fsx.WaitLock(ctx, compileCommands, "publish compile commands")
	if err != nil {
		return err
	}
	defer compileLock.Close()
	if err := fsx.AtomicWrite(compileCommands, data); err != nil {
		return err
	}
	commands, err := json.MarshalIndent(commandMetadataFor(cfg), "", "  ")
	if err != nil {
		return err
	}
	if err := fsx.AtomicWrite(filepath.Join(manifestDir, ".trestle", "commands.json"), append(commands, '\n')); err != nil {
		return err
	}
	return fsx.AtomicWrite(filepath.Join(root, ".trestle", "commands.json"), append(commands, '\n'))
}

// compileCommandEntries builds the clang-style compile_commands.json entries for
// every compile action in the build plan, resolving each path against the
// manifest directory.
func compileCommandEntries(buildPlan plan.BuildPlan, manifestDir string) []compileCommandEntry {
	absolute := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		value, err := filepath.Abs(filepath.Join(manifestDir, filepath.FromSlash(path)))
		if err != nil {
			return filepath.Clean(path)
		}
		return value
	}
	entries := make([]compileCommandEntry, 0)
	for _, action := range buildPlan.Actions {
		if !strings.HasPrefix(string(action.ID), "compile_") || len(action.Inputs) == 0 || len(action.Outputs) == 0 {
			continue
		}
		arguments := append([]string{action.Command.Exe}, action.Command.Args...)
		entries = append(entries, compileCommandEntry{Directory: manifestDir, File: absolute(action.Inputs[0]), Output: absolute(action.Outputs[0]), Arguments: arguments})
	}
	return entries
}

// commandMetadataFor builds the project, target, and test command index written
// alongside compile_commands.json.
func commandMetadataFor(cfg config.Config) commandMetadata {
	metadata := commandMetadata{Version: 1, Project: cfg.Project.Name, Profile: cfg.Build.Profile, Targets: map[string][]string{}, Tests: map[string][]string{}}
	for _, name := range sortedTargetNames(cfg.Targets) {
		metadata.Targets[name] = []string{"trestle", "build", name}
		if cfg.Targets[name].Type == "test" {
			metadata.Tests[name] = []string{"trestle", "test", "-job", name}
		}
	}
	return metadata
}

func writeResponseFiles(ctx context.Context, cfg config.Config, manifestDir string, buildPlan plan.BuildPlan) error {
	for _, action := range buildPlan.Actions {
		if !action.ResponseFile || len(action.Outputs) != 1 {
			continue
		}
		paths := make([]string, 0, len(action.Inputs))
		for _, input := range action.Inputs {
			path := input
			if !filepath.IsAbs(path) {
				path = filepath.Join(manifestDir, filepath.FromSlash(path))
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if cfg.Toolchain.Mode == "wsl" {
				absolute, err = toolchain.WSLPath(ctx, cfg.Toolchain.WSLDistribution, absolute)
				if err != nil {
					return err
				}
			}
			paths = append(paths, "\""+filepath.ToSlash(filepath.Clean(absolute))+"\"")
		}
		responsePath := action.Outputs[0] + ".objs"
		if !filepath.IsAbs(responsePath) {
			responsePath = filepath.Join(manifestDir, filepath.FromSlash(responsePath))
		}
		if err := fsx.AtomicWrite(responsePath, []byte(strings.Join(paths, "\n")+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func writeGenerationState(manifestDir string, cfg config.Config, project model.ResolvedProject, tc toolchain.Toolchain) error {
	key := state.BuildKey(cfg, project, tc, fsx.Hash(cfg.Packages), "", "")
	generation := state.Generation{Key: key, Hash: key.Hash(), Sources: key.SourcePaths, Toolchain: tc}
	return state.Save(filepath.Join(manifestDir, ".trestle", "generation.json"), generation)
}

// Build generates and runs the build for the project's default targets.
func Build(path string) error {
	return BuildTargetsWithProgress(context.Background(), path, nil, false, func(line string) { fmt.Println(line) })
}

// BuildWithProgress is Build reporting progress through the callback.
func BuildWithProgress(ctx context.Context, path string, progress func(string)) error {
	return BuildTargetsWithProgress(ctx, path, nil, false, progress)
}

// BuildTargetsWithProgress generates and builds the selected targets, or every
// target when all is set.
func BuildTargetsWithProgress(ctx context.Context, path string, targets []string, all bool, progress func(string)) error {
	return BuildTargetsWithOptions(ctx, path, targets, all, false, progress)
}

func buildTargetsWithOptions(ctx context.Context, path string, targets []string, all, force bool, progress func(string)) error {
	cfg, err := operationConfig(ctx, path)
	if err != nil {
		return err
	}
	selected, err := selectBuildTargets(cfg, targets, all)
	ctx, release, lockErr := buildResources(ctx, cfg, "build")
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if err != nil {
		return err
	}
	if !force {
		// Manifest generation resolves sources and package metadata for the whole
		// project, even when Ninja will build only one requested goal.
		if err := policy.CheckSelected(policy.AssessAt(ctx, cfg, filepath.Dir(path)), sortedTargetNames(cfg.Targets)); err != nil {
			return err
		}
	} else if progress != nil {
		progress("Force build: target availability checks bypassed")
	}
	result, err := generateWithConfig(ctx, path, cfg)
	if err != nil {
		return err
	}
	goals := make([]string, 0, len(selected))
	if all {
		goals = append(goals, "all")
	} else {
		for _, name := range selected {
			output, ok := result.Outputs[model.TargetID(name)]
			if !ok {
				return fmt.Errorf("target %q has no build output", name)
			}
			goals = append(goals, filepath.ToSlash(output))
			goals = append(goals, result.RuntimeOutputs[model.TargetID(name)]...)
		}
	}
	requested := targetClosure(cfg, selected)
	writer := &progressWriter{callback: processx.Notify(ctx, progress), statusCallback: progress, outputs: result.Outputs, failedTargets: map[string]bool{}, diagnostics: map[string][]string{}}
	defer writer.Flush()
	args := []string{"-f", "build.ninja"}
	args = append(args, goals...)
	runErr := (runner.Runner{}).RunWithEvents(ctx, "ninja", args, runner.Options{
		Dir: filepath.Dir(result.Manifest), Env: environmentList(result.Environment), Stdout: writer, Stderr: writer, RawOutput: processx.Output(ctx),
	}, writer)
	writer.Flush()
	emitBuildSummary(progress, cfg, requested, writer, runErr)
	return runErr
}

func selectBuildTargets(cfg config.Config, targets []string, all bool) ([]string, error) {
	if all {
		return sortedTargetNames(cfg.Targets), nil
	}
	if len(targets) == 0 {
		targets = append([]string{}, cfg.Build.DefaultTargets...)
	}
	if len(targets) == 0 {
		for _, candidate := range []string{cfg.Project.Name, "app"} {
			if _, ok := cfg.Targets[candidate]; ok {
				targets = []string{candidate}
				break
			}
		}
	}
	if len(targets) == 0 {
		for _, name := range sortedTargetNames(cfg.Targets) {
			if target := cfg.Targets[name]; target.Type == "executable" && target.Type != "test" {
				targets = []string{name}
				break
			}
		}
	}
	if len(targets) == 0 {
		names := sortedTargetNames(cfg.Targets)
		targets = names[:1]
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(targets))
	for _, name := range targets {
		if _, ok := cfg.Targets[name]; !ok {
			return nil, fmt.Errorf("unknown build target %q", name)
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}

func targetClosure(cfg config.Config, roots []string) map[string]bool {
	result := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if result[name] {
			return
		}
		result[name] = true
		for _, dependency := range cfg.Targets[name].Dependencies {
			if dependency.Target != "" && dependency.Scope != "interface" {
				visit(dependency.Target)
			}
		}
	}
	for _, root := range roots {
		visit(root)
	}
	return result
}

func emitBuildSummary(progress func(string), cfg config.Config, requested map[string]bool, writer *progressWriter, runErr error) {
	if progress == nil {
		return
	}
	succeeded, failed, skipped := []string{}, []string{}, []string{}
	for _, name := range sortedTargetNames(cfg.Targets) {
		switch {
		case writer.failedTargets[name]:
			failed = append(failed, name)
		case !requested[name]:
			skipped = append(skipped, name)
		case runErr == nil:
			succeeded = append(succeeded, name)
		default:
			skipped = append(skipped, name)
		}
	}
	if len(writer.diagnostics) > 0 {
		progress("Diagnostics by target / stage:")
		keys := make([]string, 0, len(writer.diagnostics))
		for key := range writer.diagnostics {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			progress("  " + key)
			for _, line := range writer.diagnostics[key] {
				progress("    " + line)
			}
		}
	}
	progress("Build summary:")
	progress("  succeeded targets: " + listOrNone(succeeded))
	progress("  failed targets: " + listOrNone(failed))
	progress("  skipped targets: " + listOrNone(skipped))
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// TestJob describes one test target: its name, group, source files, run
// arguments, and working directory.
type TestJob struct {
	Name       string
	Group      string
	Files      []string
	Args       []string
	WorkingDir string
}

// TestSelection chooses which test jobs to run by job, group, or source file, or
// all of them when All is set.
type TestSelection struct {
	Job   string
	Group string
	File  string
	All   bool
}

// ListTests returns the test jobs declared by the project.
func ListTests(path string) ([]TestJob, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return listTestsConfig(cfg), nil
}

func listTestsConfig(cfg config.Config) []TestJob {
	names := sortedTargetNames(cfg.Targets)
	jobs := make([]TestJob, 0)
	for _, name := range names {
		target := cfg.Targets[name]
		if target.Type != "test" {
			continue
		}
		group := target.TestGroup
		if group == "" {
			group = "default"
		}
		jobs = append(jobs, TestJob{Name: name, Group: group, Files: append([]string{}, target.Sources...), Args: append([]string{}, target.TestArgs...), WorkingDir: target.TestWorkingDir})
	}
	return jobs
}

func runTests(ctx context.Context, path string, selection TestSelection, progress func(string)) error {
	cfg, err := operationConfig(ctx, path)
	if err != nil {
		return err
	}
	ctx, release, err := buildResources(ctx, cfg, "test")
	if err != nil {
		return err
	}
	defer release()
	ctx = context.WithValue(ctx, configurationKey{}, cfg)
	jobs := listTestsConfig(cfg)
	selected := selectTestJobs(jobs, selection)
	if len(selected) == 0 {
		return fmt.Errorf("no test jobs match the selection")
	}
	result, err := GenerateWithContext(ctx, path)
	if err != nil {
		return err
	}
	manifestDir := filepath.Dir(result.Manifest)
	environment := environmentList(result.Environment)
	writer := &progressWriter{callback: processx.Notify(ctx, progress), statusCallback: progress}
	defer writer.Flush()
	for index, job := range selected {
		if err := runTestJob(ctx, path, manifestDir, environment, writer, result, index, len(selected), job, progress); err != nil {
			return err
		}
	}
	if progress != nil {
		progress(fmt.Sprintf("Passed %d test job(s)", len(selected)))
	}
	return nil
}

// selectTestJobs returns the jobs a test selection matches: all of them when All
// is set, otherwise those matching the requested job, group, or source file.
func selectTestJobs(jobs []TestJob, selection TestSelection) []TestJob {
	selected := make([]TestJob, 0, len(jobs))
	for _, job := range jobs {
		matches := selection.All || (selection.Job != "" && job.Name == selection.Job) || (selection.Group != "" && job.Group == selection.Group)
		if selection.File != "" {
			wanted := filepath.Clean(selection.File)
			for _, source := range job.Files {
				if filepath.Clean(source) == wanted {
					matches = true
					break
				}
			}
		}
		if matches {
			selected = append(selected, job)
		}
	}
	return selected
}

// runTestJob builds and runs one test job, reporting its progress under the
// "[index/total]" prefix. index is zero-based.
func runTestJob(ctx context.Context, path, manifestDir string, environment []string, writer *progressWriter, result BuildResult, index, total int, job TestJob, progress func(string)) error {
	if progress != nil {
		progress(fmt.Sprintf("[%d/%d] Building test job %s", index+1, total, job.Name))
	}
	output, ok := result.Outputs[model.TargetID(job.Name)]
	if !ok {
		return fmt.Errorf("test job %q has no build output", job.Name)
	}
	if err := (runner.Runner{}).RunWithEvents(ctx, "ninja", []string{"-f", "build.ninja", filepath.ToSlash(output)}, runner.Options{Dir: manifestDir, Env: environment, Stdout: writer, Stderr: writer, RawOutput: processx.Output(ctx)}, writer); err != nil {
		return err
	}
	executable := output
	if !filepath.IsAbs(executable) {
		executable = filepath.Join(manifestDir, filepath.FromSlash(executable))
	}
	workingDir := job.WorkingDir
	if workingDir == "" {
		workingDir = filepath.Dir(path)
	}
	if progress != nil {
		progress(fmt.Sprintf("[%d/%d] Running %s · group %s", index+1, total, job.Name, job.Group))
	}
	command := processx.Command(ctx, executable, job.Args...)
	command.Dir = workingDir
	command.Env = append(os.Environ(), environment...)
	var processOutput io.Writer = writer
	if raw := processx.Output(ctx); raw != nil {
		processOutput = io.MultiWriter(raw, writer)
	}
	command.Stdout, command.Stderr = processOutput, processOutput
	if err := command.Run(); err != nil {
		return fmt.Errorf("test job %s failed: %w", job.Name, err)
	}
	if progress != nil {
		progress("PASS " + job.Name)
	}
	return nil
}

// Test runs every test job in the project.
func Test(path string) error {
	return RunTests(context.Background(), path, TestSelection{All: true}, func(line string) { fmt.Println(line) })
}

type progressWriter struct {
	mu             sync.Mutex
	buffer         bytes.Buffer
	callback       func(string)
	statusCallback func(string)
	outputs        map[model.TargetID]string
	failedTargets  map[string]bool
	diagnostics    map[string][]string
	currentTarget  string
	currentStage   string
	currentSource  string
}

var sourceDiagnosticPattern = regexp.MustCompile(`(?i)^([^:]+\.(?:c|cc|cpp|cxx|h|hpp))(?::|\()[0-9]+`)

// Write buffers incoming build output and forwards each complete line to the
// callback.
func (writer *progressWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	count, _ := writer.buffer.Write(data)
	for {
		line, err := writer.buffer.ReadBytes('\n')
		if err != nil {
			_, _ = writer.buffer.Write(line)
			break
		}
		writer.emit(line)
	}
	return count, nil
}

// Flush forwards any buffered output that was not terminated by a newline.
func (writer *progressWriter) Flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.buffer.Len() > 0 {
		writer.emit(writer.buffer.Next(writer.buffer.Len()))
	}
}

func (writer *progressWriter) emit(data []byte) {
	line := strings.TrimRight(decodeConsoleOutput(data), "\r\n")
	writer.observe(line)
	if writer.callback != nil {
		writer.callback(line)
	}
}

func (writer *progressWriter) observe(line string) {
	if line == "" || writer.failedTargets == nil {
		return
	}
	if strings.HasPrefix(line, "FAILED:") {
		failedPath := strings.TrimSpace(strings.TrimPrefix(line, "FAILED:"))
		if strings.HasPrefix(failedPath, "[") {
			if end := strings.Index(failedPath, "]"); end >= 0 {
				failedPath = strings.TrimSpace(failedPath[end+1:])
			}
		}
		writer.currentTarget, writer.currentStage, writer.currentSource = writer.classifyFailure(failedPath)
		if writer.currentTarget != "" {
			writer.failedTargets[writer.currentTarget] = true
		}
		return
	}
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "error") && !strings.Contains(lower, "undefined reference") && !strings.Contains(lower, "unresolved external") {
		return
	}
	source := writer.currentSource
	if match := sourceDiagnosticPattern.FindStringSubmatch(line); len(match) > 1 {
		source = filepath.Base(strings.TrimSpace(match[1]))
	}
	target := writer.currentTarget
	if target == "" {
		target = "unknown target"
	}
	stage := writer.currentStage
	if stage == "" {
		stage = "build"
	}
	key := target + " · " + stage
	if source != "" {
		key += " · " + source
	}
	writer.diagnostics[key] = append(writer.diagnostics[key], line)
}

func (writer *progressWriter) classifyFailure(path string) (target, stage, source string) {
	normalized := filepath.ToSlash(path)
	if marker := strings.Index("/"+strings.TrimPrefix(normalized, "./"), "/obj/"); marker >= 0 {
		withRoot := "/" + strings.TrimPrefix(normalized, "./")
		remainder := withRoot[marker+len("/obj/"):]
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 {
			target = parts[0]
			stage = "compile"
			if len(parts) > 1 {
				source = parts[len(parts)-1]
			}
			return
		}
	}
	for id, output := range writer.outputs {
		if filepath.Base(filepath.FromSlash(normalized)) == filepath.Base(filepath.FromSlash(output)) {
			target = string(id)
			stage = "link"
			return
		}
	}
	return "", "build", filepath.Base(filepath.FromSlash(normalized))
}

// Progress reports aggregate build progress to the status callback.
func (writer *progressWriter) Progress(value runner.Progress) {
	callback := writer.statusCallback
	if callback == nil {
		callback = writer.callback
	}
	if callback != nil {
		callback(fmt.Sprintf("Build %d/%d · %d%%", value.Finished, value.Total, value.Percent))
	}
}

// Output forwards a chunk of build output text.
func (writer *progressWriter) Output(_ string, text string) {
	_, _ = writer.Write([]byte(text))
}

func environmentList(environment map[string]string) []string {
	values := make([]string, 0, len(environment))
	for key, value := range environment {
		values = append(values, key+"="+value)
	}
	return values
}

func sortedTargetNames(targets map[string]config.Target) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func moduleArtifactName(source string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	base = strings.NewReplacer(" ", "_", ".", "_", "/", "_", "\\", "_").Replace(base)
	return base + "-" + fsx.Hash(filepath.Clean(source))[:10] + ".pcm"
}
