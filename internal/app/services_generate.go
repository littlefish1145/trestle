package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/model"
	"trestle/internal/modules"
	"trestle/internal/modules/backends/clang"
	"trestle/internal/modules/p1689"
	"trestle/internal/plan"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

// projectHasNonShader reports whether the resolved project holds at least one
// target that is not a shader, i.e. a target that needs a C/C++ toolchain.
func projectHasNonShader(project model.ResolvedProject) bool {
	for _, target := range project.Targets {
		if target.Type != model.Shader {
			return true
		}
	}
	return false
}

// applyConfiguredExecutables overrides the detected C compiler, archiver, and
// linker with the explicit selectors stored in the configuration, resolving each
// relative to the project root. Selectors left at "auto" keep the detected
// value.
func applyConfiguredExecutables(ctx context.Context, cfg config.Config, tc toolchain.Toolchain, root string) (toolchain.Toolchain, error) {
	for _, item := range []struct {
		value, role string
		output      *string
	}{
		{cfg.Toolchain.C, "C compiler", &tc.CC},
		{cfg.Toolchain.Archiver, "archiver", &tc.Archiver},
		{cfg.Toolchain.Linker, "linker", &tc.Linker},
	} {
		if item.value == "" || item.value == "auto" {
			continue
		}
		resolved, err := configuredExecutable(ctx, cfg, tc, root, item.value, item.role)
		if err != nil {
			return toolchain.Toolchain{}, err
		}
		*item.output = resolved
	}
	return tc, nil
}

// applyMSVCRuntimeFlag appends the CRT linkage flag (/MT or /MD, with the debug
// suffix under a debug profile) that the configured vcpkg triplet and linkage
// imply, when an MSVC toolchain is in use.
func applyMSVCRuntimeFlag(cfg config.Config, tc toolchain.Toolchain) config.Config {
	if tc.Kind != toolchain.MSVC {
		return cfg
	}
	linkage := strings.ToLower(cfg.Vcpkg.CRTLinkage)
	if linkage == "" && strings.Contains(strings.ToLower(cfg.Vcpkg.Triplet), "static") && !strings.Contains(strings.ToLower(cfg.Vcpkg.Triplet), "static-md") {
		linkage = "static"
	}
	if linkage != "static" && linkage != "dynamic" {
		return cfg
	}
	flag := "/MD"
	if linkage == "static" {
		flag = "/MT"
	}
	if cfg.Build.Profile == "debug" {
		flag += "d"
	}
	cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, flag)
	return cfg
}

// detectCUDAToolchain resolves the CUDA toolkit the project configured, in the
// requested execution environment, probes it against the host toolchain, and
// applies the requested compilation mode. It returns nil when no CUDA toolkit is
// configured.
func detectCUDAToolchain(ctx context.Context, cfg config.Config, tc toolchain.Toolchain) (*cuda.Toolchain, error) {
	if cfg.Toolchain.CUDA == "" && cfg.Toolchain.CUDARoot == "" {
		return nil, nil
	}
	var detected cuda.Toolchain
	var detectErr error
	if cfg.Toolchain.CUDAExecution == "wsl" {
		if cfg.Toolchain.Mode != "wsl" {
			return nil, fmt.Errorf("E_CUDA_WSL_HOST: WSL CUDA requires a WSL C/C++ toolchain")
		}
		distribution := cfg.Toolchain.CUDAWSLDistribution
		if distribution == "" {
			distribution = cfg.Toolchain.WSLDistribution
		}
		if cfg.Toolchain.WSLDistribution != "" && distribution != "" && !strings.EqualFold(cfg.Toolchain.WSLDistribution, distribution) {
			return nil, fmt.Errorf("E_CUDA_WSL_DISTRIBUTION: CUDA is in %s but the compiler is in %s", distribution, cfg.Toolchain.WSLDistribution)
		}
		detected, detectErr = cuda.DetectWSL(ctx, distribution, cfg.Toolchain.CUDARoot, tc)
	} else {
		if tc.Runner != "" {
			return nil, fmt.Errorf("E_CUDA_NATIVE_HOST: native CUDA cannot use a WSL host compiler; connect CUDA from the same WSL distribution")
		}
		root, resolveErr := cudaRoot(ctx, cfg)
		if resolveErr != nil {
			return nil, resolveErr
		}
		detected, detectErr = cuda.DetectWithHost(ctx, root, tc)
	}
	if detectErr != nil {
		return nil, detectErr
	}
	detected.Architectures = append([]string{}, cfg.Toolchain.CUDAArchitectures...)
	if err := cuda.ProbeHost(ctx, detected); err != nil {
		return nil, err
	}
	if cfg.Toolchain.CUDAMode == "rdc" || cfg.Toolchain.CUDAMode == "separate" {
		detected.Mode = cuda.SeparateCompilation
	}
	return &detected, nil
}

