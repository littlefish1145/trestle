package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/fsx"
	"trestle/internal/graph"
	cmakeimport "trestle/internal/importer/cmake"
	xmakeimport "trestle/internal/importer/xmake"
	"trestle/internal/model"
	"trestle/internal/modules"
	"trestle/internal/modules/backends/clang"
	"trestle/internal/modules/p1689"
	"trestle/internal/ninja"
	"trestle/internal/plan"
	"trestle/internal/policy"
	"trestle/internal/runner"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/state"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

type BuildResult struct {
	ConfigPath     string
	Manifest       string
	Profile        string
	Outputs        map[model.TargetID]string
	RuntimeOutputs map[model.TargetID][]string
	Environment    map[string]string
}

func Init(root, name string) error {
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(root)
	}
	path := filepath.Join(root, config.DefaultFileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("E_CONFIG_EXISTS: %s already exists", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		return err
	}
	if err := config.Save(path, config.Default(name)); err != nil {
		return err
	}
	main := filepath.Join(root, "src", "main.cpp")
	if _, err := os.Stat(main); os.IsNotExist(err) {
		if err := os.WriteFile(main, []byte("int main() { return 0; }\n"), 0o644); err != nil {
			return err
		}
	}
	gitignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		if err := os.WriteFile(gitignore, []byte("build/\ndist/\n.trestle/\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func Configure(path, compiler, c, profile, vcpkgRoot, triplet, cudaRoot, cudaMode, setup, moduleScanner string, modulesEnabled bool) error {
	cfg, err := ensureProjectConfig(path)
	if err != nil {
		return err
	}
	if compiler != "" {
		cfg.Toolchain.CXX = compiler
	}
	if c != "" {
		cfg.Toolchain.C = c
	}
	if profile != "" {
		cfg.Build.Profile = profile
	}
	toolchainChanged := compiler != "" || setup != ""
	if setup != "" {
		cfg.Toolchain.Setup = setup
	}
	tc, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return err
	}
	if vcpkgRoot != "" {
		cfg.Vcpkg.Root = vcpkgRoot
	}
	if cfg.Vcpkg.Root == "" {
		cfg.Vcpkg.Root = os.Getenv("VCPKG_ROOT")
	}
	if triplet != "" {
		cfg.Vcpkg.Triplet = triplet
	}
	if cudaRoot != "" {
		cfg.Toolchain.CUDA = cudaRoot
	}
	if cudaMode != "" {
		cfg.Toolchain.CUDAMode = cudaMode
	}
	if moduleScanner != "" {
		cfg.Toolchain.ModuleScanner = moduleScanner
	}
	if modulesEnabled {
		cfg.Build.Modules = true
	}
	if cfg.Vcpkg.Root != "" && (cfg.Vcpkg.Triplet == "" || cfg.Vcpkg.Triplet == "auto") {
		available, err := vcpkg.AvailableTriplets(cfg.Vcpkg.Root)
		if err != nil {
			return err
		}
		selected, err := vcpkg.SelectAutoTriplet(vcpkg.TripletRequest{Compiler: string(tc.Kind), Available: available})
		if err != nil {
			return err
		}
		cfg.Vcpkg.Triplet = selected
	}
	if toolchainChanged {
		cfg.Toolchain.C = tc.CC
		cfg.Toolchain.Archiver = tc.Archiver
		cfg.Toolchain.Linker = tc.Linker
		if tc.Setup != "" {
			cfg.Toolchain.Setup = tc.Setup
		}
	}
	if cfg.Toolchain.C == "auto" || cfg.Toolchain.C == "" {
		cfg.Toolchain.C = tc.CC
	}
	if cfg.Toolchain.Archiver == "" || cfg.Toolchain.Archiver == "auto" || cfg.Toolchain.Archiver == "lib" {
		cfg.Toolchain.Archiver = tc.Archiver
	}
	if cfg.Toolchain.Linker == "" {
		cfg.Toolchain.Linker = tc.Linker
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Printf("toolchain: %s (%s)\n", tc.Kind, tc.Version)
	fmt.Printf("c: %s\ncxx: %s\narchiver: %s\n", cfg.Toolchain.C, cfg.Toolchain.CXX, cfg.Toolchain.Archiver)
	return nil
}

func withoutRunnerPath(candidate toolchain.Toolchain) toolchain.Toolchain {
	return candidate
}

func detectConfigured(ctx context.Context, cfg config.Config) (toolchain.Toolchain, error) {
	if cfg.Toolchain.Mode == "wsl" {
		return toolchain.DetectWSL(ctx, cfg.Toolchain.WSLDistribution, cfg.Toolchain.CXX)
	}
	detector := toolchain.NewDetector()
	setup := cfg.Toolchain.Setup
	if toolchain.NeedsMSVCEnvironment(ctx, cfg.Toolchain.CXX) {
		setup = toolchain.ResolveMSVCSetup(ctx, setup, cfg.Toolchain.Archiver, cfg.Toolchain.Linker, cfg.Toolchain.C, cfg.Toolchain.CXX)
	}
	if setup != "" {
		if setupDetector, ok := detector.(interface {
			DetectWithSetup(context.Context, string, string) (toolchain.Toolchain, error)
		}); ok {
			candidate, err := setupDetector.DetectWithSetup(ctx, cfg.Toolchain.CXX, setup)
			return withoutRunnerPath(candidate), err
		}
	}
	candidate, err := detector.Detect(ctx, cfg.Toolchain.CXX)
	return withoutRunnerPath(candidate), err
}

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

func SetCompileFlags(path, flags string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Build.CompileFlags = strings.Fields(flags)
	return config.Save(path, cfg)
}

func SetCompiler(path, compiler string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	compiler = strings.TrimSpace(compiler)
	if compiler == "" {
		return fmt.Errorf("compiler path is required")
	}
	cfg.Toolchain.CXX = compiler
	cfg.Toolchain.Mode = "native"
	cfg.Toolchain.WSLDistribution = ""
	if toolchain.NeedsMSVCEnvironment(context.Background(), compiler) {
		cfg.Toolchain.Setup = toolchain.ResolveMSVCSetup(context.Background(), cfg.Toolchain.Setup, cfg.Toolchain.Archiver, cfg.Toolchain.Linker, cfg.Toolchain.C, compiler)
	} else {
		cfg.Toolchain.Setup = ""
	}
	tc, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return err
	}
	cfg.Toolchain.CXX = tc.CXX
	cfg.Toolchain.C = tc.CC
	cfg.Toolchain.Archiver = tc.Archiver
	cfg.Toolchain.Linker = tc.Linker
	cfg.Toolchain.Setup = tc.Setup
	return config.Save(path, cfg)
}

func SetWSLCompiler(path, distribution, compiler string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Toolchain.Mode = "wsl"
	cfg.Toolchain.WSLDistribution = strings.TrimSpace(distribution)
	cfg.Toolchain.CXX = strings.TrimSpace(compiler)
	cfg.Toolchain.Setup = ""
	tc, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return err
	}
	cfg.Toolchain.C = tc.CC
	cfg.Toolchain.Archiver = tc.Archiver
	cfg.Toolchain.Linker = tc.Linker
	return config.Save(path, cfg)
}

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

