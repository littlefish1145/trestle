package toolchain

import (
	"context"
	"os"
	"os/exec"
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
	components = append(components, discoverMSVC(ctx)...)
	components = append(components, discoverCUDA(ctx)...)
	components = append(components, discoverVulkan(ctx)...)
	components = append(components, discoverWSL(ctx)...)
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

func discoverWSL(ctx context.Context) []Component {
	if runtime.GOOS != "windows" {
		return nil
	}
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return []Component{{Name: "not available", Family: "WSL", Ready: false, Detail: "wsl.exe was not found"}}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, wsl, "--list", "--quiet").CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(decodeWindowsCommand(out))
		if detail == "" {
			detail = err.Error()
		}
		return []Component{{Name: "unavailable", Family: "WSL", Ready: false, Detail: detail}}
	}
	var result []Component
	for _, distribution := range strings.Fields(decodeWindowsCommand(out)) {
		for _, compiler := range []string{"clang++", "g++"} {
			pathOut, findErr := exec.CommandContext(probeCtx, wsl, "-d", distribution, "--exec", "sh", "-lc", "command -v "+compiler).Output()
			path := strings.TrimSpace(string(pathOut))
			if findErr != nil || path == "" {
				continue
			}
			versionOut, _ := exec.CommandContext(probeCtx, wsl, "-d", distribution, "--exec", path, "--version").Output()
			version := strings.TrimSpace(strings.Split(string(versionOut), "\n")[0])
			if version == "" {
				version = "version unavailable"
			}
			result = append(result, Component{Name: compiler + " @ " + distribution, Family: "WSL", Path: path, Version: version, Ready: true, Detail: "runs inside WSL; Windows paths are translated automatically", Execution: "wsl", Distribution: distribution})
		}
	}
	if len(result) == 0 {
		return []Component{{Name: "no compiler", Family: "WSL", Ready: false, Detail: "WSL is installed, but clang++/g++ was not found in its distributions"}}
	}
	return result
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
	roots := []string{os.Getenv("VSINSTALLDIR"), "D:\\vs2022", "C:\\Program Files\\Microsoft Visual Studio\\2022", "C:\\Program Files (x86)\\Microsoft Visual Studio\\2022"}
	var result []Component
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, "VC", "Tools", "MSVC", "*", "bin", "Hostx64", "x64", "cl.exe"))
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true
			result = append(result, Component{Name: "MSVC", Family: "MSVC", Path: path, Version: commandVersion(ctx, path), Ready: true, Detail: "x64 compiler"})
		}
	}
	return result
}

func discoverCUDA(ctx context.Context) []Component {
	roots := []string{os.Getenv("CUDA_PATH"), os.Getenv("CUDA_HOME"), "D:\\CUDA"}
	if runtime.GOOS == "windows" {
		matches, _ := filepath.Glob("C:\\Program Files\\NVIDIA GPU Computing Toolkit\\CUDA\\v*")
		roots = append(roots, matches...)
	}
	var result []Component
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" || seen[filepath.Clean(root)] {
			continue
		}
		seen[filepath.Clean(root)] = true
		path := filepath.Join(root, "bin", "nvcc")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		result = append(result, Component{Name: "CUDA", Family: "CUDA", Path: path, Version: commandVersion(ctx, path), Ready: true, Detail: root})
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
