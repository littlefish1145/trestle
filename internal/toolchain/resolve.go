package toolchain

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Selection is the outcome of matching a portable spec against the inventory.
type Selection struct {
	Spec    string
	Version string
	Path    string
	Setup   string
}

// IsLocalPath reports whether a spec names a location on this machine rather
// than a portable version constraint. Absolute paths are still honoured as an
// escape hatch, but trestle never writes them.
func IsLocalPath(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if filepath.IsAbs(spec) || filepath.VolumeName(spec) != "" {
		return true
	}
	return strings.HasPrefix(spec, "~") || strings.ContainsAny(spec, `/\`)
}

// ResolveMSVC picks the highest installed MSVC toolset whose VCTOOLSVERSION
// satisfies spec. An empty spec or "auto" accepts the newest toolset.
func ResolveMSVC(inventory Inventory, spec string) (MSVCInstall, error) {
	wanted, err := ParseRange(spec)
	if err != nil {
		return MSVCInstall{}, err
	}
	candidates := make([]MSVCInstall, 0, len(inventory.MSVC))
	for _, install := range inventory.MSVC {
		if wanted.Contains(install.Version) {
			candidates = append(candidates, install)
		}
	}
	if len(candidates) == 0 {
		return MSVCInstall{}, fmt.Errorf("E_MSVC_NOT_FOUND: no installed MSVC toolset satisfies %s; installed toolsets: %s. Widen [toolchain].msvc or install the required Visual C++ tools",
			describeRange(spec, wanted), describeVersions(msvcVersions(inventory)))
	}
	sort.SliceStable(candidates, func(i, j int) bool { return CompareVersions(candidates[i].Version, candidates[j].Version) > 0 })
	return candidates[0], nil
}

// ResolveCUDA picks the highest installed CUDA toolkit whose version satisfies
// spec. An empty spec or "auto" accepts the newest toolkit.
func ResolveCUDA(inventory Inventory, spec string) (CUDAInstall, error) {
	wanted, err := ParseRange(spec)
	if err != nil {
		return CUDAInstall{}, err
	}
	candidates := make([]CUDAInstall, 0, len(inventory.CUDA))
	for _, install := range inventory.CUDA {
		if wanted.Contains(install.Version) {
			candidates = append(candidates, install)
		}
	}
	if len(candidates) == 0 {
		return CUDAInstall{}, fmt.Errorf("E_CUDA_NOT_FOUND: no installed CUDA toolkit satisfies %s; installed toolkits: %s. Widen [toolchain].cuda or install the required CUDA version",
			describeRange(spec, wanted), describeVersions(cudaVersions(inventory)))
	}
	sort.SliceStable(candidates, func(i, j int) bool { return CompareVersions(candidates[i].Version, candidates[j].Version) > 0 })
	return candidates[0], nil
}

// ResolveCompiler matches a compiler spec against the discovered compilers.
// A bare name or an absolute path is returned untouched so the existing
// detection path keeps working; only a version range is resolved here.
func ResolveCompiler(inventory Inventory, spec string) (string, error) {
	trimmed := strings.TrimSpace(spec)
	// An executable name or an explicit path is an escape hatch and needs no
	// resolution; only something written as a version is matched against.
	if IsLocalPath(trimmed) || !LooksLikeVersion(trimmed) {
		if trimmed == "" {
			return "auto", nil
		}
		return trimmed, nil
	}
	wanted, err := ParseRange(trimmed)
	if err != nil {
		return "", err
	}
	if !wanted.Constrained() {
		return trimmed, nil
	}
	candidates := make([]Component, 0, len(inventory.Compilers))
	for _, component := range inventory.Compilers {
		if !component.Ready {
			continue
		}
		if version, ok := ExtractVersion(component.Version); ok && wanted.Contains(version) {
			candidates = append(candidates, component)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("E_TOOLCHAIN_NOT_FOUND: no installed compiler satisfies %q; installed compilers: %s. Widen [toolchain].cxx or install the required version",
			trimmed, describeCompilers(inventory))
	}
	sortComponentsByVersion(candidates)
	return candidates[0].Path, nil
}

func describeRange(spec string, wanted Range) string {
	if trimmed := strings.TrimSpace(spec); trimmed != "" {
		return fmt.Sprintf("%q", trimmed)
	}
	return "the requested version"
}

func describeVersions(versions []string) string {
	if len(versions) == 0 {
		return "none"
	}
	return strings.Join(versions, ", ")
}

func describeCompilers(inventory Inventory) string {
	var result []string
	for _, component := range inventory.Compilers {
		if !component.Ready {
			continue
		}
		version, ok := ExtractVersion(component.Version)
		if !ok {
			continue
		}
		result = append(result, component.Family+" "+version)
	}
	sort.Strings(result)
	return describeVersions(result)
}

func msvcVersions(inventory Inventory) []string {
	result := make([]string, 0, len(inventory.MSVC))
	for _, install := range inventory.MSVC {
		result = append(result, install.Version)
	}
	return result
}

func cudaVersions(inventory Inventory) []string {
	result := make([]string, 0, len(inventory.CUDA))
	for _, install := range inventory.CUDA {
		result = append(result, install.Version)
	}
	return result
}
