package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
)

// workspaceConsole returns the standard fixture configured as a workspace root
// with merged member configurations.
func workspaceConsole(t *testing.T) (dashboardModel, *callJournal) {
	t.Helper()
	model, journal := freshConsole(t)
	model.config.Workspace = &config.Workspace{Members: []string{"core", "cli"}}
	model.probe.ProjectName = "demo-ws"
	model.probe.MemberConfigs = map[string]config.Config{
		"core": {
			Project: config.Project{Name: "core"},
			Build:   config.Build{DefaultTargets: []string{"greeter"}},
			Targets: map[string]config.Target{
				"greeter":      {Type: "static", Sources: []string{"src/greeter.cpp"}},
				"greeter_test": {Type: "test", Sources: []string{"tests/greeter_test.cpp"}, TestGroup: "unit"},
			},
			Packages: map[string]config.Package{"fmt": {Provider: "vcpkg"}},
		},
		"cli": {
			Project: config.Project{Name: "cli"},
			Targets: map[string]config.Target{
				"hello": {Type: "executable", Sources: []string{"src/main.cpp"}},
			},
			Packages: map[string]config.Package{},
		},
	}
	model.baseConfig = model.config
	return model, journal
}

func TestWorkspaceTreeRendersMembersAndCounts(t *testing.T) {
	model, _ := workspaceConsole(t)
	p := newPalette(false)
	side := ansi.Strip(model.sidebar(p, 34, 46))
	for _, expected := range []string{"▼ Root", "Overview", "Toolchains", "Build All", "▼ core", "Targets (2)", "Packages", "▼ cli", "Targets (1)"} {
		if !strings.Contains(side, expected) {
			t.Fatalf("workspace tree missing %q:\n%s", expected, side)
		}
	}
	header := ansi.Strip(model.header(p, 140))
	if !strings.Contains(header, "ws: demo-ws") || !strings.Contains(header, "pkg: all") {
		t.Fatalf("header missing workspace context: %q", header)
	}
	model.member = "core"
	model.route = TargetsRoute
	header = ansi.Strip(model.header(p, 140))
	if !strings.Contains(header, "pkg: core") {
		t.Fatalf("header missing selected member: %q", header)
	}
	side = ansi.Strip(model.sidebar(p, 34, 46))
	if !strings.Contains(side, "▸ 4 Targets (2)") {
		t.Fatalf("workspace tree does not mark the active member screen:\n%s", side)
	}
}

func TestWorkspaceTreeSelectsMemberTargets(t *testing.T) {
	model, _ := workspaceConsole(t)
	// Row 4 is "core → Targets (2)"; digits address the tree rows.
	model = tapNumber(t, model, "4")
	if model.member != "core" || model.route != TargetsRoute {
		t.Fatalf("tree selection: member=%q route=%q", model.member, model.route)
	}
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 90), "\n"))
	if !strings.Contains(view, "greeter") || !strings.Contains(view, "greeter_test") {
		t.Fatalf("targets page does not show the member's targets:\n%s", view)
	}
	if strings.Contains(view, "hello") {
		t.Fatalf("targets page leaked another member's target:\n%s", view)
	}
}

// tapNumber feeds one digit key through Update so the tree activation path runs.
func tapNumber(t *testing.T, model dashboardModel, digit string) dashboardModel {
	t.Helper()
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: rune(digit[0]), Text: digit}))
	return updated.(dashboardModel)
}

func TestWorkspaceTreeMovesWithArrowsLikeFlatNavigation(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.focus = focusNavigation
	// Overview → Toolchains → Build All, immediately switching the screen the
	// way the flat navigation does.
	down := func(m dashboardModel) dashboardModel {
		updated, _ := tap(m, "down")
		return updated
	}
	model = down(model)
	if model.route != ToolchainsRoute || model.member != "" {
		t.Fatalf("first down: route=%q member=%q", model.route, model.member)
	}
	model = down(model)
	if model.route != BuildRoute || model.member != "" {
		t.Fatalf("second down: route=%q member=%q", model.route, model.member)
	}
	side := ansi.Strip(model.sidebar(newPalette(false), 34, 46))
	if !strings.Contains(side, "▸ 3 Build All") {
		t.Fatalf("active row not highlighted:\n%s", side)
	}
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 90), "\n"))
	if !strings.Contains(view, "core") || !strings.Contains(view, "cli") {
		t.Fatalf("workspace build screen does not summarize members:\n%s", view)
	}
}

