package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
	"trestle/internal/deps"
)

func packageModelWith(t *testing.T, entries []deps.PackageEntry) dashboardModel {
	t.Helper()
	model := newDashboard("trestle.toml", Services{})
	model.width, model.height, model.probing = 120, 30, false
	model.route, model.focus = PackagesRoute, focusContent
	model.config.Packages = map[string]config.Package{}
	model.packageResults = entries
	model.packagePackages = deps.Merge(entries)
	model.cursor = len(model.config.Packages)
	return model
}

// pressKey delivers one key through handleKey, matching how the dashboard
// routes keys in the real program.
func pressKey(t *testing.T, model dashboardModel, key tea.Key) dashboardModel {
	t.Helper()
	next, _ := pressKeyCmd(t, model, key)
	return next
}

// pressKeyCmd also runs the returned command. Installing happens inside the
// tea.Cmd, so tests that assert on the installer must execute it.
func pressKeyCmd(t *testing.T, model dashboardModel, key tea.Key) (dashboardModel, tea.Cmd) {
	t.Helper()
	updated, command := model.handleKey(tea.KeyPressMsg(key))
	next, ok := updated.(dashboardModel)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	return next, command
}

func pressEnter(t *testing.T, model dashboardModel) dashboardModel {
	t.Helper()
	return pressKey(t, model, tea.Key{Code: tea.KeyEnter})
}

// pressEnterAndRun installs: it delivers enter, then starts the commands the
// dashboard returned and waits for the install to finish.
func pressEnterAndRun(t *testing.T, model dashboardModel) dashboardModel {
	t.Helper()
	return pressKeyAndRun(t, model, tea.Key{Code: tea.KeyEnter})
}

// pressKeyAndRun delivers any key, then starts the returned commands and waits
// for the installation to complete. installPackage wraps the installer in a
// tea.Batch whose first member blocks until the first event arrives, so the
// batch runs in the background and the assertion waits on the install channel.
func pressKeyAndRun(t *testing.T, model dashboardModel, key tea.Key) dashboardModel {
	t.Helper()
	next, command := pressKeyCmd(t, model, key)
	if command == nil {
		t.Fatal("the key did not start an installation")
	}
	go startBatch(t, command)
	if next.installEvents == nil {
		t.Fatal("installation did not open an event channel")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-next.installEvents:
			if event.done {
				return next
			}
		case <-deadline:
			t.Fatal("installer did not finish")
		}
	}
}

// startBatch runs every member of a batched command without waiting for them:
// one member blocks until the installer reports its first event, so waiting here
// would deadlock against the channel read below.
func startBatch(t *testing.T, command tea.Cmd) {
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, inner := range batch {
		if inner == nil {
			continue
		}
		go func(inner tea.Cmd) { _ = inner() }(inner)
	}
}

func pressEsc(t *testing.T, model dashboardModel) dashboardModel {
	t.Helper()
	return pressKey(t, model, tea.Key{Code: tea.KeyEscape})
}

func pressDown(t *testing.T, model dashboardModel) dashboardModel {
	t.Helper()
	return pressKey(t, model, tea.Key{Code: tea.KeyDown})
}

func TestPackagePageMergesSameNameAcrossProviders(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Source: "vcpkg.io"},
		{Name: "zlib", Version: "1.3.1", Provider: "conan", Source: "local", Installed: true},
	})
	view := ansi.Strip(model.View().Content)
	if strings.Count(view, " fmt ") > 1 {
		t.Fatalf("the same package must be listed once:\n%s", view)
	}
	// The merged row advertises that both providers offer it.
	if !strings.Contains(view, "conan + vcpkg") {
		t.Fatalf("merged row does not show both providers:\n%s", view)
	}
}

func TestPackageDrillDownChoosesVersionAndSource(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "10.2.1", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Source: "vcpkg.io"},
	})
	// Level 1: the package list.
	if model.packageLevel != packageListLevel {
		t.Fatalf("expected the package list first, got level %d", model.packageLevel)
	}
	model = pressEnter(t, model)
	if model.packageLevel != packageCandidatesLevel {
		t.Fatalf("enter did not drill into the version list: %d", model.packageLevel)
	}
	view := ansi.Strip(model.View().Content)
	// Level 2 shows each version with its provider and source.
	for _, want := range []string{"11.1.4", "conan", "conancenter", "11.0.0", "vcpkg"} {
		if !strings.Contains(view, want) {
			t.Fatalf("version list is missing %q:\n%s", want, view)
		}
	}
}

