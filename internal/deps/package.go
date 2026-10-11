package deps

import (
	"strings"

	"trestle/internal/deps/conan"
	"trestle/internal/deps/vcpkg"
)

// PackageEntry is the provider-neutral package record rendered by the shared
// Packages page. Provider names the package manager that produced the entry so
// the page can label results from either vcpkg or conan without special cases.
type PackageEntry struct {
	Name         string
	Version      string
	Description  string
	Installed    bool
	Provider     string
	Source       string
	Homepage     string
	PackageURL   string
	License      string
	Updated      string
	Features     []string
	Dependencies []string
	// Requested marks the version the user asked for by typing "name:version",
	// so the page can highlight it among the other releases.
	Requested bool
}

// ConvertEntries adapts vcpkg search results to the neutral view. The vcpkg
// client stamps Source with the local marker for installed-tree lookups, which
// the shared page renders alongside the provider name.
func ConvertEntries(values []vcpkg.Port) []PackageEntry {
	result := make([]PackageEntry, 0, len(values))
	for _, value := range values {
		result = append(result, PackageEntry{
			Name:         value.Name,
			Version:      value.Version,
			Description:  value.Description,
			Installed:    value.Installed,
			Provider:     "vcpkg",
			Source:       value.Source,
			Homepage:     value.Homepage,
			PackageURL:   value.PackageURL,
			License:      value.License,
			Updated:      value.Updated,
			Features:     value.Features,
			Dependencies: value.Dependencies,
		})
	}
	return result
}

// ConvertPortVersionEntries adapts vcpkg version-database results to the neutral
// view, so a historical release is offered the same way a current one is. The
// source records that the version came from the port history rather than the
// working tree, so the page can label it.
func ConvertPortVersionEntries(values []vcpkg.PortVersion) []PackageEntry {
	result := make([]PackageEntry, 0, len(values))
	for _, value := range values {
		source := "vcpkg.io"
		if value.Current {
			source = "current"
		}
		result = append(result, PackageEntry{
			Name:         value.Port,
			Version:      value.Version,
			Description:  value.Description,
			Installed:    value.Installed,
			Provider:     "vcpkg",
			Source:       source,
			Homepage:     value.Homepage,
			PackageURL:   value.PackageURL,
			License:      value.License,
			Updated:      value.Date,
			Dependencies: value.Dependencies,
		})
	}
	return result
}

// MarkRequestedVersion flags every entry whose version matches the one the user
// typed, so the Packages page can highlight it. A "#n" port-version suffix is
// accepted and matched against the plain version too.
func MarkRequestedVersion(entries []PackageEntry, version string) {
	wanted := strings.TrimSpace(version)
	if base, _, found := strings.Cut(wanted, "#"); found {
		wanted = strings.TrimSpace(base)
	}
	if wanted == "" {
		return
	}
	for index := range entries {
		if entries[index].Version == wanted {
			entries[index].Requested = true
		}
	}
}

// ConvertConanEntries adapts conan search results to the same neutral view.
// Conan stamps Source with "local" for cache hits or the remote name, so the
// shared page can show where each recipe came from.
func ConvertConanEntries(values []conan.Package) []PackageEntry {
	result := make([]PackageEntry, 0, len(values))
	for _, value := range values {
		result = append(result, PackageEntry{
			Name:         value.Name,
			Version:      value.Version,
			Description:  value.Description,
			Installed:    value.Installed,
			Provider:     "conan",
			Source:       value.Source,
			Homepage:     value.Homepage,
			PackageURL:   value.PackageURL,
			License:      value.License,
			Updated:      value.Updated,
			Features:     value.Features,
			Dependencies: value.Dependencies,
		})
	}
	return result
}