func TestWorkspaceBuildScreenBuildsEveryMember(t *testing.T) {
	model, _ := workspaceConsole(t)
	var requested WorkspaceBuildRequest
	called := false
	model.services = Services{
		BuildWorkspace: func(_ context.Context, request WorkspaceBuildRequest, progress func(string)) error {
			requested, called = request, true
			return nil
		},
	}
	model.member = "core"
	// Row 3 is "Build All": opening it clears the member filter and shows the
	// aggregate build screen.
	model = tapNumber(t, model, "3")
	if model.route != BuildRoute || model.member != "" {
		t.Fatalf("Build All row: route=%q member=%q", model.route, model.member)
	}
	updated, cmd := model.startBuild()
	model = updated.(dashboardModel)
	if cmd == nil {
		t.Fatal("build did not start")
	}
	model = feed(t, model, cmd, 8)
	if !called {
		t.Fatal("build did not invoke the workspace build service")
	}
	if requested.Member != "" || requested.Targets != nil {
		t.Fatalf("Build All request = %#v", requested)
	}
}

func TestWorkspaceMemberSwitchDrivesBuilds(t *testing.T) {
	model, _ := workspaceConsole(t)
	var requested WorkspaceBuildRequest
	called := false
	model.services = Services{
		BuildWorkspace: func(_ context.Context, request WorkspaceBuildRequest, progress func(string)) error {
			requested, called = request, true
			progress("[core] Building util")
			return nil
		},
	}
	updated, _ := tap(model, "w")
	model = updated
	if !model.inputMode || model.inputKind != "switch-member" {
		t.Fatalf("w did not open the member prompt: %#v", model.inputKind)
	}
	model.inputText = "core"
	updatedModel, _ := model.submitInput()
	model = updatedModel.(dashboardModel)
	if model.member != "core" {
		t.Fatalf("member not selected: %q", model.member)
	}
	updatedModel, cmd := model.startBuild()
	model = updatedModel.(dashboardModel)
	if cmd == nil {
		t.Fatal("build did not start")
	}
	model = feed(t, model, cmd, 8)
	if !called || requested.Member != "core" {
		t.Fatalf("build request = %#v (called=%v)", requested, called)
	}
}

func TestWorkspacePackagesAreReadOnlyForMembers(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "core"
	model.route = PackagesRoute
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 90), "\n"))
	if !strings.Contains(view, "fmt") {
		t.Fatalf("member packages not shown:\n%s", view)
	}
	updatedModel, _, handled := model.handleListKeys(tea.Key{Code: 'a', Text: "a"})
	model = updatedModel.(dashboardModel)
	if !handled {
		t.Fatal("add key not handled")
	}
	if !model.messageError || !strings.Contains(model.message, "core/trestle.toml") {
		t.Fatalf("member package mutation was not blocked: %q", model.message)
	}
}

func TestWorkspaceUnknownMemberKeepsSelection(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "core"
	model.openInput("switch-member", "Switch member")
	model.inputText = "nope"
	updated, _ := model.submitInput()
	model = updated.(dashboardModel)
	if model.member != "core" {
		t.Fatalf("invalid member replaced the selection: %q", model.member)
	}
	if !model.messageError || !strings.Contains(model.message, "Unknown member") {
		t.Fatalf("missing invalid-member message: %q", model.message)
	}
}

func TestWorkspaceLogsKeepMemberPrefix(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.route, model.workflowRoute, model.focus, model.logFollow = BuildRoute, BuildRoute, focusLog, true
	for _, line := range []string{"[core] Building CXX object util.cpp", "[app] Linking app"} {
		updated, _ := model.Update(workflowEvent{label: "Build", line: line})
		model = updated.(dashboardModel)
	}
	rendered := ansi.Strip(model.View().Content)
	for _, expected := range []string{"[core] Building CXX object util.cpp", "[app] Linking app"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("log view missing %q:\n%s", expected, rendered)
		}
	}
}

func TestWorkspaceMemberKeyIgnoredForStandaloneProject(t *testing.T) {
	model, _ := freshConsole(t)
	updated, _, handled := model.handleActionKeys(tea.Key{Code: 'w', Text: "w"})
	if handled {
		t.Fatalf("w opened a member prompt for a standalone project")
	}
	if updated.(dashboardModel).inputMode {
		t.Fatal("standalone project entered input mode")
	}
}

func TestWorkspaceScopesTestsToSelectedMember(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "core"
	jobs := model.scopedTestJobs()
	if len(jobs) != 1 || jobs[0].Name != "greeter_test" || jobs[0].Group != "unit" {
		t.Fatalf("scoped test jobs = %#v", jobs)
	}
}

func TestWorkspaceDoctorUsesSelectedMember(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "app"
	var got string
	model.services = Services{
		DoctorWorkspace: func(_ context.Context, member string, progress func(string)) error {
			got = member
			progress("[app] checking member")
			return nil
		},
	}
	updatedModel, cmd, handled := model.enterDoctor()
	model = updatedModel.(dashboardModel)
	if !handled || cmd == nil {
		t.Fatal("doctor did not start")
	}
	model = feed(t, model, cmd, 8)
	if got != "app" {
		t.Fatalf("doctor member = %q", got)
	}
}
