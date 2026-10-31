package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
)

func openCommands(model dashboardModel) dashboardModel {
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	return updated.(dashboardModel)
}

func queryCommands(model dashboardModel, query string) dashboardModel {
	updated, _ := model.Update(tea.PasteMsg{Content: query})
	return updated.(dashboardModel)
}

func TestCommandsNavigateFromAnyPaneAndRestorePagePosition(t *testing.T) {
	model, _ := freshConsole(t)
	model.route, model.focus, model.cursor = TargetsRoute, focusLog, 1
	model = openCommands(model)
	if !model.commandsOpen {
		t.Fatal("Ctrl+P must work from output")
	}
	model = queryCommands(model, "settings")
	commands := model.matchingCommands()
	if len(commands) == 0 || commands[0].route != SettingsRoute {
		t.Fatal("page name should be the first match")
	}
	model, _ = tap(model, "enter")
	if model.commandsOpen || model.route != SettingsRoute || model.focus != focusContent || model.inputMode {
		t.Fatal("command did not open settings directly")
	}
	model = queryCommands(openCommands(model), "open targets")
	model, _ = tap(model, "enter")
	if model.route != TargetsRoute || model.cursor != 1 {
		t.Fatal("returning to a page lost its selected target")
	}
}

func TestCommandSearchIgnoresTargetFilterAndBuildsExactSelection(t *testing.T) {
	model, _ := freshConsole(t)
	model.targetQuery = "unit"
	var selected []string
	model.services.BuildTargets = func(_ context.Context, names []string, _ bool, _ func(string)) error {
		selected = names
		return nil
	}
	model = queryCommands(openCommands(model), "build app")
	if commands := model.matchingCommands(); len(commands) == 0 || commands[0].target.name != "app" {
		t.Fatal("page filters must not hide commands for other targets")
	}
	armed, cmd := tap(model, "enter")
	if !armed.mutating || armed.commandsOpen || cmd == nil {
		t.Fatal("selected build was not started")
	}
	feed(t, armed, cmd, 2)
	if len(selected) != 1 || selected[0] != "app" {
		t.Fatalf("built the wrong targets: %v", selected)
	}
}

func TestCommandTaskStillRequiresPreviewConfirmation(t *testing.T) {
	model, journal := freshConsole(t)
	model = queryCommands(openCommands(model), "prepare")
	model, cmd := tap(model, "enter")
	if model.pendingTask != "prepare" || model.route != TasksRoute || cmd != nil || model.mutating || journal.has("RunTask") {
		t.Fatal("palette task must stop at its existing confirmation")
	}
	model = openCommands(model)
	if model.commandsOpen {
		t.Fatal("palette must not take over a pending confirmation")
	}
	model, _ = tap(model, "n")
	if model.pendingTask != "" || journal.has("RunTask") {
		t.Fatal("declining a task should leave it unexecuted")
	}
}

func TestWorkspaceCommandsCarryMemberAndResetSelectionOnScopeChange(t *testing.T) {
	model, _ := workspaceConsole(t)
	var request WorkspaceBuildRequest
	model.services.BuildWorkspace = func(_ context.Context, value WorkspaceBuildRequest, _ func(string)) error {
		request = value
		return nil
	}
	model = queryCommands(openCommands(model), "build cli:hello")
	armed, cmd := tap(model, "enter")
	feed(t, armed, cmd, 2)
	if request.Member != "cli" || len(request.Targets) != 1 || request.Targets[0] != "hello" {
		t.Fatalf("workspace target lost its scope: %+v", request)
	}
	model, _ = workspaceConsole(t)
	model.cursor, model.scroll = 10, 10
	model = queryCommands(openCommands(model), "switch core")
	model, _ = tap(model, "enter")
	if model.member != "core" || model.cursor != 0 || model.scroll != 0 || model.focus != focusContent {
		t.Fatal("member switch retained a stale cursor or scope")
	}
}

