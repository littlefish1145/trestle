package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"trestle/internal/runlog"
)

var viewHeading = map[Route]string{
	OverviewRoute: "Project overview", TargetsRoute: "Targets", TasksRoute: "Tasks",
	DependenciesRoute: "Dependency graph", ToolchainsRoute: "Development environment",
	PresetsRoute: "Compiler presets", ImportRoute: "Project import", PackagesRoute: "Packages",
	TestsRoute: "Test suites", BuildRoute: "Build", ReleaseRoute: "Release",
	DoctorRoute: "Doctor", SettingsRoute: "Settings",
}

// Every screen renders its heading on the standard console geometry.
func TestViewRoutesPanorama(t *testing.T) {
	base, _ := freshConsole(t)
	for _, route := range routes {
		model := base
		model.route = route
		view := model.View()
		if !view.AltScreen || view.WindowTitle != "Trestle · Project Console" {
			t.Fatalf("view flags wrong for %v: %+v", route, view)
		}
		if rendered := view.Content; !strings.Contains(compactLine(rendered), viewHeading[route]) {
			t.Fatalf("route %v lost its heading in %q", route, compactLine(rendered))
		}
	}
}

// Overlays replace the page: help, details, history and the full log.
func TestViewOverlayPanorama(t *testing.T) {
	base, _ := freshConsole(t)
	base.workflowLog = []string{"alpha", "beta"}

	help := base
	help.help = true
	if !strings.Contains(help.renderDashboard(help.palette()), "Keyboard reference") {
		t.Fatal("help overlay missing")
	}
	details := base
	details.details, details.detailText = true, "the whole truth"
	if !strings.Contains(details.renderDashboard(details.palette()), "the whole truth") {
		t.Fatal("details overlay missing")
	}
	history := base
	history.history = true
	if !strings.Contains(history.renderDashboard(history.palette()), "No saved operations yet.") {
		t.Fatal("empty history wording missing")
	}
	wrapped := history.renderDashboard(history.palette())
	if history.overlayScroll > 0 && !strings.Contains(wrapped, "passed") {
		t.Fatalf("scroll clipped the history rows")
	}
	history.runHistory = []runlog.Record{
		{ID: "id-1", Status: "passed", Operation: "Build", Started: time.Now().Local()},
	}
	historyRendered := history.renderDashboard(history.palette())
	if !strings.Contains(compactLine(historyRendered), "passed Build") {
		t.Fatalf("history row missing: %q", compactLine(historyRendered))
	}
	fullLog := base
	fullLog.fullLog = true
	if !strings.Contains(fullLog.renderDashboard(fullLog.palette()), "alpha") {
		t.Fatal("full log lost its lines")
	}
}

// The content panel frames the heading and highlights focus with the border.
func TestContentPanelFraming(t *testing.T) {
	model, _ := freshConsole(t)
	model.route = OverviewRoute
	model.focus = focusNavigation
	pane := model.contentPanel(model.palette(), 110, 34)
	if !strings.Contains(compactLine(pane), "Project overview") {
		t.Fatalf("panel heading missing: %q", compactLine(pane))
	}
	model.focus = focusContent
	if pane == model.contentPanel(model.palette(), 110, 34) {
		t.Fatal("content focus must change the border")
	}
	if blank := model.contentPanel(model.palette(), 30, 20); strings.Contains(blank, "panic") {
		t.Fatal("narrow panel misbehaved")
	}
	if rows := emptyState(model.palette(), "Nothing here", "esc returns"); len(rows) != 3 || !strings.Contains(compactLine(strings.Join(rows, "\n")), "Nothing here") {
		t.Fatalf("empty state rows = %v", rows)
	}
}

// The status line mirrors activity, errors and the input prompt.
func TestStatusAndActivityLabels(t *testing.T) {
	base, _ := freshConsole(t)
	base.message = "All good"
	calm := base.renderDashboard(base.palette())
	if !strings.Contains(compactLine(calm), "Ready") {
		t.Fatal("calm banner lost 'Ready'")
	}

	busy := base
	busy.building, busy.message, busy.messageError = true, "Building now", false
	if label := busy.activityLabel(); !strings.Contains(label, "Building") {
		t.Fatalf("building label = %q", label)
	}
	searching := base
	searching.searching = true
	if label := searching.activityLabel(); !strings.Contains(label, "Searching") {
		t.Fatalf("searching label = %q", label)
	}
	mutating := base
	mutating.mutating, mutating.operation = true, "Saving changes"
	if label := mutating.activityLabel(); !strings.Contains(label, "Saving changes") {
		t.Fatalf("mutating label = %q", label)
	}
	if tone := mutating.activityTone(); tone != "accent" {
		t.Fatalf("busy tone = %q", tone)
	}
	attention := base
	attention.messageError, attention.message = true, "went wrong"
	if label, tone := attention.activityLabel(), attention.activityTone(); label != "Needs attention" || tone != "danger" {
		t.Fatalf("attention = %q %q", label, tone)
	}

	errored := base
	errored.probe.Error, errored.messageError, errored.message = errors.New("probe broke"), true, "broken"
	if !strings.Contains(errored.activityLabel(), "Needs attention") {
		t.Fatal("probe error must draw the attention tone")
	}

	blank := base
	blank.probe, blank.config.Build.Profile = probeMessage{}, ""
	if !strings.Contains(blank.renderDashboard(blank.palette()), "Loading project") {
		t.Fatal("missing project name must show the loading banner")
	}
}

// Input labels cover every kind; the status line renders the active prompt.
func TestInputLabelsAndPrompt(t *testing.T) {
	want := map[string]string{
		"search": "Search packages", "settings-search": "Search settings", "log-search": "Search complete output",
		"package": "Add package", "install": "Install package", "features": "Package features",
		"remove": "Remove package", "version": "Package version", "flags": "Compiler flags",
		"c-flags": "C flags", "cxx-flags": "C++ flags", "link-flags": "Link flags",
		"apply-preset": "Apply preset", "save-preset": "Save preset", "setting": "Project setting",
		"release-output": "ZIP output", "release-targets": "Release targets", "environment": "Setup script",
		"cuda": "CUDA root", "cuda-wsl": "WSL CUDA root", "test-group": "Test group",
	}
	base, _ := freshConsole(t)
	for kind, label := range want {
		model := base
		model.inputMode, model.inputKind = true, kind
		if got := model.inputLabel(); got != label {
			t.Fatalf("kind %q label = %q", kind, got)
		}
		rendered := model.statusLine(model.palette(), 120)
		if !strings.Contains(compactLine(rendered), label) {
			t.Fatalf("kind %q prompt lost its label: %q", kind, compactLine(rendered))
		}
	}
	unknown := base
	unknown.inputMode, unknown.inputKind = true, "mystery"
	if got := unknown.inputLabel(); got != "Input" {
		t.Fatalf("unknown label = %q", got)
	}
}

// The help overlay frames the reference; Init primes the background commands.
func TestHelpViewAndInit(t *testing.T) {
	model, _ := freshConsole(t)
	pane := model.helpView(model.palette(), 110, 34)
	if !strings.Contains(compactLine(pane), "Keyboard reference") || !strings.Contains(pane, "Ctrl+P") || !strings.Contains(pane, "Ctrl+U clear") {
		t.Fatalf("help view incomplete: %q", compactLine(pane))
	}
	if narrow := model.helpView(model.palette(), 24, 12); len(compactLine(narrow)) == 0 {
		t.Fatal("narrow help view collapsed")
	}
	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init must prim the background commands")
	}
}