func SetCUDA(path string, enabled bool, root string) error {
	return SetCUDAConnection(path, enabled, root, "native", "")
}

func SetCUDAConnection(configPath string, enabled bool, root, execution, distribution string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !enabled {
		cfg.Toolchain.CUDA = ""
		cfg.Toolchain.CUDAExecution = "native"
		cfg.Toolchain.CUDAWSLDistribution = ""
		return config.Save(configPath, cfg)
	}
	root = strings.TrimSpace(root)
	execution = strings.ToLower(strings.TrimSpace(execution))
	if execution == "" {
		execution = "native"
	}
	distribution = strings.TrimSpace(distribution)
	if execution == "wsl" {
		if cfg.Toolchain.Mode != "wsl" {
			return fmt.Errorf("E_CUDA_WSL_HOST: select a WSL C/C++ compiler before connecting WSL CUDA")
		}
		if distribution == "" {
			distribution = cfg.Toolchain.WSLDistribution
		}
		if cfg.Toolchain.WSLDistribution != "" && distribution != "" && !strings.EqualFold(cfg.Toolchain.WSLDistribution, distribution) {
			return fmt.Errorf("E_CUDA_WSL_DISTRIBUTION: CUDA is in %s but the compiler is in %s", distribution, cfg.Toolchain.WSLDistribution)
		}
		host, detectErr := detectConfigured(context.Background(), cfg)
		if detectErr != nil {
			return detectErr
		}
		detected, detectErr := cuda.DetectWSL(context.Background(), distribution, root, host)
		if detectErr != nil {
			return detectErr
		}
		root = detected.Toolkit.Root
	} else if execution == "native" {
		if root == "" {
			root = os.Getenv("CUDA_PATH")
		}
		if root == "" {
			root = os.Getenv("CUDA_HOME")
		}
		if root == "" {
			return fmt.Errorf("CUDA toolkit root is required")
		}
		nvcc := filepath.Join(root, "bin", "nvcc")
		if _, statErr := os.Stat(nvcc); statErr != nil {
			if _, windowsErr := os.Stat(nvcc + ".exe"); windowsErr != nil {
				return fmt.Errorf("CUDA compiler was not found under %s", root)
			}
		}
	} else {
		return fmt.Errorf("CUDA execution mode %q is unsupported", execution)
	}
	cfg.Toolchain.CUDA = root
	cfg.Toolchain.CUDAExecution = execution
	cfg.Toolchain.CUDAWSLDistribution = distribution
	return config.Save(configPath, cfg)
}

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

