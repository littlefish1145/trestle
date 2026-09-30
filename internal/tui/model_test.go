package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
	"trestle/internal/policy"
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

func TestTaskRequiresExplicitConfirmation(t *testing.T) {
	model := newDashboard("trestle.toml", Services{
		PreviewTask: func(_ context.Context, _ string) (config.TaskPreview, error) {
			return config.TaskPreview{Task: config.Task{Command: []string{"go", "env"}, Set: map[string]string{"build.profile": "release"}}}, nil
		},
		RunTask: func(_ context.Context, _ string, _ config.TaskPreview, _ func(string)) error { return nil },
	})
	model.probing = false
	model.route, model.focus = TasksRoute, focusContent
	model.config.Tasks = map[string]config.Task{"prepare": {Command: []string{"go", "version"}, Set: map[string]string{"build.profile": "release"}}}
	updated, command := model.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(dashboardModel)
	if model.pendingTask != "prepare" || command != nil || model.mutating {
		t.Fatal("task ran without confirmation")
	}
	previewView := ansi.Strip(strings.Join(model.taskLines(newPalette(), 100), "\n"))
	if !strings.Contains(previewView, `"env"`) || strings.Contains(previewView, `"version"`) {
		t.Fatalf("task review did not show the previewed command: %s", previewView)
	}
	updated, _ = model.handleKey(tea.KeyPressMsg(tea.Key{Text: "n"}))
	model = updated.(dashboardModel)
	if model.pendingTask != "" || model.mutating {
		t.Fatal("task was not cancelled")
	}
	updated, _ = model.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(dashboardModel)
	updated, command = model.handleKey(tea.KeyPressMsg(tea.Key{Text: "y"}))
	model = updated.(dashboardModel)
	if model.pendingTask != "" || !model.mutating || command == nil {
		t.Fatal("confirmed task did not start")
	}
}

func TestRunningTaskCanBeCancelledWithoutLeavingDashboard(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing, model.mutating = false, true
	model.workflowRoute = TasksRoute
	cancelled := false
	model.workflowCancel = func() { cancelled = true }
	updated, command := model.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	model = updated.(dashboardModel)
	if command != nil || !cancelled || !model.mutating || !strings.Contains(model.message, "Cancelling") {
		t.Fatalf("task cancellation did not stay in dashboard: %+v", model)
	}
}

func TestQuitWaitsForTaskCancellation(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing, model.mutating = false, true
	model.workflowRoute = TasksRoute
	cancelled := false
	model.workflowCancel = func() { cancelled = true }
	updated, command := model.handleKey(tea.KeyPressMsg(tea.Key{Text: "q"}))
	model = updated.(dashboardModel)
	if command != nil || !cancelled || !model.quitAfterTask {
		t.Fatal("dashboard exited before task cleanup")
	}
	updated, command = model.Update(workflowEvent{label: "Task prepare", done: true, err: context.Canceled})
	model = updated.(dashboardModel)
	if command == nil || model.mutating || model.quitAfterTask {
		t.Fatal("dashboard did not exit after task cleanup")
	}
}

func TestRulesViewShowsMatchedRulesAndFinalChanges(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.route, model.focus = SettingsRoute, focusContent
	model.baseConfig = config.Default("demo")
	model.config = config.Default("demo")
	model.config.Toolchain.CXX = "clang++"
	if model.editableConfig().Toolchain.CXX != "auto" {
		t.Fatal("settings editor should use TOML values, not rule output")
	}
	model.probe.Rules = policy.Report{
		Rules:   []policy.RuleResult{{Index: 1, When: `os == "windows"`, Matched: true, Changes: []policy.Change{{Field: "toolchain.cxx", Before: "auto", After: "clang++"}}}, {Index: 2, When: "false"}},
		Changes: []policy.Change{{Field: "toolchain.cxx", Before: "auto", After: "clang++"}},
	}
	updated, _ := model.handleKey(tea.KeyPressMsg(tea.Key{Text: "v"}))
	model = updated.(dashboardModel)
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(), 90), "\n"))
	for _, expected := range []string{"#1 matched", "#2 skipped", "toolchain.cxx: auto → clang++", "Effective selection"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("rule view missing %q: %s", expected, view)
		}
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

