package vcpkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/config"
	"trestle/internal/model"
)

// ResolutionMethod records where a package's usage requirements came from, so
// the UI can explain how a package was resolved.
type ResolutionMethod string

// The resolution methods, from the richest metadata to a hand-written override:
// pkg-config files, a built-in adapter, vcpkg's install manifest, or explicitly
// configured manual paths.
const (
	ResolutionPkgConfig ResolutionMethod = "pkg-config"
	ResolutionAdapter   ResolutionMethod = "adapter"
	ResolutionManifest  ResolutionMethod = "install-manifest"
	ResolutionManual    ResolutionMethod = "manual"
)

// ResolvedPackage is a package whose usage requirements have been resolved,
// tagged with the method that produced them and the triplet they belong to.
type ResolvedPackage struct {
	Name    string
	Port    string
	Triplet string
	Usage   model.Usage
	Method  ResolutionMethod
}

// Resolver turns configured packages into resolved usage requirements by
// consulting a vcpkg installation. Root may be empty when a package carries
// complete manual metadata; Profile and the linkage fields select release or
// debug paths and whether private dependencies are needed.
type Resolver struct {
	Root           string
	Profile        string
	CRTLinkage     string
	LibraryLinkage string
}

// Resolve finds the usage requirements for one configured package, trying
// pkg-config, then a built-in adapter, then the installed manifest, and finally
// any manual metadata on the package. It returns an error when none applies.
func (resolver Resolver) Resolve(ctx context.Context, pkg config.Package) (ResolvedPackage, error) {
	port := pkg.Port
	if port == "" {
		port = pkg.Provider
	}
	if hasManualMetadata(pkg) && resolver.Root == "" {
		return ResolvedPackage{Name: pkg.Provider, Port: port, Triplet: pkg.Triplet, Usage: manualUsage(pkg), Method: ResolutionManual}, nil
	}
	layout, err := DetectLayout(resolver.Root, pkg.Triplet)
	if err != nil {
		if hasManualMetadata(pkg) {
			return ResolvedPackage{Name: pkg.Provider, Port: port, Triplet: pkg.Triplet, Usage: manualUsage(pkg), Method: ResolutionManual}, nil
		}
		return ResolvedPackage{}, err
	}
	if _, findErr := FindPC(layout, port); findErr == nil {
		usage, err := resolver.resolvePC(layout, port, map[string]bool{})
		if err != nil {
			return ResolvedPackage{}, err
		}
		if owned, ok := ResolveInstalledManifestForProfile(layout, port, resolver.Profile); ok {
			usage.Link.RuntimeFiles = owned.Link.RuntimeFiles
		}
		return ResolvedPackage{Name: port, Port: port, Triplet: layout.Triplet, Usage: resolver.finalizeUsage(layout, usage), Method: ResolutionPkgConfig}, nil
	}
	if usage, ok := ResolveAdapter(port, layout); ok {
		return ResolvedPackage{Name: port, Port: port, Triplet: layout.Triplet, Usage: resolver.finalizeUsage(layout, usage), Method: ResolutionAdapter}, nil
	}
	if usage, ok := ResolveInstalledManifestForProfile(layout, port, resolver.Profile); ok {
		usage = resolver.resolveManifestDependencies(layout, port, usage, map[string]bool{port: true})
		return ResolvedPackage{Name: port, Port: port, Triplet: layout.Triplet, Usage: resolver.finalizeUsage(layout, usage), Method: ResolutionManifest}, nil
	}
	if hasManualMetadata(pkg) {
		return ResolvedPackage{Name: pkg.Provider, Port: port, Triplet: pkg.Triplet, Usage: manualUsage(pkg), Method: ResolutionManual}, nil
	}
	return ResolvedPackage{}, fmt.Errorf("E_PACKAGE_METADATA_UNSUPPORTED: %s has no pkg-config metadata, built-in adapter, or manual override", port)
}

func (resolver Resolver) resolveManifestDependencies(layout Layout, port string, usage model.Usage, visiting map[string]bool) model.Usage {
	for _, dependency := range InstalledDependencies(layout, port) {
		if visiting[dependency] {
			continue
		}
		visiting[dependency] = true
		if child, ok := ResolveInstalledManifestForProfile(layout, dependency, resolver.Profile); ok {
			child = resolver.resolveManifestDependencies(layout, dependency, child, visiting)
			usage = mergeUsage(usage, child)
		}
		delete(visiting, dependency)
	}
	return usage
}