func ImportCMake(ctx context.Context, path string, progress func(string)) (cmakeimport.Result, error) {
	options := cmakeimport.Options{}
	if cfg, err := config.Load(path); err == nil {
		if selected, detectErr := (toolchain.DetectorImpl{}).DetectWithSetup(ctx, cfg.Toolchain.CXX, cfg.Toolchain.Setup); detectErr == nil {
			options.CCompiler = selected.CC
			if cfg.Toolchain.C != "" && cfg.Toolchain.C != "auto" {
				options.CCompiler = cfg.Toolchain.C
			}
			options.CXXCompiler = selected.CXX
			options.Environment = selected.Env
		}
	}
	return cmakeimport.ImportWithOptions(ctx, filepath.Dir(path), path, progress, options)
}

func ImportXmake(ctx context.Context, path string, progress func(string)) error {
	return xmakeimport.Import(ctx, filepath.Dir(path), path, progress)
}

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

func SetProjectSetting(path, key, value string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	values := strings.Fields(value)
	csv := func() []string {
		var out []string
		for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	switch key {
	case "project.name":
		cfg.Project.Name = value
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
		cfg.Build.DefaultTargets = csv()
	case "build.compile_commands":
		cfg.Build.CompileCommands = value
	case "toolchain.c":
		cfg.Toolchain.C = value
	case "toolchain.cxx":
		cfg.Toolchain.CXX = value
	case "toolchain.archiver":
		cfg.Toolchain.Archiver = value
	case "toolchain.linker":
		cfg.Toolchain.Linker = value
	case "toolchain.setup":
		cfg.Toolchain.Setup = value
	case "toolchain.cuda":
		cfg.Toolchain.CUDA = value
	case "toolchain.cuda_mode":
		cfg.Toolchain.CUDAMode = value
	case "toolchain.cuda_architectures":
		cfg.Toolchain.CUDAArchitectures = csv()
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
	case "vcpkg.root":
		cfg.Vcpkg.Root = value
	case "vcpkg.triplet":
		cfg.Vcpkg.Triplet = value
	case "vcpkg.crt_linkage":
		cfg.Vcpkg.CRTLinkage = value
	case "vcpkg.library_linkage":
		cfg.Vcpkg.LibraryLinkage = value
	case "package.output":
		cfg.Package.Output = value
	default:
		if strings.HasPrefix(key, "target:") {
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
			switch field {
			case "type":
				target.Type = value
			case "sources":
				target.Sources = csv()
			case "output_name":
				target.OutputName = value
			case "c_standard":
				target.CStandard = value
			case "cxx_standard":
				target.CXXStandard = value
			case "include_dirs":
				target.IncludeDirs = csv()
			case "private_include_dirs":
				target.PrivateIncludeDirs = csv()
			case "defines":
				target.Defines = csv()
			case "private_defines":
				target.PrivateDefines = csv()
			case "compile_options":
				target.CompileOptions = values
			case "library_dirs":
				target.LibraryDirs = csv()
			case "libraries":
				target.Libraries = csv()
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
		} else if strings.HasPrefix(key, "preset:") {
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
		} else {
			return fmt.Errorf("setting %q is not editable", key)
		}
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	return config.Save(path, cfg)
}

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
	filter := func(flags []string) []string {
		var out []string
		for _, flag := range flags {
			lower := strings.ToLower(flag)
			if strings.HasPrefix(lower, "/o") || strings.HasPrefix(lower, "-o") || lower == "/gl" || lower == "-flto" || lower == "/ltcg" {
				continue
			}
			out = append(out, flag)
		}
		return out
	}
	cfg.Build.CompileFlags = filter(cfg.Build.CompileFlags)
	cfg.Build.LinkFlags = filter(cfg.Build.LinkFlags)
	switch name {
	case "balanced":
		if msvc {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "/O2", "/DNDEBUG")
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, "/OPT:REF", "/OPT:ICF")
		} else {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "-O2", "-DNDEBUG")
		}
	case "speed":
		if msvc {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "/O2", "/Ob3", "/GL", "/DNDEBUG")
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, "/LTCG", "/OPT:REF", "/OPT:ICF")
		} else {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "-O3", "-flto", "-DNDEBUG")
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, "-flto")
		}
	case "size":
		if msvc {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "/O1", "/GL", "/DNDEBUG")
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, "/LTCG", "/OPT:REF", "/OPT:ICF")
		} else {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, "-Os", "-ffunction-sections", "-fdata-sections", "-DNDEBUG")
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, "-Wl,--gc-sections")
		}
	default:
		return fmt.Errorf("unknown release optimization %q", name)
	}
	return nil
}

