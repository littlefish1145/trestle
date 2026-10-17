package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"trestle/internal/deps"
)

// packageLevel tracks how deep the shared Packages page currently is. The page
// is a drill-down: a merged package list, then the version and source choices
// for one package, then the provider that owns the chosen install.
type packageLevel int

const (
	// packageListLevel lists every package once, regardless of how many
	// providers offer it.
	packageListLevel packageLevel = iota
	// packageCandidatesLevel lists the version and source options for the
	// selected package, so the user can pick an exact version and origin.
	packageCandidatesLevel
)

// enterPackageByName opens the version picker directly on the package a
// name:version query named, with the requested release already selected. It
// returns false when that package or version is not among the results.
func (model *dashboardModel) enterPackageByName(entries []deps.PackageEntry) bool {
	name := ""
	for _, entry := range entries {
		name = entry.Name
		if entry.Version == model.packageRequested {
			break
		}
	}
	if name == "" {
		return false
	}
	for index, pkg := range model.packagePackages {
		if pkg.Name != name {
			continue
		}
		for position, candidate := range pkg.Candidates {
			if candidate.Version != model.packageRequested {
				continue
			}
			model.packageReturnRoute, model.packageReturnFocus = model.route, model.focus
			model.packageReturnCurs, model.packageReturnScrol = model.cursor, model.scroll
			model.packageSelected = index
			model.packageLevel = packageCandidatesLevel
			model.cursor, model.scroll = position, 0
			return true
		}
	}
	return false
}

// queryName reports the package a result set describes, used to phrase the
// message shown after a name:version search.
func queryName(entries []deps.PackageEntry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Name
}

// enterCandidates drills into the selected package and remembers where the user
// came from, so escape and the return key restore that exact position.
func (model *dashboardModel) enterCandidates() bool {
	index := model.cursor - len(model.config.Packages)
	if index < 0 || index >= len(model.packagePackages) {
		return false
	}
	if len(model.packagePackages[index].Candidates) == 0 {
		model.setMessage("No installable version is available for "+model.packagePackages[index].Name, true)
		return false
	}
	model.packageReturnRoute, model.packageReturnFocus = model.route, model.focus
	model.packageReturnCurs, model.packageReturnScrol = model.cursor, model.scroll
	model.packageSelected = index
	model.packageLevel = packageCandidatesLevel
	model.cursor, model.scroll = 0, 0
	return true
}

// leaveCandidates returns to the package list, restoring the previous cursor.
func (model *dashboardModel) leaveCandidates() {
	model.packageLevel = packageListLevel
	model.route, model.focus = model.packageReturnRoute, model.packageReturnFocus
	model.cursor, model.scroll = model.packageReturnCurs, model.packageReturnScrol
	model.keepCursorVisible()
}

// selectedPackage returns the package row the cursor is on, or false when the
// cursor is over the configured list instead of the search results.
func (model dashboardModel) selectedPackage() (deps.Package, bool) {
	if model.packageLevel != packageListLevel {
		return deps.Package{}, false
	}
	index := model.cursor - len(model.config.Packages)
	if index < 0 || index >= len(model.packagePackages) {
		return deps.Package{}, false
	}
	return model.packagePackages[index], true
}

// selectedCandidate returns the version/source option the cursor is on.
func (model dashboardModel) selectedCandidate() (deps.Package, deps.Candidate, bool) {
	if model.packageLevel != packageCandidatesLevel {
		return deps.Package{}, deps.Candidate{}, false
	}
	if model.packageSelected < 0 || model.packageSelected >= len(model.packagePackages) {
		return deps.Package{}, deps.Candidate{}, false
	}
	pkg := model.packagePackages[model.packageSelected]
	if model.cursor < 0 || model.cursor >= len(pkg.Candidates) {
		return deps.Package{}, deps.Candidate{}, false
	}
	return pkg, pkg.Candidates[model.cursor], true
}

// packageRows is the number of selectable rows on the current level.
func (model dashboardModel) packageRows() int {
	if model.packageLevel == packageCandidatesLevel {
		if model.packageSelected < 0 || model.packageSelected >= len(model.packagePackages) {
			return 0
		}
		return len(model.packagePackages[model.packageSelected].Candidates)
	}
	return len(model.packagePackages)
}

// referenceFor builds the reference the installer records for a candidate.
// Conan needs the version appended to the recipe name (fmt/11.1.4), which is
// also how the provider is detected. vcpkg tracks the version separately from
// the port name, so the bare port name is the correct reference there.
func referenceFor(pkg deps.Package, candidate deps.Candidate) string {
	if candidate.Provider != "conan" || candidate.Version == "" {
		return pkg.Name
	}
	return pkg.Name + "/" + candidate.Version
}

