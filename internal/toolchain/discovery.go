package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// MSVCInstall is one installed Visual C++ toolset. Version is the VCTOOLSVERSION
// directory name (14.44.35207), which identifies the toolset without probing it.
type MSVCInstall struct {
	Version    string `json:"version"`
	Root       string `json:"root"`
	ToolsetDir string `json:"toolset_dir"`
	Setup      string `json:"setup"`
	Compiler   string `json:"compiler"`
}

// CUDAInstall is one installed CUDA toolkit, located from its toolkit root so
// callers never have to store a machine-specific path in trestle.toml.
type CUDAInstall struct {
	Version string `json:"version"`
	Root    string `json:"root"`
	NVCC    string `json:"nvcc"`
}

// RootOverrides replaces the built-in search paths when set. CI and hermetic
// builds use it to pin the toolchains under test instead of whatever the machine
// happens to have installed.
const (
	VisualStudioRootsEnv = "TRESTLE_VS_ROOTS"
	CUDARootsEnv         = "TRESTLE_CUDA_ROOTS"
)

// overrideRoots reads a path-list override, reporting whether it was set.
func overrideRoots(name string) ([]string, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, false
	}
	var roots []string
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == os.PathListSeparator || r == ';' }) {
		if entry = strings.TrimSpace(entry); entry != "" {
			roots = append(roots, entry)
		}
	}
	return roots, true
}

// visualStudioRoots lists candidate Visual Studio installation roots, newest
// first, from the environment, well-known locations, and vswhere.
func visualStudioRoots(ctx context.Context) []string {
	if roots, overridden := overrideRoots(VisualStudioRootsEnv); overridden {
		return existingRoots(roots)
	}
	var roots []string
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(root))
		for _, existing := range roots {
			if strings.EqualFold(filepath.Clean(existing), key) {
				return
			}
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return
		}
		roots = append(roots, root)
	}
	for _, root := range vswhereInstallations(ctx) {
		add(root)
	}
	for _, root := range []string{os.Getenv("VSINSTALLDIR"), `D:\vs2022`, `C:\Program Files\Microsoft Visual Studio\2022`, `C:\Program Files (x86)\Microsoft Visual Studio\2022`} {
		add(root)
	}
	return roots
}

func existingRoots(roots []string) []string {
	var result []string
	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			result = append(result, root)
		}
	}
	return result
}

func vswhereInstallations(ctx context.Context) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	vswhere := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if _, err := os.Stat(vswhere); err != nil {
		return nil
	}
	output, err := exec.CommandContext(ctx, vswhere, "-all", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "installationPath").Output()
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(decodeNativeOutput(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

// DiscoverMSVCInstalls enumerates every installed MSVC toolset found under the
// known Visual Studio roots. It only inspects directories, so it stays cheap
// enough to run on every configuration change and to cache.
func DiscoverMSVCInstalls(ctx context.Context) []MSVCInstall {
	var result []MSVCInstall
	seen := map[string]bool{}
	for _, root := range visualStudioRoots(ctx) {
		matches, _ := filepath.Glob(filepath.Join(root, "VC", "Tools", "MSVC", "*"))
		for _, toolset := range matches {
			key := strings.ToLower(filepath.Clean(toolset))
			if seen[key] {
				continue
			}
			version, ok := NormalizeVersion(filepath.Base(toolset))
			if !ok {
				continue
			}
			compiler := filepath.Join(toolset, "bin", "Hostx64", "x64", executableName("cl"))
			if _, err := os.Stat(compiler); err != nil {
				continue
			}
			seen[key] = true
			result = append(result, MSVCInstall{
				Version:    version,
				Root:       root,
				ToolsetDir: toolset,
				Setup:      filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat"),
				Compiler:   compiler,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return CompareVersions(result[i].Version, result[j].Version) > 0 })
	return result
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// cudaRoots lists candidate CUDA toolkit roots from the environment and the
// platform's default installation prefixes.
func cudaRoots() []string {
	if roots, overridden := overrideRoots(CUDARootsEnv); overridden {
		return roots
	}
	var candidates []string
	seen := map[string]bool{}
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, root)
	}
	add(os.Getenv("CUDA_PATH"))
	add(os.Getenv("CUDA_HOME"))
	if runtime.GOOS == "windows" {
		matches, _ := filepath.Glob(`C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v*`)
		add("D:\\CUDA")
		for _, match := range matches {
			add(match)
		}
		return candidates
	}
	matches, _ := filepath.Glob("/usr/local/cuda*")
	add("/usr/local/cuda")
	add("/opt/cuda")
	for _, match := range matches {
		add(match)
	}
	return candidates
}

// DiscoverCUDAInstalls enumerates installed CUDA toolkits and recovers each
// toolkit version from its directory name, version.json, or nvcc banner.
func DiscoverCUDAInstalls(ctx context.Context) []CUDAInstall {
	var result []CUDAInstall
	seen := map[string]bool{}
	for _, root := range cudaRoots() {
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			continue
		}
		nvcc := filepath.Join(root, "bin", executableName("nvcc"))
		if _, err := os.Stat(nvcc); err != nil {
			continue
		}
		seen[key] = true
		version, ok := cudaVersionFromRoot(root)
		if !ok {
			version, _ = ExtractVersion(commandVersion(ctx, nvcc))
		}
		result = append(result, CUDAInstall{Version: version, Root: filepath.Clean(root), NVCC: nvcc})
	}
	sort.Slice(result, func(i, j int) bool { return CompareVersions(result[i].Version, result[j].Version) > 0 })
	return result
}

// cudaVersionFromRoot reads the version without executing anything: the toolkit
// directory is named v12.4, and the toolkit ships version.json/version.txt.
func cudaVersionFromRoot(root string) (string, bool) {
	return ToolkitVersionFromPath(root)
}
