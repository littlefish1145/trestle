package toolchain

import (
	"context"
	"fmt"
	"strings"
)

// Request is the portable [toolchain] specification of one project, expressed
// with version ranges instead of machine-local paths.
type Request struct {
	Mode            string
	WSLDistribution string
	C               string
	CXX             string
	Archiver        string
	Linker          string
	Setup           string
	MSVC            string
	CUDA            string
	CUDARoot        string
	CacheDir        string
}

type setupDetector interface {
	DetectWithSetup(context.Context, string, string) (Toolchain, error)
}

// DetectConfigured turns a Request into a concrete toolchain. Version ranges are
// matched against the cached inventory first, then the existing detection path
// runs with the resolved executables, so a committed configuration keeps working
// on any machine that has a matching toolchain installed.
func DetectConfigured(ctx context.Context, request Request) (Toolchain, error) {
	if request.Mode == "wsl" {
		return DetectWSL(ctx, request.WSLDistribution, request.CXX)
	}
	inventory := DiscoverInventory(ctx, request.CacheDir)
	compiler, cCompiler, err := resolveConfiguredCompiler(inventory, request)
	if err != nil {
		return Toolchain{}, err
	}
	setup, compiler, cCompiler, err := configuredMSVCSetup(ctx, inventory, request, compiler, cCompiler)
	if err != nil {
		return Toolchain{}, err
	}
	detected, err := detectConfiguredToolchain(ctx, compiler, setup)
	if err != nil {
		return Toolchain{}, err
	}
	return applyConfiguredOverrides(detected, request), nil
}

// resolveConfiguredCompiler resolves the cxx and c specs against the inventory,
// keeping the raw c value when it does not name a resolvable compiler.
func resolveConfiguredCompiler(inventory Inventory, request Request) (string, string, error) {
	compiler, err := ResolveCompiler(inventory, request.CXX)
	if err != nil {
		return "", "", err
	}
	cCompiler := request.C
	if resolved, resolveErr := ResolveCompiler(inventory, request.C); resolveErr == nil {
		cCompiler = resolved
	}
	return compiler, cCompiler, nil
}

// configuredMSVCSetup turns the request's msvc range into the Visual Studio
// setup script, letting a constrained pin choose the compiler when nothing else
// was requested. It returns the possibly-updated compiler and c compiler.
func configuredMSVCSetup(ctx context.Context, inventory Inventory, request Request, compiler, cCompiler string) (string, string, string, error) {
	pinnedRange, err := ParseRange(request.MSVC)
	if err != nil {
		return "", "", "", err
	}
	setup := strings.TrimSpace(request.Setup)
	install, installErr := ResolveMSVC(inventory, request.MSVC)
	switch {
	case installErr != nil && pinnedRange.Constrained():
		return "", "", "", installErr
	case installErr == nil:
		if setup == "" {
			setup = install.Setup
		}
		// A constrained msvc range is an explicit instruction to build with that
		// toolset, so it also decides the compiler unless another compiler was
		// requested explicitly.
		if pinnedRange.Constrained() && acceptsMSVC(compiler) {
			compiler = install.Compiler
			if !strings.EqualFold(request.C, "clang-cl") {
				cCompiler = install.Compiler
			}
		}
	}
	if NeedsMSVCEnvironment(ctx, compiler) && setup == "" {
		setup = ResolveMSVCSetup(ctx, setup, request.Archiver, request.Linker, cCompiler, compiler)
	}
	return setup, compiler, cCompiler, nil
}

// detectConfiguredToolchain runs the standard detector, using the
// setup-aware path when a Visual Studio setup script was resolved.
func detectConfiguredToolchain(ctx context.Context, compiler, setup string) (Toolchain, error) {
	detector := NewDetector()
	if setup == "" {
		return detector.Detect(ctx, compiler)
	}
	if withSetup, ok := detector.(setupDetector); ok {
		return withSetup.DetectWithSetup(ctx, compiler, setup)
	}
	return detector.Detect(ctx, compiler)
}

// acceptsMSVC reports whether an msvc toolset pin may choose the compiler. The
// pin names a toolset, so it supplies the compiler only when nothing else was
// requested; an explicitly named clang-cl or an explicit path always wins.
func acceptsMSVC(compiler string) bool {
	switch strings.ToLower(strings.TrimSpace(compiler)) {
	case "", "auto", "cl", "cl.exe":
		return true
	}
	return false
}

func applyConfiguredOverrides(detected Toolchain, request Request) Toolchain {
	if value := strings.TrimSpace(request.C); value != "" && !strings.EqualFold(value, "auto") {
		detected.CC = value
	}
	if value := strings.TrimSpace(request.Archiver); value != "" && !strings.EqualFold(value, "auto") && !strings.EqualFold(value, "lib") {
		detected.Archiver = value
	}
	if value := strings.TrimSpace(request.Linker); value != "" && !strings.EqualFold(value, "auto") {
		detected.Linker = value
	}
	if setup := strings.TrimSpace(request.Setup); setup != "" {
		detected.Setup = setup
	}
	return detected
}

// ResolveCUDARoot turns the portable cuda spec into the toolkit root that nvcc
// lives in. CUDARoot wins when set, and an explicit root path is honoured for
// setups that version detection cannot describe.
func ResolveCUDARoot(ctx context.Context, request Request) (string, error) {
	if trimmed := strings.TrimSpace(request.CUDARoot); trimmed != "" {
		return trimmed, nil
	}
	spec := strings.TrimSpace(request.CUDA)
	if IsLocalPath(spec) {
		return spec, nil
	}
	if spec == "" {
		return "", fmt.Errorf("E_CUDA_NOT_FOUND: set [toolchain].cuda to a version range such as 12.0~12.9")
	}
	install, err := ResolveCUDA(DiscoverInventory(ctx, request.CacheDir), spec)
	if err != nil {
		return "", err
	}
	return install.Root, nil
}

// ResolveMSVCSetupScript returns the environment script for the configured msvc
// range, or an empty string when the request does not constrain one.
func ResolveMSVCSetupScript(ctx context.Context, cacheDir, spec string) string {
	pinned, err := ParseRange(spec)
	if err != nil || pinned.Any() {
		return ""
	}
	install, err := ResolveMSVC(DiscoverInventory(ctx, cacheDir), spec)
	if err != nil {
		return ""
	}
	return install.Setup
}
