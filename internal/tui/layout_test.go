package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"trestle/internal/config"
	"trestle/internal/policy"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

func TestFrameGeometryStableDuringLongOutput(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {100, 30}, {160, 50}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			model := newDashboard("trestle.toml", Services{})
			model.width, model.height, model.probing = size[0], size[1], false
			model.focus = focusLog
			model.logFollow = true
			before := strings.Split(ansi.Strip(model.View().Content), "\n")
			for i := 0; i < 200; i++ {
				updated, _ := model.Update(workflowEvent{label: "Build", line: strings.Repeat("中", 100) + fmt.Sprint(i)})
				model = updated.(dashboardModel)
			}
			after := strings.Split(ansi.Strip(model.View().Content), "\n")
			if len(before) != size[1] || len(after) != size[1] {
				t.Fatalf("height overflow: %d / %d", len(before), len(after))
			}
			for i, line := range after {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("width overflow row %d", i)
				}
				if strings.Contains(before[i], "╭") != strings.Contains(line, "╭") || strings.Contains(before[i], "╰") != strings.Contains(line, "╰") {
					t.Fatalf("panel moved at row %d", i)
				}
			}
			if !strings.Contains(after[len(after)-1], "? help") {
				t.Fatal("footer clipped")
			}
		})
	}
}

func TestSettingsCategoriesSearchAndCorrectEdit(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	model.config = config.Default("demo")
	model.probing = false
	model.route, model.focus = SettingsRoute, focusContent
	model.cursor = 1
	model.openSelectedSetting()
	if model.settingsCategory != "build." || model.inputMode {
		t.Fatal("category did not open second level")
	}
	model.openSelectedSetting()
	if model.inputPackage != "build.build_dir" {
		t.Fatalf("wrong setting %q", model.inputPackage)
	}
	model.closeInput()
	updated, _, handled := model.handleSettingsKey("/")
	model = updated.(dashboardModel)
	if !handled {
		t.Fatal("search not available")
	}
	updated, _ = model.handleInput(tea.Key{Text: "cxx"})
	model = updated.(dashboardModel)
	if len(model.filteredSettings()) < 2 || model.settingsQuery != "cxx" {
		t.Fatal("search is not live across categories")
	}
	updated, _ = model.handleInput(tea.Key{Code: tea.KeyEnter})
	model = updated.(dashboardModel)
	model.cursor = 1
	want := model.filteredSettings()[1].key
	model.openSelectedSetting()
	if model.inputPackage != want {
		t.Fatal("filtered index edited wrong setting")
	}
}

func TestLocalRefreshDoesNotRunDiscoveryOrBlockActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := newDashboard(path, Services{AssessTargets: func(context.Context) (config.Config, map[string]policy.Status, policy.Report, error) {
		calls++
		return config.Config{}, nil, policy.Report{}, nil
	}})
	model.probe.Components = []toolchain.Component{{Name: "cached", Ready: true}}
	message := model.probeCommand()()
	if calls != 0 {
		t.Fatal("file refresh invoked slow assessment")
	}
	updated, command := model.Update(message)
	model = updated.(dashboardModel)
	if model.busy() || !model.assessing || command == nil || len(model.probe.Components) != 1 {
		t.Fatal("background assessment blocks actions or clears cached inventory")
	}
	command()
	if calls != 1 {
		t.Fatal("assessment missing")
	}
	updated, _ = model.Update(mutationMessage{label: "Saved"})
	model = updated.(dashboardModel)
	if model.probing {
		t.Fatal("ordinary setting edit triggers blocking inspection")
	}
}

func TestDiskBackedLogKeepsHistoryWithBoundedCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	var lines strings.Builder
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&lines, "line %06d\n", i)
	}
	lines.WriteString(strings.Repeat("中文", 1000) + "TAIL")
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newDashboard("trestle.toml", Services{})
	model.probing = false
	model.fullLog = true
	model.focus = focusLog
	model.diskLog = &logView{reader: runlog.Reader{Path: path}}
	model.logFollow = true
	if !strings.Contains(ansi.Strip(model.View().Content), "TAIL") {
		t.Fatal("last visual row clipped")
	}
	model.logFollow = false
	model.logScroll = 0
	if !strings.Contains(ansi.Strip(model.View().Content), "line 000000") {
		t.Fatal("earliest history inaccessible")
	}
	for i := 0; i < 1000; i++ {
		model.receiveLog(fmt.Sprint(i), false)
	}
	if len(model.workflowLog) > 512 {
		t.Fatal("unbounded UI text cache")
	}
}