func TestCommandModalConsumesTextWithoutInvokingShortcuts(t *testing.T) {
	model, _ := freshConsole(t)
	model = openCommands(model)
	model, _ = tap(model, "q")
	if !model.commandsOpen || model.commandQuery != "q" {
		t.Fatal("q should edit the command search")
	}
	model, _ = tap(model, "ctrl+u")
	model = queryCommands(model, "not-a-real-command-不存在")
	model, cmd := tap(model, "enter")
	if !model.commandsOpen || cmd != nil || len(model.matchingCommands()) != 0 {
		t.Fatal("Enter on no results should keep search open")
	}
	model, _ = tap(model, "backspace")
	if !strings.HasSuffix(model.commandQuery, "不存") {
		t.Fatal("backspace split a Unicode character")
	}
	model, _ = tap(model, "ctrl+u")
	model = queryCommands(model, "构建")
	if len(model.matchingCommands()) == 0 {
		t.Fatal("Chinese action keywords should be searchable")
	}
	model, _ = tap(model, "esc")
	if model.commandsOpen || model.route != OverviewRoute {
		t.Fatal("Escape should close the palette without navigating")
	}
	model.openInput("setting", "Edit a value")
	model = openCommands(model)
	if model.commandsOpen || !model.inputMode {
		t.Fatal("Ctrl+P must not discard an active input")
	}
}

func TestCommandsRemainSearchableWhileOperationRuns(t *testing.T) {
	model, _ := freshConsole(t)
	model.mutating = true
	model.operation = "Build"
	model = queryCommands(openCommands(model), "build app")
	model, cmd := tap(model, "enter")
	if cmd != nil || model.route != OverviewRoute || model.operation != "Build" || !strings.Contains(model.message, "already running") && !strings.Contains(model.message, "operation is running") {
		t.Fatal("a palette action must not replace an active operation")
	}
}

func TestCommandsFitNarrowAndLightTerminals(t *testing.T) {
	base, _ := freshConsole(t)
	for _, size := range [][2]int{{40, 12}, {68, 20}, {100, 30}, {160, 50}} {
		for _, light := range []bool{false, true} {
			model := base
			model.width, model.height, model.lightBackground = size[0], size[1], light
			model = openCommands(model)
			model, _ = tap(model, "end")
			assertFrameFits(t, model)
			model = queryCommands(model, strings.Repeat("中文", 50))
			assertFrameFits(t, model)
			if !strings.Contains(ansi.Strip(model.View().Content), "No matching commands") {
				t.Fatal("no-results message should remain visible")
			}
		}
	}
}

func TestOverviewBuildsSelectedTargetAndFiltersLive(t *testing.T) {
	model, _ := freshConsole(t)
	model, _ = tap(model, "down")
	if model.cursor != 1 || model.route != OverviewRoute {
		t.Fatal("home should navigate targets directly")
	}
	armed, cmd := tap(model, "enter")
	if !armed.mutating || cmd == nil || armed.operation != "Build unit" {
		t.Fatal("home selection did not start the target build")
	}
	model, _ = tap(model, "/")
	updated, _ := model.Update(tea.PasteMsg{Content: "unit"})
	model = updated.(dashboardModel)
	if model.targetQuery != "unit" || model.itemCount() != 1 || model.cursor != 0 {
		t.Fatal("pasted home filter did not update live")
	}
}

func TestPastedInputIsTextAndKeepsCursorVisible(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.config = config.Default("demo")
	model.openInput("setting", "Edit")
	updated, _ := model.Update(tea.PasteMsg{Content: "enter\nq\x1b"})
	model = updated.(dashboardModel)
	if !model.inputMode || model.inputText != "enter q" {
		t.Fatal("paste should insert text without executing keys or control bytes")
	}
	model.inputText = strings.Repeat("中", 100) + "TAIL"
	line := ansi.Strip(model.statusLine(model.palette(), 40))
	if !strings.Contains(line, "TAIL▏") || ansi.StringWidth(line) > 40 {
		t.Fatalf("long input lost its cursor: %q", line)
	}
}