func ReleaseWithProgress(ctx context.Context, path string, progress func(string)) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := applyReleaseOptimization(&cfg); err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
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
	return packageSelected(path, targets, progress)
}

func AddPackage(path, name, targetName string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
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
	if targetName == "" {
		targetName = packageTarget(cfg, name)
	}
	target, ok := cfg.Targets[targetName]
	if !ok {
		return fmt.Errorf("target %q not found", targetName)
	}
	if _, exists := cfg.Packages[name]; !exists {
		cfg.Packages[name] = config.Package{Provider: "vcpkg", Port: name, Version: version, Features: features, Triplet: cfg.Vcpkg.Triplet}
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
	for _, dependency := range target.Dependencies {
		if dependency.Package == name {
			return nil
		}
	}
	target.Dependencies = append(target.Dependencies, config.Dependency{Package: name, Scope: "private"})
	cfg.Targets[targetName] = target
	return config.Save(path, cfg)
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

func SearchPackages(ctx context.Context, path, query string) ([]vcpkg.Port, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	registry := vcpkg.WebRegistry{CachePath: filepath.Join(filepath.Dir(path), ".trestle", "cache", "vcpkg-index.json")}
	client, clientErr := vcpkg.NewClient(cfg.Vcpkg.Root)
	if ports, cached := registry.SearchCached(query, 50); cached {
		if clientErr == nil {
			for index := range ports {
				ports[index].Installed = client.IsInstalled(ports[index].Name)
			}
		}
		return ports, nil
	}
	if clientErr == nil {
		ports, localErr := client.SearchPorts(ctx, query)
		if localErr == nil {
			go func() {
				refreshCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_ = registry.Refresh(refreshCtx)
			}()
			return ports, nil
		}
	}
	ports, webErr := registry.Search(ctx, query, 50)
	if webErr != nil {
		return nil, fmt.Errorf("online search failed (%v); local fallback unavailable (%v)", webErr, clientErr)
	}
	return ports, nil
}

// InstallPackage downloads and builds a package using the configured vcpkg
// instance, then records it as a dependency of the project's default target.
func InstallPackage(ctx context.Context, path, name string, progress func(string)) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("package name is required")
	}
	client, err := vcpkg.NewClient(cfg.Vcpkg.Root)
	if err != nil {
		return err
	}
	client.Environment = toolchain.EnvironmentForVcpkg(ctx, cfg.Toolchain.Setup, cfg.Toolchain.Archiver, cfg.Toolchain.CXX, cfg.Toolchain.C)
	triplet := cfg.Vcpkg.Triplet
	if triplet == "" || triplet == "auto" {
		triplet = vcpkg.DefaultTriplet()
	}
	if err := client.InstallWithProgress(ctx, []string{name}, triplet, progress); err != nil {
		return err
	}
	if cfg.Vcpkg.Root != client.Root || cfg.Vcpkg.Triplet != triplet {
		cfg.Vcpkg.Root, cfg.Vcpkg.Triplet = client.Root, triplet
		if err := config.Save(path, cfg); err != nil {
			return err
		}
	}
	return AddPackage(path, name, "")
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

func Vcpkg(ctx context.Context, root, query string, ports []string, triplet string, install bool) error {
	if install {
		client, err := vcpkg.NewClient(root)
		if err != nil {
			return err
		}
		return client.Install(ctx, ports, triplet)
	}
	results, err := (vcpkg.WebRegistry{}).Search(ctx, query, 50)
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Printf("%-24s %-14s %s\n", result.Name, result.Version, result.Description)
	}
	return nil
}

func Toolchains(ctx context.Context) error {
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
	return nil
}

func Generate(path string) (BuildResult, error) {
	return GenerateWithContext(context.Background(), path)
}

func GenerateWithContext(ctx context.Context, path string) (BuildResult, error) {
	cfg, err := ensureProjectConfig(path)
	if err != nil {
		return BuildResult{}, err
	}
	cfg, err = policy.Apply(ctx, cfg)
	if err != nil {
		return BuildResult{}, err
	}
	return generateWithConfig(ctx, path, cfg)
}

