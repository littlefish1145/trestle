package conan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/config"
)

// Layout describes where a finished conan install leaves consumable files.
// Conan has no global install tree like vcpkg: every `conan install` targets
// its own output folder (-of), where the CMakeDeps generator writes
// config/Find modules and CMakeToolchain writes the toolchain and conanbuild.
type Layout struct {
	Root       string // conan home (cache) directory
	BuildRoot  string // parent folder that holds per-profile install outputs
	Profile    string // profile name the output folder was built for
	OutputDir  string // <BuildRoot>/conan-<profile basename> for this install
	IncludeDir string
	LibDir     string
	BinDir     string
}

// Reference builds the conan recipe reference for a configured package:
// name/version[@user/channel] with bracketed features carried on the name
// (config validation keeps features out of Version).
func Reference(pkg config.Package) string {
	name := pkg.Port
	if name == "" {
		name = pkg.Provider
	}
	if pkg.Version != "" {
		name += "/" + pkg.Version
	}
	for _, feature := range pkg.Features {
		if feature == "" {
			continue
		}
		if strings.Contains(name, "[") {
			name += "," + feature
		} else {
			name += "[" + feature
		}
	}
	if strings.Contains(name, "[") {
		name += "]"
	}
	return name
}

// NewLayout returns the layout a conan install for pkg produced under
// buildRoot, checking that the generator output actually exists. The include,
// lib, and bin folders mirror the vcpkg Layout shape so consumers merge usages
// the same way for both providers.
func NewLayout(root, buildRoot, profile string) (Layout, error) {
	if root == "" {
		root = os.Getenv("CONAN_HOME")
	}
	if root == "" {
		return Layout{}, fmt.Errorf("E_CONAN_ROOT: set CONAN_HOME or [conan].root")
	}
	if buildRoot == "" {
		return Layout{}, fmt.Errorf("E_CONAN_BUILD_ROOT: set build_root under [conan] to record conan install output")
	}
	profileName := profile
	if profileName == "" {
		profileName = "default"
	}
	output := filepath.Join(buildRoot, "conan-"+filepath.Base(profileName))
	layout := Layout{
		Root: root, BuildRoot: buildRoot, Profile: profileName, OutputDir: output,
		IncludeDir: filepath.Join(output, "include"),
		LibDir:     filepath.Join(output, "lib"),
		BinDir:     filepath.Join(output, "bin"),
	}
	if _, err := os.Stat(output); err != nil {
		return Layout{}, fmt.Errorf("E_CONAN_LAYOUT: conan install output %q not found under %s", filepath.Base(output), buildRoot)
	}
	return layout, nil
}
