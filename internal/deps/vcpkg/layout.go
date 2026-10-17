package vcpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Layout holds the absolute directories of one installed vcpkg triplet: its
// include directory, release and debug libraries and binaries, and the
// pkg-config search paths.
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

// DetectLayout builds the Layout for a triplet under a vcpkg root, defaulting the
// root to VCPKG_ROOT. It fails when no root is known, when the triplet is "auto",
// or when the triplet has not been installed.
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

// DefaultTriplet returns the triplet name for the host platform, the natural
// default when the project does not pin one.
func DefaultTriplet() string {
	return TripletFor(runtime.GOOS, runtime.GOARCH)
}

// TripletFor builds a vcpkg triplet name from a Go OS and architecture,
// translating Go's names (amd64, 386, darwin) into vcpkg's (x64, x86, osx).
func TripletFor(osName, arch string) string {
	if arch == "amd64" {
		arch = "x64"
	}
	if arch == "386" {
		arch = "x86"
	}
	if osName == "darwin" {
		osName = "osx"
	}
	return arch + "-" + osName
}

// ValidTripletName reports whether a string can serve as a triplet name: it must
// be non-empty, not the literal "auto", and free of path separators.
func ValidTripletName(value string) bool {
	if value == "" || value == "auto" || strings.ContainsAny(value, `/\\`) {
		return false
	}
	return true
}