func generateWithConfig(ctx context.Context, path string, cfg config.Config) (BuildResult, error) {
	project, err := graph.Resolve(cfg)
	if err != nil {
		return BuildResult{}, err
	}
	hasNonShader := false
	for _, target := range project.Targets {
		if target.Type != model.Shader {
			hasNonShader = true
		}
	}
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return BuildResult{}, err
	}
	buildRoot, err := filepath.Abs(cfg.Build.BuildDir)
	if err != nil {
		return BuildResult{}, err
	}
	manifestDir := filepath.Join(buildRoot, cfg.Build.Profile)
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
	}
	if cfg.Toolchain.C != "" && cfg.Toolchain.C != "auto" {
		tc.CC = cfg.Toolchain.C
	}
	if cfg.Toolchain.Archiver != "" && cfg.Toolchain.Archiver != "auto" && cfg.Toolchain.Archiver != "lib" {
		tc.Archiver = cfg.Toolchain.Archiver
	}
	if cfg.Toolchain.Linker != "" && cfg.Toolchain.Linker != "auto" {
		tc.Linker = cfg.Toolchain.Linker
	}
	if cfg.Toolchain.Mode == "wsl" && hasNonShader {
		linuxDir, pathErr := toolchain.WSLPath(ctx, cfg.Toolchain.WSLDistribution, manifestDir)
		if pathErr != nil {
			return BuildResult{}, pathErr
		}
		tc = toolchain.WithWSLDirectory(tc, linuxDir)
	}
	if tc.Kind == toolchain.MSVC {
		linkage := strings.ToLower(cfg.Vcpkg.CRTLinkage)
		if linkage == "" && strings.Contains(strings.ToLower(cfg.Vcpkg.Triplet), "static") && !strings.Contains(strings.ToLower(cfg.Vcpkg.Triplet), "static-md") {
			linkage = "static"
		}
		if linkage == "static" || linkage == "dynamic" {
			flag := "/MD"
			if linkage == "static" {
				flag = "/MT"
			}
			if cfg.Build.Profile == "debug" {
				flag += "d"
			}
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, flag)
		}
	}
	var cudaToolchain *cuda.Toolchain
	if cfg.Toolchain.CUDA != "" {
		var detected cuda.Toolchain
		var detectErr error
		if cfg.Toolchain.CUDAExecution == "wsl" {
			if cfg.Toolchain.Mode != "wsl" {
				return BuildResult{}, fmt.Errorf("E_CUDA_WSL_HOST: WSL CUDA requires a WSL C/C++ toolchain")
			}
			distribution := cfg.Toolchain.CUDAWSLDistribution
			if distribution == "" {
				distribution = cfg.Toolchain.WSLDistribution
			}
			if cfg.Toolchain.WSLDistribution != "" && distribution != "" && !strings.EqualFold(cfg.Toolchain.WSLDistribution, distribution) {
				return BuildResult{}, fmt.Errorf("E_CUDA_WSL_DISTRIBUTION: CUDA is in %s but the compiler is in %s", distribution, cfg.Toolchain.WSLDistribution)
			}
			detected, detectErr = cuda.DetectWSL(context.Background(), distribution, cfg.Toolchain.CUDA, tc)
		} else {
			if tc.Runner != "" {
				return BuildResult{}, fmt.Errorf("E_CUDA_NATIVE_HOST: native CUDA cannot use a WSL host compiler; connect CUDA from the same WSL distribution")
			}
			detected, detectErr = cuda.DetectWithHost(context.Background(), cfg.Toolchain.CUDA, tc)
		}
		if detectErr != nil {
			return BuildResult{}, detectErr
		}
		detected.Architectures = append([]string{}, cfg.Toolchain.CUDAArchitectures...)
		if err := cuda.ProbeHost(context.Background(), detected); err != nil {
			return BuildResult{}, err
		}
		if cfg.Toolchain.CUDAMode == "rdc" || cfg.Toolchain.CUDAMode == "separate" {
			detected.Mode = cuda.SeparateCompilation
		}
		cudaToolchain = &detected
	}
	var vulkanSDK *vulkan.SDK
	for _, target := range project.Targets {
		if target.Type == model.Shader {
			var detected vulkan.SDK
			var detectErr error
			if cfg.Toolchain.VulkanExecution == "wsl" {
				distribution := cfg.Toolchain.VulkanWSLDistribution
				if distribution == "" {
					distribution = cfg.Toolchain.WSLDistribution
				}
				detected, detectErr = vulkan.DetectWSL(context.Background(), distribution, cfg.Toolchain.Vulkan)
				if detectErr == nil {
					linuxDir, pathErr := toolchain.WSLPath(context.Background(), distribution, manifestDir)
					if pathErr != nil {
						return BuildResult{}, pathErr
					}
					detected = vulkan.WithWSLDirectory(detected, linuxDir)
				}
			} else if cfg.Toolchain.Vulkan != "" {
				detected, detectErr = vulkan.DetectRoot(cfg.Toolchain.Vulkan)
			} else {
				detected, detectErr = vulkan.Detect()
			}
			if detectErr != nil {
				return BuildResult{}, detectErr
			}
			vulkanSDK = &detected
			break
		}
	}
	moduleInfos := map[string]plan.ModuleInfo{}
	var moduleBackend modules.Support
	moduleBackends := map[string]modules.Support{}
	if cfg.Build.Modules {
		if tc.Kind != toolchain.Clang {
			return BuildResult{}, fmt.Errorf("E_MODULE_BACKEND_UNSUPPORTED: modules currently require a Clang toolchain")
		}
		scannerPath := cfg.Toolchain.ModuleScanner
		if scannerPath == "" {
			scannerPath = os.Getenv("CLANG_SCAN_DEPS")
		}
		if scannerPath == "" {
			if tc.Runner != "" {
				scannerPath, err = toolchain.WSLExecutable(ctx, cfg.Toolchain.WSLDistribution, "clang-scan-deps")
			} else {
				scannerPath, err = exec.LookPath("clang-scan-deps")
			}
			if err != nil {
				return BuildResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: clang-scan-deps was not found: %w", err)
			}
		}
		if tc.Runner != "" && filepath.VolumeName(scannerPath) != "" {
			return BuildResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: WSL requires a Linux clang-scan-deps path, got %q", scannerPath)
		}
		documents := make([]p1689.Document, 0)
		bySource := make(map[string]p1689.Document)
		scanKeysBySource := make(map[string]modules.ScanKey)
		headerDepsBySource := make(map[string][]string)
		for _, target := range project.Targets {
			for _, source := range target.Sources {
				if moduleScannableSource(source) {
					standard := cfg.Build.CXXStandard
					if target.CXXStandard != "" {
						standard = target.CXXStandard
					}
					options := append(append(append([]string{}, cfg.Build.CompileFlags...), cfg.Build.CXXFlags...), target.CompileSelf.Options...)
					if target.Type == model.SharedLibrary && !hasPICOption(options) {
						options = append(options, "-fPIC")
					}
					localDirs := make([]string, 0, len(target.CompileSelf.IncludeDirs))
					for _, include := range target.CompileSelf.IncludeDirs {
						absolute := include
						if !filepath.IsAbs(absolute) {
							absolute = filepath.Join(root, absolute)
						}
						localDirs = append(localDirs, absolute)
						relative, relErr := filepath.Rel(manifestDir, absolute)
						if relErr != nil {
							relative = absolute
						}
						options = append(options, "-I"+filepath.ToSlash(relative))
					}
					for _, define := range target.CompileSelf.Defines {
						options = append(options, "-D"+define)
					}
					localOptions := append([]string{}, options...)
					if tc.Runner != "" {
						options, err = mapWSLModuleOptions(ctx, cfg.Toolchain.WSLDistribution, manifestDir, options)
						if err != nil {
							return BuildResult{}, err
						}
					}
					scanner := modules.CommandScanner{Compiler: tc.CXX, Scanner: scannerPath, Standard: standard, Options: options, Cache: modules.NewCache(manifestDir), Directory: manifestDir, Runner: tc.Runner, RunnerArgs: tc.RunnerArgs, HeaderDirs: localDirs, LocalOptions: localOptions}
					if tc.Runner != "" {
						scanner.PathMapper = func(ctx context.Context, path string) (string, error) {
							return toolchain.WSLPath(ctx, cfg.Toolchain.WSLDistribution, path)
						}
						scanner.DependencyMapper = func(ctx context.Context, path string) (string, error) {
							return toolchain.WSLWindowsPath(ctx, cfg.Toolchain.WSLDistribution, path)
						}
					}
					scan, err := scanner.Scan(ctx, source)
					if err != nil {
						return BuildResult{}, err
					}
					if previous, exists := scanKeysBySource[source]; exists && previous != scan.Key && (hasModuleEdges(bySource[source]) || hasModuleEdges(scan.Document)) {
						return BuildResult{}, fmt.Errorf("E_MODULE_SCAN_CONFLICT: %s is used by multiple targets with different module compile options", source)
					}
					moduleBackends[source] = clang.Backend{Compiler: tc.CXX, Scanner: scannerPath, Standard: standard, Options: options, Runner: tc.Runner, RunnerArgs: tc.RunnerArgs}
					if _, exists := scanKeysBySource[source]; !exists {
						documents = append(documents, scan.Document)
					}
					scanKeysBySource[source] = scan.Key
					bySource[source] = scan.Document
					headerDepsBySource[source] = scan.HeaderDeps
					if !scan.Complete {
						stamp := filepath.Join(manifestDir, "modules", "stamps", moduleArtifactName(source)+".stamp")
						if err := fsx.AtomicWrite(stamp, []byte(time.Now().UTC().Format(time.RFC3339Nano))); err != nil {
							return BuildResult{}, err
						}
						headerDepsBySource[source] = append(headerDepsBySource[source], stamp)
					}
				}
			}
		}
		if _, err := modules.BuildGraph(documents); err != nil {
			return BuildResult{}, err
		}
		providerArtifacts := make(map[string]string)
		artifactFor := func(source string) string {
			return filepath.Join(buildRoot, cfg.Build.Profile, "bmi", moduleArtifactName(source))
		}
		for source, document := range bySource {
			for _, rule := range document.Rules {
				for _, provided := range rule.Provides {
					artifact, relErr := filepath.Rel(manifestDir, artifactFor(source))
					if relErr != nil {
						return BuildResult{}, relErr
					}
					providerArtifacts[provided.Key().String()] = filepath.ToSlash(artifact)
				}
			}
		}
		for source, document := range bySource {
			info := plan.ModuleInfo{HeaderDeps: headerDepsBySource[source]}
			for _, rule := range document.Rules {
				for _, provided := range rule.Provides {
					info.Artifact = artifactFor(source)
					info.Provides = append(info.Provides, provided.LogicalName)
				}
				for _, required := range rule.Requires {
					artifact, ok := providerArtifacts[required.Key().String()]
					if !ok {
						return BuildResult{}, fmt.Errorf("E_MODULE_MISSING_PROVIDER: %s", required.LogicalName)
					}
					info.Requires = append(info.Requires, modules.Reference{LogicalName: required.LogicalName, Path: artifact})
				}
			}
			if len(info.Provides) > 0 || len(info.Requires) > 0 {
				moduleInfos[source] = info
			}
		}
		moduleBackend = clang.Backend{Compiler: tc.CXX, Scanner: scannerPath, Standard: cfg.Build.CXXStandard, Runner: tc.Runner, RunnerArgs: tc.RunnerArgs}
	}
	options := plan.Options{Root: root, BuildDir: buildRoot, Toolchain: tc, CUDA: cudaToolchain, Vulkan: vulkanSDK, Modules: moduleInfos, ModuleBackend: moduleBackend, ModuleBackends: moduleBackends}
	buildPlan, err := plan.Build(cfg, project, options)
	if err != nil {
		return BuildResult{}, err
	}
	if err := writeResponseFiles(manifestDir, buildPlan); err != nil {
		return BuildResult{}, err
	}
	manifest, err := ninja.Emit(buildPlan)
	if err != nil {
		return BuildResult{}, err
	}
	manifestPath := filepath.Join(manifestDir, "build.ninja")
	if err := fsx.AtomicWrite(manifestPath, manifest); err != nil {
		return BuildResult{}, err
	}
	if err := writeCommandMetadata(root, manifestDir, cfg, buildPlan); err != nil {
		return BuildResult{}, err
	}
	if err := writeGenerationState(manifestDir, cfg, project, tc); err != nil {
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

func writeCommandMetadata(root, manifestDir string, cfg config.Config, buildPlan plan.BuildPlan) error {
	entries := make([]compileCommandEntry, 0)
	for _, action := range buildPlan.Actions {
		if !strings.HasPrefix(string(action.ID), "compile_") || len(action.Inputs) == 0 || len(action.Outputs) == 0 {
			continue
		}
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
		arguments := append([]string{action.Command.Exe}, action.Command.Args...)
		entries = append(entries, compileCommandEntry{Directory: manifestDir, File: absolute(action.Inputs[0]), Output: absolute(action.Outputs[0]), Arguments: arguments})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].File < entries[j].File })
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	compileCommands := cfg.Build.CompileCommands
	if compileCommands == "" {
		compileCommands = filepath.Join(root, "compile_commands.json")
	}
	if err := fsx.AtomicWrite(compileCommands, data); err != nil {
		return err
	}
	metadata := commandMetadata{Version: 1, Project: cfg.Project.Name, Profile: cfg.Build.Profile, Targets: map[string][]string{}, Tests: map[string][]string{}}
	for _, name := range sortedTargetNames(cfg.Targets) {
		metadata.Targets[name] = []string{"trestle", "build", name}
		if cfg.Targets[name].Type == "test" {
			metadata.Tests[name] = []string{"trestle", "test", "-job", name}
		}
	}
	commands, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(filepath.Join(root, ".trestle", "commands.json"), append(commands, '\n'))
}

