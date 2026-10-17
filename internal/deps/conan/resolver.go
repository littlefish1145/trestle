package conan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/config"
	"trestle/internal/model"
)

// Resolver turns a configured conan package into compile/link usage. Conan 2
// has no pkg-config tree: the CMakeDeps generator writes <name>Config.cmake and
// the CMakeToolchain generator writes conanbuild.{sh,bat,cmake} plus
// conan_[release|debug]_{config,opts}.{cmake,txt} into the install output
// folder. This resolver reads that output directly, mirroring how the vcpkg
// resolver reads the installed tree.
type Resolver struct {
	Root      string // conan home (cache)
	BuildRoot string // parent folder holding conan-<profile> outputs
	Profile   string // build profile name ("debug"/"release"/custom)
}

// ResolvedPackage mirrors vcpkg.ResolvedPackage so the shared graph code can
// treat both providers uniformly.
type ResolvedPackage struct {
	Name      string
	Reference string
	OutputDir string
	Usage     model.Usage
	Method    ResolutionMethod
}

// ResolutionMethod identifies how a conan package's usage was derived.
type ResolutionMethod string

// The ways a conan package's usage can be determined: from generator output or
// from manual include/library/define metadata.
const (
	ResolutionGenerators ResolutionMethod = "generators"
	ResolutionManual     ResolutionMethod = "manual"
)

// Resolve returns the compile and link usage for pkg, either from its conan
// install output or from manual metadata when the package declares its own
// paths.
func (resolver Resolver) Resolve(ctx context.Context, pkg config.Package) (ResolvedPackage, error) {
	if hasManualMetadata(pkg) {
		return ResolvedPackage{Name: pkg.Port, Reference: Reference(pkg), Usage: manualUsage(pkg), Method: ResolutionManual}, nil
	}
	layout, err := NewLayout(resolver.Root, resolver.BuildRoot, resolver.Profile)
	if err != nil {
		return ResolvedPackage{}, err
	}
	usage, err := usageFromOutput(layout, pkg)
	if err != nil {
		return ResolvedPackage{}, err
	}
	return ResolvedPackage{Name: pkg.Port, Reference: Reference(pkg), OutputDir: layout.OutputDir, Usage: usage, Method: ResolutionGenerators}, nil
}

// usageFromOutput derives usage from the CMakeDeps artifacts in the install
// output: the include folder, the library folder, and each per-dependency
// <name>-config.cmake listing targets. Runtime DLLs come from the bin folder.
func usageFromOutput(layout Layout, pkg config.Package) (model.Usage, error) {
	name := pkg.Port
	if name == "" {
		name = pkg.Provider
	}
	if slash := strings.Index(name, "/"); slash > 0 {
		name = name[:slash]
	}
	if _, err := os.Stat(layout.OutputDir); err != nil {
		return model.Usage{}, fmt.Errorf("E_CONAN_LAYOUT: conan install output %q is missing", layout.OutputDir)
	}
	usage := model.Usage{Link: model.LinkUsage{LibraryDirs: []string{layout.LibDir}}}
	if entries, err := os.ReadDir(layout.IncludeDir); err == nil && len(entries) > 0 {
		usage.Compile.IncludeDirs = append(usage.Compile.IncludeDirs, layout.IncludeDir)
	}
	// CMakeDeps writes one <name>Config.cmake (or <name>-config.cmake) per
	// dependency plus the requested package; collect the declared libraries
	// from the package's own config file when present.
	for _, candidate := range configCandidates(name) {
		path := filepath.Join(layout.OutputDir, candidate)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		usage.Link.Items = append(usage.Link.Items, librariesFromConfig(string(data), layout.LibDir)...)
		break
	}
	for _, library := range pkg.Libraries {
		usage.Link.Items = append(usage.Link.Items, model.LinkItem{Kind: "library", Name: library, Path: existingLibrary(layout.LibDir, library)})
	}
	for _, define := range pkg.Defines {
		usage.Compile.Defines = append(usage.Compile.Defines, define)
	}
	usage.Link.RuntimeFiles = appendRuntimeFiles(layout.BinDir, usage.Link.RuntimeFiles)
	if len(usage.Compile.IncludeDirs) == 0 && len(usage.Link.Items) == 0 {
		return model.Usage{}, fmt.Errorf("E_CONAN_LAYOUT: no CMakeDeps artifacts for %s under %s", name, layout.OutputDir)
	}
	return usage, nil
}

