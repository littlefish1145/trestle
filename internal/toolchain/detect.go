package toolchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type DetectorImpl struct{}

func NewDetector() Detector { return DetectorImpl{} }

func (DetectorImpl) Detect(ctx context.Context, requested string) (Toolchain, error) {
	return detect(ctx, requested, "")
}

func (DetectorImpl) DetectWithSetup(ctx context.Context, requested, setup string) (Toolchain, error) {
	return detect(ctx, requested, setup)
}

func EnvironmentForSetup(ctx context.Context, setup string) map[string]string {
	environment := environmentForSetup(ctx, setup)
	if root := visualStudioRootFromSetup(setup); root != "" {
		if environment == nil {
			environment = map[string]string{}
		}
		environment["VCPKG_VISUAL_STUDIO_PATH"] = root
	}
	return environment
}

func NeedsMSVCEnvironment(ctx context.Context, compiler string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(compiler)))
	if base == "cl" || base == "cl.exe" || strings.Contains(base, "clang-cl") {
		return true
	}
	if runtime.GOOS != "windows" || !strings.Contains(base, "clang") {
		return false
	}
	return targetUsesMSVCABI(compilerTarget(ctx, compiler))
}

func targetUsesMSVCABI(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.Contains(target, "-windows-msvc") || strings.HasSuffix(target, "-msvc")
}

func compilerTarget(ctx context.Context, compiler string) string {
	compiler = strings.TrimSpace(compiler)
	if compiler == "" || compiler == "auto" {
		return ""
	}
	path, err := exec.LookPath(compiler)
	if err != nil {
		return ""
	}
	if output, err := exec.CommandContext(ctx, path, "-dumpmachine").CombinedOutput(); err == nil {
		if target := strings.TrimSpace(decodeNativeOutput(output)); target != "" {
			return strings.Fields(target)[0]
		}
	}
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(decodeNativeOutput(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "target:") {
			return strings.TrimSpace(line[len("target:"):])
		}
	}
	return ""
}

// ResolveMSVCSetup selects the ABI environment belonging to configured MSVC
// tools before considering machine-wide Visual Studio discovery.
func ResolveMSVCSetup(ctx context.Context, setup string, toolPaths ...string) string {
	if candidate := validMSVCSetup(setup); candidate != "" {
		return candidate
	}
	for _, path := range toolPaths {
		if root := visualStudioRootFromToolPath(path); root != "" {
			if candidate := validMSVCSetup(filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")); candidate != "" {
				return candidate
			}
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, root := range []string{`D:\vs2022`, os.Getenv("VSINSTALLDIR"), `C:\Program Files\Microsoft Visual Studio\2022`, `C:\Program Files (x86)\Microsoft Visual Studio\2022`} {
		if candidate := setupUnderRoot(root); candidate != "" {
			return candidate
		}
	}
	return setupFromVSWhere(ctx)
}

func validMSVCSetup(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(absolute); err != nil {
		return ""
	}
	root := visualStudioRootFromSetup(absolute)
	if root == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(root, "VC", "Tools", "MSVC", "*", "bin", "Hostx64", "x64", "cl.exe"))
	if len(matches) == 0 {
		return ""
	}
	return absolute
}

func setupUnderRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if candidate := validMSVCSetup(filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")); candidate != "" {
		return candidate
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", "VC", "Auxiliary", "Build", "vcvars64.bat"))
	sort.Strings(matches)
	for _, match := range matches {
		if candidate := validMSVCSetup(match); candidate != "" {
			return candidate
		}
	}
	return ""
}

func setupFromVSWhere(ctx context.Context) string {
	vswhere := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if _, err := os.Stat(vswhere); err != nil {
		return ""
	}
	output, err := exec.CommandContext(ctx, vswhere, "-all", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "installationPath").Output()
	if err != nil {
		return ""
	}
	for _, root := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if candidate := setupUnderRoot(root); candidate != "" {
			return candidate
		}
	}
	return ""
}

// EnvironmentForVcpkg keeps vcpkg on the same Visual Studio installation as
// the configured MSVC-ABI toolchain. This is especially important for
// clang-cl: vcpkg otherwise asks vswhere for the newest instance, which may be
// a preview or incomplete installation unrelated to the selected archiver.
func EnvironmentForVcpkg(ctx context.Context, setup string, toolPaths ...string) map[string]string {
	root := visualStudioRootFromSetup(setup)
	if root == "" {
		for _, path := range toolPaths {
			if root = visualStudioRootFromToolPath(path); root != "" {
				break
			}
		}
	}
	effectiveSetup := setup
	if effectiveSetup == "" && root != "" {
		candidate := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
		if _, err := os.Stat(candidate); err == nil {
			effectiveSetup = candidate
		}
	}
	environment := environmentForSetup(ctx, effectiveSetup)
	if root != "" {
		if environment == nil {
			environment = map[string]string{}
		}
		environment["VCPKG_VISUAL_STUDIO_PATH"] = root
	}
	return environment
}