// providerTag renders the provider and source a candidate comes from, so the
// version list makes the origin of each choice obvious.
func providerTag(candidate deps.Candidate) string {
	provider := fallback(candidate.Provider, "vcpkg")
	source := candidate.Source
	if source == "" || source == "local" {
		source = "local cache"
	}
	return provider + " · " + source
}

// packageNameForTest reports the package the cursor resolves to, on either
// level, so navigation tests can assert where the user actually is.
func (model dashboardModel) selectedPackageNameForTest() string {
	if model.packageLevel == packageCandidatesLevel {
		if model.packageSelected >= 0 && model.packageSelected < len(model.packagePackages) {
			return model.packagePackages[model.packageSelected].Name
		}
		return ""
	}
	pkg, ok := model.selectedPackage()
	if !ok {
		return ""
	}
	return pkg.Name
}

// candidateFeatures reports the vcpkg features offered for a package, since
// conan recipes express options differently and the feature editor is vcpkg's.
func candidateFeatures(model dashboardModel, pkg deps.Package) []string {
	for _, candidate := range pkg.Candidates {
		if candidate.Provider != "conan" {
			return candidate.Features
		}
	}
	return nil
}

// packageHint is the footer for whichever level is showing.
func (model dashboardModel) packageHint() string {
	if model.packageLevel == packageCandidatesLevel {
		return "enter install this version   i install   ↑↓ choose version · source   esc return"
	}
	return "/ search   fmt:11.2.0 pins a version   enter choose a version & source   i install newest   a install by name"
}

// packageCandidateLines renders the version and source options for one package.
func (model dashboardModel) packageCandidateLines(p palette, width int) []string {
	pkg, _, ok := model.selectedCandidate()
	if !ok {
		return emptyState(p, "No version selected", "Press esc to return to the package list.")
	}
	lines := candidateHeadingLines(p, width, pkg, model.packageRequested)
	for index, candidate := range pkg.Candidates {
		row := candidateLabel(candidate, model.packageRequested, width)
		if candidate.Version == model.packageRequested {
			lines = append(lines, model.selectableRowHighlighted(p, index, row))
		} else {
			lines = append(lines, model.selectableRow(p, index, row))
		}
		if index == model.cursor {
			lines = append(lines, candidateDetailLines(p, candidate, model.packageRequested)...)
		}
	}
	return lines
}

// candidateHeadingLines is the package name, description and pinned-version
// banner shown above the version list.
func candidateHeadingLines(p palette, width int, pkg deps.Package, requested string) []string {
	lines := []string{
		"",
		p.title.Render("  "+pkg.Name) + "  " + p.faint.Render("choose a version and source"),
	}
	if pkg.Description != "" {
		lines = append(lines, p.faint.Render("  "+ansi.Truncate(pkg.Description, max(12, width-4), "…")))
	}
	if requested != "" {
		lines = append(lines, p.accent.Render("  requested "+requested))
	}
	return append(lines, "")
}

// candidateMarker selects the row marker: a check when the version is already
// installed, a star for the release the user requested, and a circle otherwise.
func candidateMarker(candidate deps.Candidate, requested string) string {
	switch {
	case candidate.Installed:
		return "✓"
	case candidate.Version == requested:
		// ★ marks the release the user asked for with name:version, so it
		// stands out from the rest of the history without moving the cursor.
		return "★"
	}
	return "○"
}

// candidateLabel is the marker, version and provider tag formatted for one
// candidate, truncated to the available width.
func candidateLabel(candidate deps.Candidate, requested string, width int) string {
	version := fallback(candidate.Version, "latest")
	row := fmt.Sprintf("%s  %-16s %s", candidateMarker(candidate, requested), version, providerTag(candidate))
	return ansi.Truncate(row, width-3, "…")
}

// candidateDetailLines describes the candidate under the cursor: whether it was
// requested, when it was updated, its license and URL, and cache availability.
func candidateDetailLines(p palette, candidate deps.Candidate, requested string) []string {
	var lines []string
	var detail []string
	if candidate.Version == requested {
		detail = append(detail, "requested")
	}
	if candidate.Updated != "" {
		detail = append(detail, "updated "+candidate.Updated)
	}
	if candidate.License != "" {
		detail = append(detail, "license "+candidate.License)
	}
	if candidate.PackageURL != "" {
		detail = append(detail, "details "+candidate.PackageURL)
	}
	if len(detail) > 0 {
		lines = append(lines, p.faint.Render("     "+strings.Join(detail, " · ")))
	}
	if candidate.Installed {
		lines = append(lines, p.good.Render("     already available in the local cache"))
	}
	return lines
}
