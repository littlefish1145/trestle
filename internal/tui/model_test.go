package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
	"trestle/internal/toolchain"
)

func TestModelTransitions(t *testing.T) {
	model := New("demo", "", "clang++", nil)
	model.Update(NextAction, "")
	if model.State().Tab != ToolchainTab {
		t.Fatalf("unexpected tab: %v", model.State().Tab)
	}
	model.Update(ToggleEditAction, "")
	model.Update(InputAction, "g++")
	if model.State().Compiler != "g++" || model.State().Editing {
		t.Fatalf("input did not commit: %#v", model.State())
	}
	model.Update(ConfirmAction, "")
	if !model.State().Done {
		t.Fatal("confirm did not finish")
	}
}

func TestDashboardNavigationAndFocus(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(dashboardModel)
	if model.route != TargetsRoute {
		t.Fatalf("down should select Targets, got %s", model.route)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	model = updated.(dashboardModel)
	if model.focus != focusContent {
		t.Fatal("tab should move focus to content")
	}
	model.config.Targets = map[string]config.Target{"z": {}, "a": {}}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(dashboardModel)
	if model.cursor != 1 {
		t.Fatalf("down should move row cursor, got %d", model.cursor)
	}
}

func TestDashboardInputSupportsEditing(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.openInput("package", "Add package")
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "fmt"}))
	model = updated.(dashboardModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	model = updated.(dashboardModel)
	if model.inputText != "fm" {
		t.Fatalf("backspace should edit input, got %q", model.inputText)
	}
}

func TestDashboardCompactViewContainsCoreStatus(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.width, model.height, model.probing = 68, 20, false
	model.probe.ProjectName = "demo"
	model.config.Build.Profile = "debug"
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"TRESTLE", "demo", "Project overview", "b build", "? help"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("compact view missing %q:\n%s", expected, view)
		}
	}
}

func TestDashboardRowsAreStable(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.route = TargetsRoute
	model.config.Targets = map[string]config.Target{"zeta": {}, "alpha": {}}
	view := ansi.Strip(model.View().Content)
	if strings.Index(view, "alpha") > strings.Index(view, "zeta") {
		t.Fatalf("target rows should be sorted:\n%s", view)
	}
}

func TestDashboardKeepsCompleteInstallLogAndSupportsMouseScroll(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.width, model.height, model.probing = 120, 20, false
	model.route, model.focus = PackagesRoute, focusNavigation
	for index := 0; index < 40; index++ {
		updated, _ := model.Update(installEvent{line: fmt.Sprintf("install line %02d", index)})
		model = updated.(dashboardModel)
	}
	if len(model.installLog) != 40 {
		t.Fatalf("install log was truncated: %d lines", len(model.installLog))
	}
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(dashboardModel)
	if model.focus != focusContent || model.scroll == 0 {
		t.Fatalf("mouse wheel did not scroll content: focus=%d scroll=%d", model.focus, model.scroll)
	}
	updated, _ = model.Update(tea.MouseClickMsg(tea.Mouse{X: 80, Y: 8, Button: tea.MouseLeft}))
	model = updated.(dashboardModel)
	if model.focus != focusContent {
		t.Fatal("mouse click did not focus content")
	}
}

func TestDashboardTestFileViewMapsBackToJob(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.route, model.focus, model.testFileMode = TestsRoute, focusContent, true
	model.probe.Tests = []testJob{
		{Name: "network_tests", Group: "integration", Files: []string{"tests/z_network.cpp"}},
		{Name: "math_tests", Group: "unit", Files: []string{"tests/a_math.cpp"}},
	}
	if model.itemCount() != 2 {
		t.Fatalf("unexpected file count: %d", model.itemCount())
	}
	job, ok := model.selectedTestJob()
	if !ok || job.Name != "math_tests" || job.Group != "unit" {
		t.Fatalf("file selection did not resolve its job: %#v", job)
	}
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"FILE VIEW", "a_math.cpp", "network_tests", "g group"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("test view missing %q:\n%s", expected, view)
		}
	}
}

func TestDashboardExposesTargetBuildAndCompilerControls(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.width, model.height = 140, 40
	model.config.Targets = map[string]config.Target{"peekg": {Type: "executable"}, "gui": {Type: "executable"}}
	model.config.Build.DefaultTargets = []string{"peekg"}
	model.route = TargetsRoute
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"peekg", "★", "build selected", "build all"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("target view missing %q:\n%s", expected, view)
		}
	}
	model.route = ToolchainsRoute
	model.config.Toolchain.Mode = "wsl"
	model.config.Toolchain.Preset = "fast"
	view = ansi.Strip(model.View().Content)
	for _, expected := range []string{"WSL ·", "fast", "native or WSL", "apply preset"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("toolchain view missing %q:\n%s", expected, view)
		}
	}
}

func TestDashboardTreatsWSLCompilerAsSelectable(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.cursor = 0
	model.probe.Components = []toolchain.Component{{Name: "clang++ @ Ubuntu", Family: "WSL", Path: "/usr/bin/clang++", Ready: true, Execution: "wsl", Distribution: "Ubuntu"}}
	component, ok := model.selectedCompiler()
	if !ok || component.Distribution != "Ubuntu" {
		t.Fatalf("WSL compiler was not selectable: %#v", component)
	}
}

func TestCompactLineRemovesEmbeddedConsoleLines(t *testing.T) {
	if got := compactLine("wsl: warning\r\nGNU g++\x00 14.2"); got != "wsl: warning GNU g++ 14.2" {
		t.Fatalf("unexpected compact text %q", got)
	}
}

func TestDashboardSettingsAndReleasePages(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.width, model.height = 140, 42
	model.config = config.Default("demo")
	model.config.CompilerPresets["fast"] = config.CompilerPreset{CXX: "clang++", CXXFlags: []string{"-march=native"}}
	model.route = SettingsRoute
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Project name", "C++ compiler", "vcpkg triplet", "[app] Sources", "edit selected field"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("settings missing %q:\n%s", expected, view)
		}
	}
	foundPreset := false
	for _, row := range settingsRows(model.config) {
		if row.label == "{preset fast} C++ compiler" {
			foundPreset = true
		}
	}
	if !foundPreset {
		t.Fatal("compiler preset fields are missing from Settings")
	}
	model.route = ReleaseRoute
	view = ansi.Strip(model.View().Content)
	for _, expected := range []string{"Release", "balanced", "speed", "size", "release ZIP"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("release missing %q:\n%s", expected, view)
		}
	}
}
