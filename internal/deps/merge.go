package deps

import (
	"sort"
	"strings"
)

// Candidate is one installable (provider, version, source) option for a
// package. The Packages page lists packages, then lets the user pick one of
// these to install, so every provider contributes candidates to the same
// package row instead of appearing as a separate entry.
type Candidate struct {
	Version      string
	Provider     string
	Source       string
	Installed    bool
	Updated      string
	License      string
	PackageURL   string
	Description  string
	Features     []string
	Dependencies []string
}

// Package groups every candidate found for one package name, across providers
// and sources. A name present in both vcpkg and conan yields one Package with
// candidates from each, so the UI never shows the same library twice.
type Package struct {
	Name        string
	Description string
	Installed   bool
	Candidates  []Candidate
}

// Merge collapses flat provider results into one Package per name. Candidates
// are ordered newest-first per provider so the default pick is the newest
// available version, and installed candidates lead their own provider group.
func Merge(entries []PackageEntry) []Package {
	index := map[string]int{}
	var packages []Package
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		position, ok := index[name]
		if !ok {
			index[name] = len(packages)
			packages = append(packages, Package{Name: name, Description: entry.Description, Installed: entry.Installed})
			position = len(packages) - 1
		}
		target := &packages[position]
		if target.Description == "" {
			target.Description = entry.Description
		}
		target.Installed = target.Installed || entry.Installed
		target.Candidates = append(target.Candidates, Candidate{
			Version:      entry.Version,
			Provider:     entry.Provider,
			Source:       entry.Source,
			Installed:    entry.Installed,
			Updated:      entry.Updated,
			License:      entry.License,
			PackageURL:   entry.PackageURL,
			Description:  entry.Description,
			Features:     entry.Features,
			Dependencies: entry.Dependencies,
		})
	}
	for i := range packages {
		sortCandidates(packages[i].Candidates)
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].Installed != packages[j].Installed {
			return packages[i].Installed
		}
		return packages[i].Name < packages[j].Name
	})
	return packages
}

// Preferred returns the candidate the quick-install action should use: the
// newest vcpkg release when vcpkg offers the package, otherwise the newest
// release from whichever provider does. vcpkg wins because it is the toolchain
// trestle drives by default, and its binary is usually already configured.
//
// A version the user explicitly requested wins over the provider preference, so
// "fmt:10.2.1" installs that release even when a newer vcpkg port exists.
func (pkg Package) Preferred(requested string) (Candidate, bool) {
	wanted := strings.TrimSpace(requested)
	if base, _, found := strings.Cut(wanted, "#"); found {
		wanted = strings.TrimSpace(base)
	}
	if wanted != "" {
		for _, candidate := range pkg.Candidates {
			if candidate.Version == wanted {
				return candidate, true
			}
		}
	}
	best := pkg.bestVcpkg()
	if best < 0 {
		best = pkg.bestOverall()
	}
	if best < 0 {
		return Candidate{}, false
	}
	return pkg.Candidates[best], true
}

// bestVcpkg and bestOverall scan the candidate list independently, so the
// provider preference is applied by choosing between two winners rather than by
// filtering candidates in a single pass.
func (pkg Package) bestVcpkg() int {
	best := -1
	for index, candidate := range pkg.Candidates {
		if candidate.Provider != "vcpkg" {
			continue
		}
		if best < 0 || compareVersion(candidate.Version, pkg.Candidates[best].Version) > 0 {
			best = index
		}
	}
	return best
}

func (pkg Package) bestOverall() int {
	best := -1
	for index, candidate := range pkg.Candidates {
		if best < 0 || compareVersion(candidate.Version, pkg.Candidates[best].Version) > 0 {
			best = index
		}
	}
	return best
}

// sortCandidates orders each provider group newest-version-first, keeping
// already-installed options above the rest so the current state is obvious.
func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Provider != candidates[j].Provider {
			return candidates[i].Provider < candidates[j].Provider
		}
		if candidates[i].Installed != candidates[j].Installed {
			return candidates[i].Installed
		}
		return compareVersion(candidates[i].Version, candidates[j].Version) > 0
	})
}

// compareVersion reports whether left is newer than right. Version strings come
// from different providers, so an unparsable value falls back to reverse
// lexical order rather than interleaving across providers.
func compareVersion(left, right string) int {
	leftParts, leftOK := versionNumbers(left)
	rightParts, rightOK := versionNumbers(right)
	if !leftOK || !rightOK {
		return strings.Compare(right, left)
	}
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		a, b := 0, 0
		if index < len(leftParts) {
			a = leftParts[index]
		}
		if index < len(rightParts) {
			b = rightParts[index]
		}
		if a != b {
			return a - b
		}
	}
	return 0
}

func versionNumbers(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	fields := strings.Split(value, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			return nil, false
		}
		number := 0
		for _, char := range field {
			if char < '0' || char > '9' {
				return nil, false
			}
			number = number*10 + int(char-'0')
		}
		parts = append(parts, number)
	}
	return parts, true
}
