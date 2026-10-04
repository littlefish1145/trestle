package toolchain

import (
	"context"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

type Component struct {
	Name         string
	Family       string
	Path         string
	Version      string
	Ready        bool
	Detail       string
	Execution    string
	Distribution string
}

func Discover(ctx context.Context) []Component {
	return append(DiscoverNative(ctx), discoverWSL(ctx)...)
}

func DiscoverWSL(ctx context.Context) []Component { return discoverWSL(ctx) }

func DiscoverNative(ctx context.Context) []Component {
	components := discoverCompilerComponents(ctx)
	components = append(components, discoverMSVC(ctx)...)
	components = append(components, discoverCUDA(ctx)...)
	components = append(components, discoverVulkan(ctx)...)
	seenFamilies := map[string]bool{}
	for _, component := range components {
		seenFamilies[component.Family] = true
	}
	for _, family := range []string{"Clang", "MSVC", "CUDA", "Vulkan", "MinGW"} {
		if !seenFamilies[family] {
			components = append(components, Component{Name: "not detected", Family: family, Ready: false, Detail: "not found"})
		}
	}
	return components
}

// discoverCompilerComponents finds host C/C++ compilers on PATH and in the
// well-known Clang and MinGW prefixes, without probing WSL distributions.
func discoverCompilerComponents(ctx context.Context) []Component {
	var components []Component
	addExecutables := func(family string, names ...string) {
		seen := map[string]bool{}
		add := func(path string) {
			if path == "" || seen[path] {
				return
			}
			seen[path] = true
			components = append(components, Component{Name: filepath.Base(path), Family: family, Path: path, Version: commandVersion(ctx, path), Ready: true, Detail: "compiler"})
		}
		for _, name := range names {
			if path, err := exec.LookPath(name); err == nil {
				add(path)
			}
		}
		roots := map[string][]string{
			"Clang": {"D:\\LLVM\\bin"},
			"MinGW": {os.Getenv("MINGW_ROOT"), "D:\\mingw64\\mingw64\\bin", "D:\\mingw64\\bin", "C:\\mingw64\\bin", "C:\\msys64\\mingw64\\bin"},
		}[family]
		for _, root := range roots {
			if root == "" {
				continue
			}
			for _, name := range names {
				path := filepath.Join(root, name)
				if runtime.GOOS == "windows" {
					path += ".exe"
				}
				if _, err := os.Stat(path); err == nil {
					add(path)
				}
			}
		}
	}
	addExecutables("Clang", "clang-cl", "clang++", "clang")
	addExecutables("MinGW", "g++", "gcc", "mingw32-g++")
	addExecutables("MSVC", "cl")
	return components
}

func discoverWSL(ctx context.Context) []Component {
	if runtime.GOOS != "windows" {
		return nil
	}
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return []Component{{Name: "not available", Family: "WSL", Ready: false, Detail: "wsl.exe was not found"}}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, wsl, "--list", "--quiet").Output()
	if err != nil {
		detail := strings.TrimSpace(decodeWindowsCommand(out))
		if detail == "" {
			detail = err.Error()
		}
		return []Component{{Name: "unavailable", Family: "WSL", Ready: false, Detail: detail}}
	}
	distributions := WSLDistributions(out)
	type discovered struct {
		index      int
		components []Component
	}
	results := make(chan discovered, len(distributions))
	for index, distribution := range distributions {
		go func(index int, distribution string) {
			results <- discovered{index, discoverWSLDistribution(ctx, wsl, distribution)}
		}(index, distribution)
	}
	ordered := make([][]Component, len(distributions))
	for range distributions {
		result := <-results
		ordered[result.index] = result.components
	}
	var result []Component
	for _, components := range ordered {
		result = append(result, components...)
	}
	return result
}