func configCandidates(name string) []string {
	return []string{name + "Config.cmake", name + "-config.cmake", "conan_official_" + name + "Config.cmake"}
}

// librariesFromConfig extracts static/import library names from the
// `set(<name>_LIBRARIES ...)` block CMakeDeps generates, resolving each against
// the output lib folder.
func librariesFromConfig(text, libDir string) []model.LinkItem {
	var items []model.LinkItem
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "set(") || !strings.Contains(line, "_LIBRARIES") {
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(line, "set("), ")")
		// CMakeDeps wraps the whole value in one pair of quotes and may append
		// a keyword, as in:
		//   set(fmt_LIBRARIES "${fmt_LIBRARIES};${CMAKE_CURRENT_LIST_DIR}/lib/fmt.lib" OPTIONAL)
		// so keep only the quoted value, then split that CMake list on ';'.
		// Entries keep their ${...} directory prefix, so only the basename
		// matters and keywords like OPTIONAL/REQUIRED never carry a library
		// extension.
		if opening := strings.Index(body, `"`); opening >= 0 {
			body = body[opening+1:]
		}
		if closing := strings.LastIndex(body, `"`); closing >= 0 {
			body = body[:closing]
		}
		if fields := strings.Fields(body); len(fields) > 0 {
			body = fields[len(fields)-1]
		}
		for _, field := range strings.Split(body, ";") {
			field = strings.Trim(strings.TrimSpace(field), `"`)
			if !isLibraryFile(field) {
				continue
			}
			items = append(items, libraryItem(libDir, filepath.Base(field)))
		}
	}
	return items
}

// isLibraryFile reports whether a CMake list field names a library artifact
// rather than a keyword like OPTIONAL/REQUIRED.
func isLibraryFile(field string) bool {
	switch strings.ToLower(filepath.Ext(field)) {
	case ".lib", ".a", ".so", ".dylib", ".dll.a":
		return true
	}
	return false
}

func libraryItem(dir, name string) model.LinkItem {
	item := model.LinkItem{Kind: "library", Name: name}
	for _, candidate := range []string{name + ".lib", "lib" + name + ".a", name + ".a", name + ".so"} {
		if path := existingLibrary(dir, candidate); path != "" {
			item.Path = path
			break
		}
	}
	return item
}

func existingLibrary(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func appendRuntimeFiles(binDir string, files []string) []string {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.EqualFold(filepath.Ext(name), ".dll") || strings.EqualFold(filepath.Ext(name), ".dylib") {
			files = append(files, filepath.Join(binDir, name))
		}
	}
	return files
}

func hasManualMetadata(pkg config.Package) bool {
	return len(pkg.IncludeDirs) > 0 || len(pkg.LibraryDirs) > 0 || len(pkg.Libraries) > 0 || len(pkg.Defines) > 0
}

func manualUsage(pkg config.Package) model.Usage {
	usage := model.Usage{Compile: model.CompileUsage{IncludeDirs: pkg.IncludeDirs, Defines: pkg.Defines}, Link: model.LinkUsage{LibraryDirs: pkg.LibraryDirs, RuntimeFiles: pkg.RuntimeFiles}}
	for _, library := range pkg.Libraries {
		usage.Link.Items = append(usage.Link.Items, model.LinkItem{Kind: "library", Name: library})
	}
	return usage
}