func TestSidebarKeepsRoutesOnOneLine(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.route, model.focus = PresetsRoute, focusNavigation
	for _, total := range []int{96, 110, 150} {
		width := sidebarWidth(total)
		plain := ansi.Strip(model.sidebar(newPalette(), width, 36))
		lines := strings.Split(plain, "\n")
		seen := map[int]bool{}
		for _, route := range routes {
			found := false
			for index, line := range lines {
				if strings.Contains(line, navigationLabel(route)) {
					if seen[index] {
						t.Fatalf("routes share a line at width %d: %q", width, line)
					}
					seen[index], found = true, true
					break
				}
			}
			if !found {
				t.Fatalf("%s wrapped or vanished at width %d:\n%s", route, width, plain)
			}
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("sidebar exceeds width %d: %q", width, line)
			}
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
	model.route, model.focus = PackagesRoute, focusLog
	for index := 0; index < 40; index++ {
		updated, _ := model.Update(installEvent{line: fmt.Sprintf("install line %02d", index)})
		model = updated.(dashboardModel)
	}
	if len(model.installLog) != 40 {
		t.Fatalf("install log was truncated: %d lines", len(model.installLog))
	}
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(dashboardModel)
	if model.focus != focusLog || model.logScroll == 0 {
		t.Fatalf("mouse wheel did not scroll log: focus=%d scroll=%d", model.focus, model.logScroll)
	}
	updated, _ = model.Update(tea.MouseClickMsg(tea.Mouse{X: 80, Y: 8, Button: tea.MouseLeft}))
	model = updated.(dashboardModel)
	if model.focus != focusContent {
		t.Fatal("mouse click did not focus content")
	}
}

func TestDashboardBuildLogFollowsAndKeepsScrolledHistory(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.width, model.height, model.probing = 100, 20, false
	model.route, model.workflowRoute, model.focus, model.logFollow = BuildRoute, BuildRoute, focusLog, true
	for i := 0; i < 100; i++ {
		updated, _ := model.Update(workflowEvent{label: "Build", line: fmt.Sprintf("line %03d", i)})
		model = updated.(dashboardModel)
	}
	if len(model.workflowLog) != 100 || model.logScroll != model.maxLogScroll() {
		t.Fatalf("log did not follow output: lines=%d scroll=%d max=%d", len(model.workflowLog), model.logScroll, model.maxLogScroll())
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "line 099") {
		t.Fatal("latest output is not visible")
	}
	model.scrollLog(-10000)
	if model.logFollow || model.logScroll != 0 {
		t.Fatal("manual scroll did not pause log following")
	}
	updated, _ := model.Update(workflowEvent{label: "Build", line: "line 100"})
	model = updated.(dashboardModel)
	if model.logScroll != 0 || len(model.workflowLog) != 101 {
		t.Fatal("new output displaced the reader from earlier log lines")
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	model = updated.(dashboardModel)
	if !model.logFollow || model.logScroll != model.maxLogScroll() {
		t.Fatal("End did not resume following the log")
	}
}

func TestDashboardHardWrapsUnbrokenLogLines(t *testing.T) {
	line := strings.Repeat("x", 1000) + "TAIL"
	parts := appendWrappedLog(nil, newPalette().muted, line, 50)
	if len(parts) < 20 || !strings.Contains(ansi.Strip(parts[len(parts)-1]), "TAIL") {
		t.Fatalf("unbroken log was clipped: %d rows, tail %q", len(parts), ansi.Strip(parts[len(parts)-1]))
	}
	for _, part := range parts {
		if ansi.StringWidth(part) > 50 {
			t.Fatalf("wrapped log row is too wide: %d", ansi.StringWidth(part))
		}
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
	for _, expected := range []string{"Project", "Toolchain & SDKs", "Dependencies", "Target · app", "/ search"} {
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
