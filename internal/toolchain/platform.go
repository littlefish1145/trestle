package toolchain

import (
	"runtime"
	"strings"
)

func TargetPlatform(tc Toolchain) (string, string) {
	osName, arch := runtime.GOOS, runtime.GOARCH
	target := strings.ToLower(tc.Target)
	switch {
	case tc.Runner != "" || target == "wsl":
		osName = "linux"
	case strings.Contains(target, "windows") || strings.Contains(target, "mingw"):
		osName = "windows"
	case strings.Contains(target, "darwin") || strings.Contains(target, "apple"):
		osName = "darwin"
	case strings.Contains(target, "linux"):
		osName = "linux"
	}
	switch {
	case strings.Contains(target, "aarch64") || strings.Contains(target, "arm64"):
		arch = "arm64"
	case strings.Contains(target, "x86_64") || strings.Contains(target, "amd64"):
		arch = "amd64"
	case strings.Contains(target, "i686") || strings.Contains(target, "i386"):
		arch = "386"
	}
	return osName, arch
}

func TargetABI(tc Toolchain) string {
	osName, _ := TargetPlatform(tc)
	if osName != "windows" {
		return ""
	}
	target := strings.ToLower(tc.Target)
	if strings.Contains(target, "msvc") || tc.Kind == MSVC {
		return "msvc"
	}
	if strings.Contains(target, "mingw") || strings.Contains(target, "windows-gnu") || tc.Kind == GCC {
		return "mingw"
	}
	return ""
}