func writeResponseFiles(manifestDir string, buildPlan plan.BuildPlan) error {
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
			paths = append(paths, filepath.ToSlash(filepath.Clean(absolute)))
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

func Build(path string) error {
	return BuildTargetsWithProgress(context.Background(), path, nil, false, func(line string) { fmt.Println(line) })
}

func BuildWithProgress(ctx context.Context, path string, progress func(string)) error {
	return BuildTargetsWithProgress(ctx, path, nil, false, progress)
}

func BuildTargetsWithProgress(ctx context.Context, path string, targets []string, all bool, progress func(string)) error {
	return BuildTargetsWithOptions(ctx, path, targets, all, false, progress)
}

func BuildTargetsWithOptions(ctx context.Context, path string, targets []string, all, force bool, progress func(string)) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg, err = policy.Apply(ctx, cfg)
	if err != nil {
		return err
	}
	selected, err := selectBuildTargets(cfg, targets, all)
	if err != nil {
		return err
	}
	if !force {
		if err := policy.CheckSelected(policy.Assess(ctx, cfg), selected); err != nil {
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
	writer := &progressWriter{callback: progress, outputs: result.Outputs, failedTargets: map[string]bool{}, diagnostics: map[string][]string{}}
	defer writer.Flush()
	args := []string{"-f", "build.ninja"}
	args = append(args, goals...)
	runErr := (runner.Runner{}).RunWithEvents(ctx, "ninja", args, runner.Options{
		Dir: filepath.Dir(result.Manifest), Env: environmentList(result.Environment), Stdout: writer, Stderr: writer,
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

type TestJob struct {
	Name       string
	Group      string
	Files      []string
	Args       []string
	WorkingDir string
}

type TestSelection struct {
	Job   string
	Group string
	File  string
	All   bool
}

func ListTests(path string) ([]TestJob, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
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
	return jobs, nil
}

func RunTests(ctx context.Context, path string, selection TestSelection, progress func(string)) error {
	jobs, err := ListTests(path)
	if err != nil {
		return err
	}
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
	if len(selected) == 0 {
		return fmt.Errorf("no test jobs match the selection")
	}
	result, err := Generate(path)
	if err != nil {
		return err
	}
	manifestDir := filepath.Dir(result.Manifest)
	environment := environmentList(result.Environment)
	writer := &progressWriter{callback: progress}
	for index, job := range selected {
		if progress != nil {
			progress(fmt.Sprintf("[%d/%d] Building test job %s", index+1, len(selected), job.Name))
		}
		output, ok := result.Outputs[model.TargetID(job.Name)]
		if !ok {
			return fmt.Errorf("test job %q has no build output", job.Name)
		}
		if err := (runner.Runner{}).RunWithEvents(ctx, "ninja", []string{"-f", "build.ninja", filepath.ToSlash(output)}, runner.Options{Dir: manifestDir, Env: environment, Stdout: writer, Stderr: writer}, writer); err != nil {
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
			progress(fmt.Sprintf("[%d/%d] Running %s · group %s", index+1, len(selected), job.Name, job.Group))
		}
		command := exec.CommandContext(ctx, executable, job.Args...)
		command.Dir = workingDir
		command.Env = append(os.Environ(), environment...)
		command.Stdout, command.Stderr = writer, writer
		if err := command.Run(); err != nil {
			return fmt.Errorf("test job %s failed: %w", job.Name, err)
		}
		if progress != nil {
			progress("PASS " + job.Name)
		}
	}
	if progress != nil {
		progress(fmt.Sprintf("Passed %d test job(s)", len(selected)))
	}
	return nil
}

func Test(path string) error {
	return RunTests(context.Background(), path, TestSelection{All: true}, func(line string) { fmt.Println(line) })
}

type progressWriter struct {
	mu            sync.Mutex
	buffer        bytes.Buffer
	callback      func(string)
	outputs       map[model.TargetID]string
	failedTargets map[string]bool
	diagnostics   map[string][]string
	currentTarget string
	currentStage  string
	currentSource string
}

var sourceDiagnosticPattern = regexp.MustCompile(`(?i)^([^:]+\.(?:c|cc|cpp|cxx|h|hpp))(?::|\()[0-9]+`)

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
	if writer.callback != nil && line != "" {
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

func (writer *progressWriter) Progress(value runner.Progress) {
	if writer.callback != nil {
		writer.callback(fmt.Sprintf("Build %d/%d · %d%%", value.Finished, value.Total, value.Percent))
	}
}

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