func detect(ctx context.Context, requested, setup string) (Toolchain, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return Toolchain{}, fmt.Errorf("toolchain is not configured")
	}
	if requested != "auto" {
		path, err := exec.LookPath(requested)
		if err != nil {
			return Toolchain{}, fmt.Errorf("compiler %q was not found: %w", requested, err)
		}
		kind := kindFromBase(filepath.Base(path), requested)
		version := commandVersion(ctx, path)
		target := compilerTarget(ctx, path)
		if target == "" {
			target = runtime.GOARCH
		}
		candidate := Toolchain{Kind: kind, CC: compilerCC(kind, path), CXX: path, Archiver: findArchiver(kind, path), Linker: path, Version: version, Target: target, Setup: setup}
		if kind == MSVC || targetUsesMSVCABI(target) {
			configured, err := withMSVCEnvironment(ctx, candidate, setup)
			if err != nil {
				return Toolchain{}, err
			}
			candidate = configured
		}
		return candidate, nil
	}
	if setup != "" {
		for _, preferred := range []string{"clang-cl", "cl"} {
			if _, err := exec.LookPath(preferred); err == nil {
				return detect(ctx, preferred, setup)
			}
		}
	}
	var candidates []Toolchain
	if path, err := exec.LookPath("clang++"); err == nil {
		target := compilerTarget(ctx, path)
		candidate := Toolchain{Kind: Clang, CC: clangCC(path), CXX: path, Archiver: findArchiver(Clang, path), Linker: path, Version: commandVersion(ctx, path), Target: target}
		if targetUsesMSVCABI(target) {
			candidate, err = withMSVCEnvironment(ctx, candidate, setup)
			if err != nil {
				return Toolchain{}, err
			}
		}
		candidates = append(candidates, candidate)
	}
	if path, err := exec.LookPath("g++"); err == nil {
		candidates = append(candidates, Toolchain{Kind: GCC, CC: gccCC(path), CXX: path, Archiver: findArchiver(GCC, path), Linker: path, Version: commandVersion(ctx, path), Target: runtime.GOARCH})
	}
	if path, err := exec.LookPath("cl"); err == nil {
		candidate, configureErr := withMSVCEnvironment(ctx, Toolchain{Kind: MSVC, CC: path, CXX: path, Archiver: findArchiver(MSVC, path), Linker: path, Version: commandVersion(ctx, path), Target: runtime.GOARCH}, setup)
		if configureErr != nil {
			return Toolchain{}, configureErr
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return Toolchain{}, fmt.Errorf("E_TOOLCHAIN_NOT_FOUND: no clang++, g++, or cl executable was found")
	}
	if len(candidates) > 1 {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, string(candidate.Kind))
		}
		return Toolchain{}, fmt.Errorf("E_TOOLCHAIN_AMBIGUOUS: explicit toolchain = auto found %s; set cxx in trestle.toml", strings.Join(names, ", "))
	}
	return candidates[0], nil
}

func (t Toolchain) List() []Toolchain { return []Toolchain{t} }

func withMSVCEnvironment(ctx context.Context, candidate Toolchain, setup string) (Toolchain, error) {
	setup = ResolveMSVCSetup(ctx, setup, candidate.Archiver, candidate.Linker, candidate.CC, candidate.CXX)
	environment := msvcEnvironment(environmentForSetup(ctx, setup))
	if environment["INCLUDE"] == "" || environment["LIB"] == "" {
		return Toolchain{}, fmt.Errorf("E_MSVC_ENVIRONMENT_NOT_FOUND: compiler %s targets the MSVC ABI, but no valid Visual Studio x64 C++ environment was found", candidate.CXX)
	}
	candidate.Setup = setup
	candidate.Env = environment
	if candidate.Kind == MSVC {
		candidate.Archiver = findArchiverInEnvironment(candidate.Kind, candidate.CXX, environment)
	}
	return candidate, nil
}

func List(ctx context.Context) []Toolchain {
	var result []Toolchain
	for _, requested := range []string{"clang++", "g++", "cl"} {
		if path, err := exec.LookPath(requested); err == nil {
			kind := kindFromBase(filepath.Base(path), requested)
			target := compilerTarget(ctx, path)
			if target == "" {
				target = runtime.GOARCH
			}
			candidate := Toolchain{Kind: kind, CC: compilerCC(kind, path), CXX: path, Archiver: findArchiver(kind, path), Linker: path, Version: commandVersion(ctx, path), Target: target}
			if kind == MSVC || targetUsesMSVCABI(target) {
				if configured, configureErr := withMSVCEnvironment(ctx, candidate, ""); configureErr == nil {
					candidate = configured
				}
			}
			result = append(result, candidate)
		}
	}
	return result
}

func msvcEnvironment(environment map[string]string) map[string]string {
	if environment == nil {
		environment = map[string]string{}
	}
	// Ninja's MSVC dependency parser expects the stable English
	// /showIncludes prefix. This also keeps redirected diagnostics ASCII/UTF-8
	// friendly instead of emitting the machine's legacy console code page.
	environment["VSLANG"] = "1033"
	return environment
}