func TestPackageEscapeReturnsToPreviousLevelAndPosition(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "aaa", Version: "1.0.0", Provider: "conan", Source: "conancenter"},
		{Name: "bbb", Version: "2.0.0", Provider: "conan", Source: "conancenter"},
	})
	model.cursor = 1 // point at "bbb"
	before := model.cursor
	model = pressEnter(t, model)
	if model.packageLevel != packageCandidatesLevel {
		t.Fatal("enter did not open the version list")
	}
	if model.selectedPackageNameForTest() != "bbb" {
		t.Fatalf("drill-down opened the wrong package: %q", model.selectedPackageNameForTest())
	}
	model = pressEsc(t, model)
	if model.packageLevel != packageListLevel {
		t.Fatalf("esc did not return to the package list: %d", model.packageLevel)
	}
	if model.cursor != before {
		t.Fatalf("cursor was not restored: got %d want %d", model.cursor, before)
	}
	if name := model.selectedPackageNameForTest(); name != "bbb" {
		t.Fatalf("esc did not restore the selected package: %q", name)
	}
}

func TestPackageBackspaceAlsoReturns(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{{Name: "fmt", Version: "11.1.4", Provider: "conan"}})
	model = pressEnter(t, model)
	model = pressKey(t, model, tea.Key{Code: tea.KeyBackspace})
	if model.packageLevel != packageListLevel {
		t.Fatal("backspace did not return to the package list")
	}
}

func TestEscapeOnPackageListDoesNothingHarmful(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{{Name: "fmt", Version: "11.1.4", Provider: "conan"}})
	model = pressEsc(t, model)
	if model.route != PackagesRoute {
		t.Fatalf("esc left the packages route: %v", model.route)
	}
	if model.packageLevel != packageListLevel {
		t.Fatal("esc changed the level without a drill-down")
	}
}

func TestPackageEnterOnCandidateInstallsExactReference(t *testing.T) {
	var installed string
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Source: "vcpkg.io"},
	})
	model.services.InstallPackage = func(_ context.Context, name string, _ func(string)) error {
		installed = name
		return nil
	}
	model = pressEnter(t, model)
	// The newest conan recipe leads its provider group, so the reference must
	// carry the version: a bare "fmt" would install an arbitrary version.
	model = pressEnterAndRun(t, model)
	if installed != "fmt/11.1.4" {
		t.Fatalf("conan install reference = %q, want fmt/11.1.4", installed)
	}
}

func TestPackageVcpkgCandidateInstallsBarePortName(t *testing.T) {
	var installed string
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Source: "vcpkg.io"},
	})
	model.services.InstallPackage = func(_ context.Context, name string, _ func(string)) error {
		installed = name
		return nil
	}
	model = pressEnter(t, model)
	// Move to the vcpkg candidate, which sorts after the conan group.
	model = pressDown(t, model)
	model = pressDown(t, model)
	model = pressEnterAndRun(t, model)
	if installed != "fmt" {
		t.Fatalf("vcpkg install reference = %q, want fmt", installed)
	}
}

func TestReferenceForBuildsProviderCorrectReferences(t *testing.T) {
	pkg := deps.Package{Name: "fmt"}
	if got := referenceFor(pkg, deps.Candidate{Provider: "conan", Version: "11.1.4"}); got != "fmt/11.1.4" {
		t.Fatalf("conan reference = %q", got)
	}
	if got := referenceFor(pkg, deps.Candidate{Provider: "vcpkg", Version: "11.0.0"}); got != "fmt" {
		t.Fatalf("vcpkg reference = %q", got)
	}
	if got := referenceFor(pkg, deps.Candidate{Provider: "conan"}); got != "fmt" {
		t.Fatalf("conan reference without a version = %q", got)
	}
}

func TestProviderSummaryListsEveryProviderOnce(t *testing.T) {
	pkg := deps.Package{Name: "fmt", Candidates: []deps.Candidate{
		{Provider: "conan"}, {Provider: "conan"}, {Provider: "vcpkg"},
	}}
	if got := providerSummary(pkg); got != "conan + vcpkg" {
		t.Fatalf("provider summary = %q", got)
	}
	empty := deps.Package{Name: "fmt"}
	if got := providerSummary(empty); got != "no versions" {
		t.Fatalf("empty provider summary = %q", got)
	}
}

