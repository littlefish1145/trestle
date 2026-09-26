package vcpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Layout struct {
	Root            string
	Triplet         string
	IncludeDir      string
	ReleaseLibDir   string
	DebugLibDir     string
	ReleaseBinDir   string
	DebugBinDir     string
	PkgConfigDirs   []string
	InstalledPrefix string
}

func DetectLayout(root, triplet string) (Layout, error) {
	if root == "" {
		root = os.Getenv("VCPKG_ROOT")
	}
	if root == "" {
		return Layout{}, fmt.Errorf("E_VCPKG_ROOT: set VCPKG_ROOT or [vcpkg].root")
	}
	if triplet == "" || triplet == "auto" {
		return Layout{}, fmt.Errorf("E_VCPKG_TRIPLET: auto requires exactly one configured triplet; use configure to persist it")
	}
	installed := filepath.Join(root, "installed", triplet)
	layout := Layout{
		Root: root, Triplet: triplet, IncludeDir: filepath.Join(installed, "include"),
		ReleaseLibDir: filepath.Join(installed, "lib"), DebugLibDir: filepath.Join(installed, "debug", "lib"),
		ReleaseBinDir: filepath.Join(installed, "bin"), DebugBinDir: filepath.Join(installed, "debug", "bin"),
		InstalledPrefix: installed,
	}
	layout.PkgConfigDirs = []string{
		filepath.Join(installed, "lib", "pkgconfig"),
		filepath.Join(installed, "share", "pkgconfig"),
		filepath.Join(installed, "debug", "lib", "pkgconfig"),
	}
	if _, err := os.Stat(installed); err != nil {
		return Layout{}, fmt.Errorf("E_VCPKG_LAYOUT: installed triplet %q not found under %s", triplet, root)
	}
	return layout, nil
}

func DefaultTriplet() string {
	if runtime.GOOS == "windows" {
		return "x64-windows"
	}
	if runtime.GOOS == "darwin" {
		return "x64-osx"
	}
	return "x64-linux"
}

func ValidTripletName(value string) bool {
	if value == "" || value == "auto" || strings.ContainsAny(value, `/\\`) {
		return false
	}
	return true
}