func kindFromBase(base, requested string) Kind {
	base = strings.ToLower(base)
	requested = strings.ToLower(requested)
	switch {
	case strings.Contains(base, "clang-cl") || strings.Contains(requested, "clang-cl"):
		return MSVC
	case strings.Contains(base, "clang") || strings.Contains(requested, "clang"):
		return Clang
	case strings.Contains(base, "g++") || strings.Contains(base, "gcc") || strings.Contains(base, "mingw") || strings.Contains(requested, "mingw"):
		return GCC
	case base == "cl.exe" || base == "cl" || strings.Contains(requested, "msvc"):
		return MSVC
	default:
		return Kind(base)
	}
}

func commandVersion(ctx context.Context, path string) string {
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		output, err = exec.CommandContext(ctx, path).CombinedOutput()
		if err != nil {
			return "unknown"
		}
	}
	lines := strings.Split(strings.TrimSpace(decodeNativeOutput(output)), "\n")
	if len(lines) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(lines[0])
}

func compilerCC(kind Kind, cxx string) string {
	if kind == MSVC {
		return cxx
	}
	dir := filepath.Dir(cxx)
	var name string
	if kind == Clang {
		name = "clang"
	} else {
		name = "gcc"
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	candidate := filepath.Join(dir, name+".exe")
	if path, err := exec.LookPath(candidate); err == nil {
		return path
	}
	return cxx
}

func clangCC(path string) string { return compilerCC(Clang, path) }
func gccCC(path string) string   { return compilerCC(GCC, path) }

func visualStudioRootFromSetup(setup string) string {
	if setup == "" {
		return ""
	}
	path, err := filepath.Abs(setup)
	if err != nil {
		return ""
	}
	directory := filepath.Dir(path)
	for index := 0; index < 6; index++ {
		devcmd := filepath.Join(directory, "Common7", "Tools", "VsDevCmd.bat")
		vcvars := filepath.Join(directory, "VC", "Auxiliary", "Build", "vcvars64.bat")
		if _, err := os.Stat(devcmd); err == nil {
			return directory
		}
		if _, err := os.Stat(vcvars); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return ""
}

func visualStudioRootFromToolPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.VolumeName(path) == "" {
		return ""
	}
	directory := filepath.Dir(filepath.Clean(path))
	for index := 0; index < 10; index++ {
		vcvars := filepath.Join(directory, "VC", "Auxiliary", "Build", "vcvars64.bat")
		if _, err := os.Stat(vcvars); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return ""
}

func environmentForSetup(ctx context.Context, setup string) map[string]string {
	if setup == "" {
		return visualStudioEnvironment(ctx)
	}
	if _, err := os.Stat(setup); err != nil {
		return nil
	}
	command := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "call vcvars64.bat >nul && set")
	command.Dir = filepath.Dir(setup)
	output, err := command.Output()
	if err != nil {
		return nil
	}
	return parseSetupEnvironment(string(output))
}

func parseSetupEnvironment(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 && requiredEnvironment[strings.ToUpper(parts[0])] {
			result[strings.ToUpper(parts[0])] = parts[1]
		}
	}
	return result
}

var requiredEnvironment = map[string]bool{
	"INCLUDE":            true,
	"LIB":                true,
	"LIBPATH":            true,
	"PATH":               true,
	"VCTOOLSINSTALLDIR":  true,
	"VCTOOLSVERSION":     true,
	"VSINSTALLDIR":       true,
	"WINDOWSSDKDIR":      true,
	"WINDOWSSDKVERSION":  true,
	"UNIVERSALCRTSDKDIR": true,
	"UCRTVERSION":        true,
}

func visualStudioEnvironment(ctx context.Context) map[string]string {
	if runtime.GOOS != "windows" {
		return nil
	}
	vswhere := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if _, err := os.Stat(vswhere); err != nil {
		return nil
	}
	output, err := exec.CommandContext(ctx, vswhere, "-latest", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "installationPath").CombinedOutput()
	if err != nil {
		return nil
	}
	installation := strings.TrimSpace(string(output))
	if installation == "" {
		return nil
	}
	devcmd := filepath.Join(installation, "Common7", "Tools", "VsDevCmd.bat")
	if _, err := os.Stat(devcmd); err != nil {
		return nil
	}
	command := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", fmt.Sprintf("call %s -arch=x64 >nul && set", devcmd))
	environment, err := command.Output()
	if err != nil {
		return nil
	}
	return parseSetupEnvironment(string(environment))
}

func findArchiverInEnvironment(kind Kind, compiler string, environment map[string]string) string {
	if kind != MSVC {
		return findArchiver(kind, compiler)
	}
	for _, directory := range strings.Split(environment["PATH"], ";") {
		for _, name := range []string{"lib.exe", "lib"} {
			candidate := filepath.Join(directory, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return findArchiver(kind, compiler)
}

func findArchiver(kind Kind, compiler string) string {
	if kind == MSVC {
		if path, err := exec.LookPath("lib"); err == nil {
			return path
		}
		return "lib"
	}
	name := "llvm-ar"
	if kind == GCC {
		name = "ar"
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	candidate := filepath.Join(filepath.Dir(compiler), name+".exe")
	if path, err := exec.LookPath(candidate); err == nil {
		return path
	}
	return name
}