// On the package list "i" must install straight away, preferring vcpkg even
// when conan offers a newer version.
func TestPackageIInstallsNewestWithoutDrillDown(t *testing.T) {
	var installed string
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "12.2.0", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"},
	})
	model.services.InstallPackage = func(_ context.Context, name string, _ func(string)) error {
		installed = name
		return nil
	}
	model = pressKeyAndRun(t, model, tea.Key{Code: 'i', Text: "i"})
	if model.packageLevel != packageListLevel {
		t.Fatal("i must install in place rather than entering the version picker")
	}
	if installed != "fmt" {
		t.Fatalf("vcpkg-first install reference = %q, want fmt", installed)
	}
}

func TestPackageIOnCandidateInstallsThatVersion(t *testing.T) {
	var installed string
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "10.2.1", Provider: "conan", Source: "conancenter"},
	})
	model.services.InstallPackage = func(_ context.Context, name string, _ func(string)) error {
		installed = name
		return nil
	}
	model = pressEnter(t, model)
	model = pressDown(t, model)
	model = pressKeyAndRun(t, model, tea.Key{Code: 'i', Text: "i"})
	if installed != "fmt/10.2.1" {
		t.Fatalf("install reference = %q, want fmt/10.2.1", installed)
	}
}

func TestPackageIWithoutCandidatesReportsInstead(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"},
	})
	model.packagePackages[0].Candidates = nil
	model.services.InstallPackage = func(_ context.Context, _ string, _ func(string)) error {
		t.Error("install ran without a candidate")
		return nil
	}
	model = pressKey(t, model, tea.Key{Code: 'i', Text: "i"})
	if !model.messageError {
		t.Fatalf("expected an error message, got %q", model.message)
	}
}

// A name:version search must land directly on that release, highlighted.
func TestNameVersionSearchSelectsRequestedVersion(t *testing.T) {
	entries := []deps.PackageEntry{
		{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"},
		{Name: "fmt", Version: "11.0.2", Provider: "vcpkg", Source: "vcpkg.io"},
		{Name: "fmt", Version: "10.2.1", Provider: "vcpkg", Source: "vcpkg.io"},
	}
	model := packageModelWith(t, nil)
	model.packageRequested = "10.2.1"
	updated, _ := model.Update(packageSearchMessage{ports: entries})
	model = updated.(dashboardModel)
	if model.packageLevel != packageCandidatesLevel {
		t.Fatal("a pinned version should open the version picker")
	}
	_, candidate, ok := model.selectedCandidate()
	if !ok || candidate.Version != "10.2.1" {
		t.Fatalf("selected candidate = %+v (ok=%v), want 10.2.1", candidate, ok)
	}
}

func TestNameVersionSearchFallsBackToListWhenMissing(t *testing.T) {
	entries := []deps.PackageEntry{{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"}}
	model := packageModelWith(t, nil)
	model.packageRequested = "1.0.0"
	updated, _ := model.Update(packageSearchMessage{ports: entries})
	model = updated.(dashboardModel)
	if model.packageLevel != packageListLevel {
		t.Fatal("an unavailable version must leave the user on the package list")
	}
	if len(model.packagePackages) != 1 {
		t.Fatalf("expected the package list, got %+v", model.packagePackages)
	}
}

func TestRequestedVersionIsHighlightedAndMarked(t *testing.T) {
	model := packageModelWith(t, []deps.PackageEntry{
		{Name: "fmt", Version: "11.2.0", Provider: "vcpkg"},
		{Name: "fmt", Version: "10.2.1", Provider: "vcpkg"},
	})
	model.packageRequested = "10.2.1"
	model.packageLevel = packageCandidatesLevel
	lines := ansi.Strip(strings.Join(model.packageCandidateLines(newPalette(false), 100), "\n"))
	if !strings.Contains(lines, "★") {
		t.Fatalf("requested version is not marked:\n%s", lines)
	}
	if !strings.Contains(lines, "requested 10.2.1") {
		t.Fatalf("requested version header is missing:\n%s", lines)
	}
}
