package vcpkg

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/model"
)

// ResolveInstalledManifest reads vcpkg's authoritative per-port file lists.
// This supports ports that expose neither pkg-config metadata nor a built-in
// adapter without guessing from the shared installation tree.
func ResolveInstalledManifest(layout Layout, port string) (model.Usage, bool) {
	return ResolveInstalledManifestForProfile(layout, port, "release")
}

// ResolveInstalledManifestForProfile reads the per-port file lists for a specific
// profile, returning the libraries, headers and runtime files the port installed
// and whether any manifest was found. It is the profile-aware form of
// ResolveInstalledManifest.
func ResolveInstalledManifestForProfile(layout Layout, port, profile string) (model.Usage, bool) {
	manifests := manifestListPaths(layout, port)
	if len(manifests) == 0 {
		return model.Usage{}, false
	}
	contents := collectManifestContents(manifests, layout, profile)
	usage := model.Usage{}
	if contents.hasHeaders {
		usage.Compile.IncludeDirs = []string{layout.IncludeDir}
	}
	if len(contents.libraries) > 0 {
		usage.Link.LibraryDirs = []string{layout.ReleaseLibDir, layout.DebugLibDir}
		for _, library := range sortedKeys(contents.libraries) {
			usage.Link.Items = append(usage.Link.Items, model.LinkItem{Kind: "library", Name: library})
		}
	}
	usage.Link.RuntimeFiles = sortedKeys(contents.runtimeFiles)
	return usage, true
}

// manifestListPaths returns the installed manifest files for a port and triplet,
// covering both the plain and the port-version-suffixed list names.
func manifestListPaths(layout Layout, port string) []string {
	infoDir := filepath.Join(layout.Root, "installed", "vcpkg", "info")
	patterns := []string{
		filepath.Join(infoDir, port+"_*_"+layout.Triplet+".list"),
		filepath.Join(infoDir, port+"_*_"+layout.Triplet+"_*.list"),
	}
	var manifests []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		manifests = append(manifests, matches...)
	}
	return manifests
}

// manifestContents records the install facts gathered from a port's manifest
// lists: the libraries and runtime DLLs it installed and whether it has headers.
type manifestContents struct {
	libraries    map[string]bool
	runtimeFiles map[string]bool
	hasHeaders   bool
}

// collectManifestContents reads every manifest list, gathering the libraries,
// runtime DLLs and header presence it records for the given profile. A list that
// cannot be read is skipped instead of failing the whole resolution.
func collectManifestContents(manifests []string, layout Layout, profile string) manifestContents {
	contents := manifestContents{libraries: map[string]bool{}, runtimeFiles: map[string]bool{}}
	prefix := filepath.ToSlash(layout.Triplet) + "/"
	libPrefix, binPrefix := prefix+"lib/", prefix+"bin/"
	if strings.EqualFold(profile, "debug") {
		libPrefix, binPrefix = prefix+"debug/lib/", prefix+"debug/bin/"
	}
	for _, manifest := range manifests {
		file, err := os.Open(manifest)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			entry := strings.TrimSpace(filepath.ToSlash(scanner.Text()))
			if strings.HasPrefix(entry, prefix+"include/") {
				contents.hasHeaders = true
			}
			if strings.HasPrefix(entry, libPrefix) && !strings.Contains(entry, "/pkgconfig/") {
				if library := libraryName(entry); library != "" {
					contents.libraries[library] = true
				}
			}
			if strings.HasPrefix(entry, binPrefix) && strings.EqualFold(filepath.Ext(entry), ".dll") {
				contents.runtimeFiles[filepath.Join(layout.Root, "installed", filepath.FromSlash(entry))] = true
			}
		}
		_ = file.Close()
	}
	return contents
}

func libraryName(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".lib" && extension != ".a" && extension != ".so" && extension != ".dylib" {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if extension != ".lib" {
		name = strings.TrimPrefix(name, "lib")
	}
	return name
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
