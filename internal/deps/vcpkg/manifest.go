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

func ResolveInstalledManifestForProfile(layout Layout, port, profile string) (model.Usage, bool) {
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
	if len(manifests) == 0 {
		return model.Usage{}, false
	}

	libraries := map[string]bool{}
	runtimeFiles := map[string]bool{}
	hasHeaders := false
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
				hasHeaders = true
			}
			if strings.HasPrefix(entry, libPrefix) && !strings.Contains(entry, "/pkgconfig/") {
				if library := libraryName(entry); library != "" {
					libraries[library] = true
				}
			}
			if strings.HasPrefix(entry, binPrefix) && strings.EqualFold(filepath.Ext(entry), ".dll") {
				runtimeFiles[filepath.Join(layout.Root, "installed", filepath.FromSlash(entry))] = true
			}
		}
		_ = file.Close()
	}

	usage := model.Usage{}
	if hasHeaders {
		usage.Compile.IncludeDirs = []string{layout.IncludeDir}
	}
	if len(libraries) > 0 {
		usage.Link.LibraryDirs = []string{layout.ReleaseLibDir, layout.DebugLibDir}
		for _, library := range sortedKeys(libraries) {
			usage.Link.Items = append(usage.Link.Items, model.LinkItem{Kind: "library", Name: library})
		}
	}
	usage.Link.RuntimeFiles = sortedKeys(runtimeFiles)
	return usage, true
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