func discoverWSLDistribution(ctx context.Context, wsl, distribution string) []Component {
	var result []Component
	probeCtx, distributionCancel := context.WithTimeout(ctx, 8*time.Second)
	distributionFound := false
	var failures []string
	for _, compiler := range []string{"clang++", "g++"} {
		compilerPath, findErr := WSLExecutable(probeCtx, distribution, compiler)
		if findErr != nil || compilerPath == "" {
			if findErr != nil {
				failures = append(failures, findErr.Error())
			}
			continue
		}
		versionOut, _ := exec.CommandContext(probeCtx, wsl, "-d", distribution, "--exec", compilerPath, "--version").Output()
		version := strings.TrimSpace(strings.Split(string(versionOut), "\n")[0])
		if version == "" {
			version = "version unavailable"
		}
		distributionFound = true
		result = append(result, Component{Name: compiler + " @ " + distribution, Family: "WSL", Path: compilerPath, Version: version, Ready: true, Detail: "runs inside WSL; Windows paths are translated automatically", Execution: "wsl", Distribution: distribution})
	}

	if nvcc, findErr := WSLExecutable(probeCtx, distribution, "nvcc"); findErr == nil {
		root := WSLVariable(probeCtx, distribution, "CUDA_HOME")
		if root == "" {
			root = WSLVariable(probeCtx, distribution, "CUDA_PATH")
		}
		if root == "" {
			root = pathpkg.Dir(pathpkg.Dir(nvcc))
		}
		versionOut, _ := exec.CommandContext(probeCtx, wsl, "-d", distribution, "--exec", nvcc, "--version").Output()
		result = append(result, Component{Name: "CUDA @ " + distribution, Family: "CUDA", Path: nvcc, Version: lastNonEmptyLine(string(versionOut)), Ready: true, Detail: root, Execution: "wsl", Distribution: distribution})
	}

	vulkanRoot := WSLVariable(probeCtx, distribution, "VULKAN_SDK")
	for _, shaderTool := range []string{"glslc", "glslangValidator"} {
		shaderPath, findErr := WSLExecutable(probeCtx, distribution, shaderTool)
		if findErr != nil {
			continue
		}
		root := vulkanRoot
		if root == "" {
			root = pathpkg.Dir(pathpkg.Dir(shaderPath))
		}
		versionOut, _ := exec.CommandContext(probeCtx, wsl, "-d", distribution, "--exec", shaderPath, "--version").Output()
		result = append(result, Component{Name: shaderTool + " @ " + distribution, Family: "Vulkan", Path: shaderPath, Version: firstNonEmptyLine(string(versionOut)), Ready: true, Detail: root, Execution: "wsl", Distribution: distribution})
	}
	if !distributionFound {
		result = append(result, Component{Name: "not ready @ " + distribution, Family: "WSL", Distribution: distribution, Execution: "wsl", Detail: strings.Join(failures, "; ") + ". Check this distribution with wsl -d <name> --exec clang++ --version."})
	}
	distributionCancel()
	return result
}

func WSLDistributions(output []byte) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(decodeWindowsCommand(output), "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "unknown"
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return "unknown"
}

func decodeWindowsCommand(data []byte) string {
	hasNUL := false
	for _, value := range data {
		if value == 0 {
			hasNUL = true
			break
		}
	}
	if len(data) >= 2 && (hasNUL || (data[0] == 0xff && data[1] == 0xfe)) {
		if data[0] == 0xff && data[1] == 0xfe {
			data = data[2:]
		}
		values := make([]uint16, 0, len(data)/2)
		for index := 0; index+1 < len(data); index += 2 {
			values = append(values, uint16(data[index])|uint16(data[index+1])<<8)
		}
		return strings.TrimSpace(string(utf16.Decode(values)))
	}
	return strings.TrimSpace(string(data))
}

func discoverMSVC(ctx context.Context) []Component {
	var result []Component
	for _, install := range DiscoverMSVCInstalls(ctx) {
		result = append(result, Component{Name: "MSVC " + install.Version, Family: "MSVC", Path: install.Compiler, Version: commandVersion(ctx, install.Compiler), Ready: true, Detail: "x64 compiler"})
	}
	return result
}

func discoverCUDA(ctx context.Context) []Component {
	var result []Component
	for _, install := range DiscoverCUDAInstalls(ctx) {
		result = append(result, Component{Name: "CUDA " + install.Version, Family: "CUDA", Path: install.NVCC, Version: commandVersion(ctx, install.NVCC), Ready: true, Detail: install.Root})
	}
	return result
}

func discoverVulkan(ctx context.Context) []Component {
	roots := []string{os.Getenv("VULKAN_SDK"), "D:\\vulkan", "C:\\VulkanSDK", "D:\\VulkanSDK"}
	var result []Component
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, name := range []string{"glslc", "glslangValidator"} {
			path := filepath.Join(root, "Bin", name)
			if runtime.GOOS != "windows" {
				path = filepath.Join(root, "bin", name)
			}
			if runtime.GOOS == "windows" {
				path += ".exe"
			}
			if _, err := os.Stat(path); err != nil {
				if found, findErr := exec.LookPath(name); findErr == nil {
					path = found
				} else {
					continue
				}
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			result = append(result, Component{Name: name, Family: "Vulkan", Path: path, Version: shaderVersion(ctx, path), Ready: true, Detail: root})
		}
	}
	return result
}

func shaderVersion(ctx context.Context, path string) string {
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(lines[0])
}
