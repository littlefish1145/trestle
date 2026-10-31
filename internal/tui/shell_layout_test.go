package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestQuietWorkspaceUsesFullWidthAndSkipsEmptyPanes(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	if model.showSidebar() || model.showOutput() || model.contentWidth() != model.width || model.focus != focusContent {
		t.Fatal("quiet workspace should start in the full-width page")
	}
	model, _ = tap(model, "tab")
	if model.focus != focusContent {
		t.Fatal("Tab focused a hidden pane")
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	model = updated.(dashboardModel)
	if !model.showSidebar() || model.focus != focusNavigation || model.contentWidth() >= model.width {
		t.Fatal("Ctrl+B did not open navigation")
	}
	model, _ = tap(model, "tab")
	if model.focus != focusContent {
		t.Fatal("Tab should skip empty output")
	}
	model.workflowLog = []string{"build output"}
	model, _ = tap(model, "tab")
	if model.focus != focusLog {
		t.Fatal("Tab should reach output once available")
	}
}

func TestWorkspaceShellFitsAllRoutesAndPaneStates(t *testing.T) {
	base, _ := freshConsole(t)
	for _, size := range [][2]int{{40, 12}, {68, 20}, {80, 24}, {100, 30}, {160, 50}} {
		for _, route := range routes {
			for state := 0; state < 4; state++ {
				t.Run(fmt.Sprintf("%dx%d/%s/%d", size[0], size[1], route, state), func(t *testing.T) {
					model := base
					model.width, model.height, model.route = size[0], size[1], route
					model.sidebarVisible = state&1 != 0
					if state&2 != 0 {
						model.workflowLog = []string{strings.Repeat("构建 output ", 20), "last line"}
					}
					assertFrameFits(t, model)
				})
			}
		}
	}
}

func assertFrameFits(t *testing.T, model dashboardModel) {
	t.Helper()
	lines := strings.Split(ansi.Strip(model.View().Content), "\n")
	if len(lines) != model.height {
		t.Fatalf("height: got %d, want %d", len(lines), model.height)
	}
	for i, line := range lines {
		if ansi.StringWidth(line) != model.width {
			t.Fatalf("row %d width: got %d, want %d", i, ansi.StringWidth(line), model.width)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "? help") {
		t.Fatal("footer was clipped")
	}
}

func TestNarrowNavigationRemainsUsable(t *testing.T) {
	model, _ := freshConsole(t)
	model.width, model.height = 40, 20
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	model = updated.(dashboardModel)
	if model.focus != focusNavigation || !strings.Contains(ansi.Strip(model.View().Content), "Workspace") {
		t.Fatal("narrow navigation should use the whole page")
	}
	model, _ = tap(model, "down")
	model, _ = tap(model, "enter")
	if model.route != TargetsRoute || model.focus != focusContent || !strings.Contains(ansi.Strip(model.View().Content), "Targets ·") {
		t.Fatal("narrow navigation did not open the selected page")
	}
}