func (resolver Resolver) finalizeUsage(layout Layout, usage model.Usage) model.Usage {
	libDir := layout.ReleaseLibDir
	if strings.EqualFold(resolver.Profile, "debug") {
		libDir = layout.DebugLibDir
	}
	usage.Link.LibraryDirs = []string{libDir}
	for index := range usage.Link.Items {
		if usage.Link.Items[index].Path == "" {
			usage.Link.Items[index] = libraryItem(libDir, usage.Link.Items[index].Name)
		}
	}
	binDir := layout.ReleaseBinDir
	if strings.EqualFold(resolver.Profile, "debug") {
		binDir = layout.DebugBinDir
	}
	runtimeFiles := make([]string, 0, len(usage.Link.RuntimeFiles))
	for _, file := range usage.Link.RuntimeFiles {
		candidate := filepath.Join(binDir, filepath.Base(file))
		if _, err := os.Stat(candidate); err == nil {
			file = candidate
		}
		duplicate := false
		for _, existing := range runtimeFiles {
			if strings.EqualFold(filepath.Clean(existing), filepath.Clean(file)) || strings.EqualFold(filepath.Base(existing), filepath.Base(file)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			runtimeFiles = append(runtimeFiles, file)
		}
	}
	usage.Link.RuntimeFiles = runtimeFiles
	return usage
}

func (resolver Resolver) resolvePC(layout Layout, name string, visiting map[string]bool) (model.Usage, error) {
	if visiting[name] {
		return model.Usage{}, nil
	}
	visiting[name] = true
	defer delete(visiting, name)
	if strings.EqualFold(resolver.Profile, "debug") {
		layout.PkgConfigDirs = append([]string{filepath.Join(layout.DebugLibDir, "pkgconfig")}, layout.PkgConfigDirs...)
	}
	path, err := FindPC(layout, name)
	if err != nil {
		return model.Usage{}, err
	}
	pc, err := ParsePC(path)
	if err != nil {
		return model.Usage{}, fmt.Errorf("parse %s: %w", path, err)
	}
	libDir := resolver.libraryDir(layout)
	u := model.Usage{Compile: model.CompileUsage{IncludeDirs: []string{layout.IncludeDir}}, Link: model.LinkUsage{LibraryDirs: []string{libDir}}}
	applyPCCflags(&u.Compile, pc.Cflags)
	libs := append([]string{}, pc.Libs...)
	requires := append([]string{}, pc.Requires...)
	if resolver.usesStaticLibraries(layout) {
		libs = append(libs, pc.LibsPrivate...)
		requires = append(requires, pc.RequiresPrivate...)
	}
	u = resolver.mergeResolvedRequires(layout, u, requires, visiting)
	applyPCLibs(&u.Link, libDir, libs)
	return u, nil
}

// libraryDir returns the library directory for the profile this resolver is
// configured for.
func (resolver Resolver) libraryDir(layout Layout) string {
	if strings.EqualFold(resolver.Profile, "debug") {
		return layout.DebugLibDir
	}
	return layout.ReleaseLibDir
}

// usesStaticLibraries reports whether the resolver links libraries statically,
// either from its own linkage setting or from a static triplet name.
func (resolver Resolver) usesStaticLibraries(layout Layout) bool {
	return strings.EqualFold(resolver.LibraryLinkage, "static") || strings.Contains(layout.Triplet, "static")
}

// mergeResolvedRequires merges the usage of every required pkg-config file into
// usage. A requirement that fails to resolve is skipped rather than failing the
// whole package.
func (resolver Resolver) mergeResolvedRequires(layout Layout, usage model.Usage, requires []string, visiting map[string]bool) model.Usage {
	for _, dep := range requires {
		child, err := resolver.resolvePC(layout, dep, visiting)
		if err == nil {
			usage = mergeUsage(usage, child)
		}
	}
	return usage
}

// applyPCCflags folds pkg-config compiler flags into the compile usage,
// splitting -I include directories and -D defines out of the remaining options.
func applyPCCflags(compile *model.CompileUsage, flags []string) {
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-I") {
			compile.IncludeDirs = append(compile.IncludeDirs, strings.TrimPrefix(flag, "-I"))
		} else if strings.HasPrefix(flag, "-D") {
			compile.Defines = append(compile.Defines, strings.TrimPrefix(flag, "-D"))
		} else {
			compile.Options = append(compile.Options, flag)
		}
	}
}

// applyPCLibs folds pkg-config linker flags into the link usage, splitting -L
// directories, -l libraries and bare archive names out of the remaining options.
func applyPCLibs(link *model.LinkUsage, libDir string, flags []string) {
	for _, flag := range flags {
		switch {
		case strings.HasPrefix(flag, "-L"):
			link.LibraryDirs = append(link.LibraryDirs, strings.TrimPrefix(flag, "-L"))
		case strings.HasPrefix(flag, "-l"):
			name := strings.TrimPrefix(flag, "-l")
			link.Items = append(link.Items, libraryItem(libDir, name))
		case strings.HasSuffix(strings.ToLower(flag), ".lib") || strings.HasSuffix(strings.ToLower(flag), ".a"):
			link.Items = append(link.Items, model.LinkItem{Kind: "library", Name: flag, Path: existingLibrary(libDir, flag)})
		default:
			link.Options = append(link.Options, flag)
		}
	}
}

func libraryItem(dir, name string) model.LinkItem {
	item := model.LinkItem{Kind: "library", Name: name}
	for _, candidate := range []string{name + ".lib", "lib" + name + ".a", name + ".a"} {
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
func mergeUsage(a, b model.Usage) model.Usage {
	a.Compile.IncludeDirs = append(a.Compile.IncludeDirs, b.Compile.IncludeDirs...)
	a.Compile.Defines = append(a.Compile.Defines, b.Compile.Defines...)
	a.Compile.Options = append(a.Compile.Options, b.Compile.Options...)
	a.Link.LibraryDirs = append(a.Link.LibraryDirs, b.Link.LibraryDirs...)
	a.Link.Items = append(a.Link.Items, b.Link.Items...)
	a.Link.Options = append(a.Link.Options, b.Link.Options...)
	a.Link.RuntimeFiles = append(a.Link.RuntimeFiles, b.Link.RuntimeFiles...)
	return a
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

// PackageNameFromPath extracts a package name from a pkg-config file path by
// dropping the directory and the .pc extension.
func PackageNameFromPath(path string) string { return strings.TrimSuffix(filepath.Base(path), ".pc") }

// InstalledMarker reports whether a triplet layout has a populated include
// directory, which distinguishes an installed prefix from an empty one.
func InstalledMarker(layout Layout) bool {
	_, err := os.Stat(filepath.Join(layout.InstalledPrefix, "include"))
	return err == nil
}