// detectVulkanSDK resolves the Vulkan SDK for the first shader target in the
// project, honouring the configured execution environment. It returns nil when
// the project has no shader target.
func detectVulkanSDK(ctx context.Context, cfg config.Config, project model.ResolvedProject, manifestDir string) (*vulkan.SDK, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, target := range project.Targets {
		if target.Type != model.Shader {
			continue
		}
		var detected vulkan.SDK
		var detectErr error
		if cfg.Toolchain.VulkanExecution == "wsl" {
			distribution := cfg.Toolchain.VulkanWSLDistribution
			if distribution == "" {
				distribution = cfg.Toolchain.WSLDistribution
			}
			detected, detectErr = vulkan.DetectWSL(ctx, distribution, cfg.Toolchain.Vulkan)
			if detectErr == nil {
				linuxDir, pathErr := toolchain.WSLPath(ctx, distribution, manifestDir)
				if pathErr != nil {
					return nil, pathErr
				}
				detected = vulkan.WithWSLDirectory(detected, linuxDir)
			}
		} else if cfg.Toolchain.Vulkan != "" {
			detected, detectErr = vulkan.DetectRoot(cfg.Toolchain.Vulkan)
		} else {
			detected, detectErr = vulkan.Detect()
		}
		if detectErr != nil {
			return nil, detectErr
		}
		return &detected, nil
	}
	return nil, nil
}

// scanModules scans every module-providing source in the project and assembles
// the per-source module information and backend extensions used by the build
// plan. With module support disabled it returns empty results.
func scanModules(ctx context.Context, cfg config.Config, project model.ResolvedProject, tc toolchain.Toolchain, root, buildRoot, manifestDir string) (map[string]plan.ModuleInfo, modules.Support, map[string]modules.Support, error) {
	moduleInfos := map[string]plan.ModuleInfo{}
	var moduleBackend modules.Support
	moduleBackends := map[string]modules.Support{}
	if !cfg.Build.Modules {
		return moduleInfos, moduleBackend, moduleBackends, nil
	}
	if tc.Kind != toolchain.Clang {
		return nil, nil, nil, fmt.Errorf("E_MODULE_BACKEND_UNSUPPORTED: modules currently require a Clang toolchain")
	}
	var err error
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
			return nil, nil, nil, fmt.Errorf("E_MODULE_SCAN_FAILED: clang-scan-deps was not found: %w", err)
		}
	}
	if tc.Runner != "" && filepath.VolumeName(scannerPath) != "" {
		return nil, nil, nil, fmt.Errorf("E_MODULE_SCAN_FAILED: WSL requires a Linux clang-scan-deps path, got %q", scannerPath)
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
				options := append(append(append(append([]string{}, cfg.Build.CompileFlags...), cfg.Build.CXXFlags...), target.CXXFlags...), target.CompileSelf.Options...)
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
						return nil, nil, nil, err
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
					return nil, nil, nil, err
				}
				if previous, exists := scanKeysBySource[source]; exists && previous != scan.Key && (hasModuleEdges(bySource[source]) || hasModuleEdges(scan.Document)) {
					return nil, nil, nil, fmt.Errorf("E_MODULE_SCAN_CONFLICT: %s is used by multiple targets with different module compile options", source)
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
						return nil, nil, nil, err
					}
					headerDepsBySource[source] = append(headerDepsBySource[source], stamp)
				}
			}
		}
	}
	if _, err := modules.BuildGraph(documents); err != nil {
		return nil, nil, nil, err
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
					return nil, nil, nil, relErr
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
					return nil, nil, nil, fmt.Errorf("E_MODULE_MISSING_PROVIDER: %s", required.LogicalName)
				}
				info.Requires = append(info.Requires, modules.Reference{LogicalName: required.LogicalName, Path: artifact})
			}
		}
		if len(info.Provides) > 0 || len(info.Requires) > 0 {
			moduleInfos[source] = info
		}
	}
	moduleBackend = clang.Backend{Compiler: tc.CXX, Scanner: scannerPath, Standard: cfg.Build.CXXStandard, Runner: tc.Runner, RunnerArgs: tc.RunnerArgs}
	return moduleInfos, moduleBackend, moduleBackends, nil
}

// writeGenerationArtifacts writes the interruption marker, response files, Ninja
// manifest, command metadata, and generation state for a manifest directory, and
// clears the marker once every write has succeeded.
func writeGenerationArtifacts(ctx context.Context, root, manifestDir, manifestPath string, cfg config.Config, project model.ResolvedProject, tc toolchain.Toolchain, buildPlan plan.BuildPlan, manifest []byte) error {
	marker := filepath.Join(manifestDir, ".trestle", "incomplete")
	if err := fsx.AtomicWrite(marker, []byte("Generation interrupted: run trestle build to regenerate.\n")); err != nil {
		return err
	}
	if err := writeResponseFiles(ctx, cfg, manifestDir, buildPlan); err != nil {
		return err
	}
	if err := fsx.AtomicWrite(manifestPath, manifest); err != nil {
		return err
	}
	if err := writeCommandMetadata(ctx, root, manifestDir, cfg, buildPlan); err != nil {
		return err
	}
	if err := writeGenerationState(manifestDir, cfg, project, tc); err != nil {
		return err
	}
	return os.Remove(marker)
}
