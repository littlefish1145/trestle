package tui

import (
	"context"
	"fmt"
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
			Packages: map[string]config.Package{
				"fmt":  {Provider: "vcpkg", Version: "11.2.0"},
				"yaml": {Provider: "vcpkg"},
			},
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

// selectWorkspaceHeader selects a member header and presses Enter, toggling the
// section. It returns the updated model with the cursor still on the header.
func selectWorkspaceHeader(t *testing.T, model dashboardModel, name string) dashboardModel {
	t.Helper()
	model.focus = focusNavigation
	for index, item := range model.workspaceNavItems() {
		if item.header && item.member == name {
			model.navCursor = index
			updated, _, handled := model.handleEnterKey()
			if !handled {
				t.Fatal("header Enter was not handled")
			}
			return updated.(dashboardModel)
		}
	}
	t.Fatalf("member header %q not found", name)
	return model
}

func TestWorkspaceTreeStartsWithOnlyRootExpanded(t *testing.T) {
	model, _ := workspaceConsole(t)
	p := newPalette(false)
	side := ansi.Strip(model.sidebar(p, 40, 60))
	for _, expected := range []string{
		"▼ Root", "Overview", "Targets (all)", "Packages", "Tasks", "Toolchains", "Presets", "Doctor", "Settings",
		"▶ core", "▶ cli",
	} {
		if !strings.Contains(side, expected) {
			t.Fatalf("workspace tree missing %q:\n%s", expected, side)
		}
	}
	for _, hidden := range []string{"Targets (2)", "Targets (1)", "Tests (1)"} {
		if strings.Contains(side, hidden) {
			t.Fatalf("member rows must start collapsed, found %q:\n%s", hidden, side)
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
}

func TestWorkspaceHeaderEnterExpandsAndCollapses(t *testing.T) {
	model, _ := workspaceConsole(t)
	model = selectWorkspaceHeader(t, model, "core")
	if !model.expanded["core"] {
		t.Fatal("Enter on the core header did not expand it")
	}
	side := ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	for _, expected := range []string{"▸ ▼ core", "Targets (2)", "Packages (2)", "Tests (1)", "Release", "▶ cli"} {
		if !strings.Contains(side, expected) {
			t.Fatalf("expanded tree missing %q:\n%s", expected, side)
		}
	}
	model = selectWorkspaceHeader(t, model, "core")
	if model.expanded["core"] {
		t.Fatal("Enter on the header did not collapse it again")
	}
	side = ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	if !strings.Contains(side, "▶ core") || strings.Contains(side, "Targets (2)") {
		t.Fatalf("collapsed tree still shows member rows:\n%s", side)
	}
}

func TestWorkspaceTreeSelectsMemberTargets(t *testing.T) {
	model, _ := workspaceConsole(t)
	model = selectWorkspaceHeader(t, model, "core")
	// The 9th visible leaf is "core → Targets (2)"; digits count leaves, not
	// headers.
	model = tapNumber(t, model, "9")
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
	down := func(m dashboardModel) dashboardModel {
		updated, _ := tap(m, "down")
		return updated
	}
	// Overview → Targets (all) → Packages: leaves switch the screen immediately.
	model = down(model)
	if model.route != TargetsRoute || model.member != "" {
		t.Fatalf("first down: route=%q member=%q", model.route, model.member)
	}
	model = down(model)
	if model.route != PackagesRoute || model.member != "" {
		t.Fatalf("second down: route=%q member=%q", model.route, model.member)
	}
	side := ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	if !strings.Contains(side, "▸ 3 Packages") {
		t.Fatalf("active row not highlighted:\n%s", side)
	}
	// Move down to the last root row (Settings), then onto the collapsed core
	// header: the header is selected, but the page stays put until Enter.
	for model.navCursor < 7 {
		model = down(model)
	}
	if model.route != SettingsRoute {
		t.Fatalf("last root row: route=%q", model.route)
	}
	model = down(model)
	items := model.workspaceNavItems()
	if !items[model.navCursor].header || items[model.navCursor].member != "core" {
		t.Fatalf("cursor should rest on the core header: %#v", items[model.navCursor])
	}
	if model.route != SettingsRoute {
		t.Fatalf("header selection must not switch the page: route=%q", model.route)
	}
	side = ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	if !strings.Contains(side, "▸ ▶ core") {
		t.Fatalf("header not highlighted:\n%s", side)
	}
	// The next header is cli, then the cursor wraps back to Overview.
	model = down(model)
	if items := model.workspaceNavItems(); !items[model.navCursor].header || items[model.navCursor].member != "cli" {
		t.Fatalf("cursor should rest on the cli header: %#v", items[model.navCursor])
	}
	model = down(model)
	if model.route != OverviewRoute || model.member != "" {
		t.Fatalf("wrapped cursor: route=%q member=%q", model.route, model.member)
	}
}

func TestWorkspaceBuildBuildsEveryMemberFromRootScope(t *testing.T) {
	model, _ := workspaceConsole(t)
	var requested WorkspaceBuildRequest
	called := false
	model.services = Services{
		BuildWorkspace: func(_ context.Context, request WorkspaceBuildRequest, progress func(string)) error {
			requested, called = request, true
			return nil
		},
	}
	model.member = ""
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
		t.Fatalf("root build request = %#v", requested)
	}
}

func TestWorkspaceTreeCollapseShortcut(t *testing.T) {
	model, _ := workspaceConsole(t)
	// The root scope starts with every member collapsed; z expands them all and
	// then folds them again.
	updated, _ := tap(model, "z")
	model = updated
	if !model.expanded["core"] || !model.expanded["cli"] {
		t.Fatalf("z from the root did not expand every member: %#v", model.expanded)
	}
	side := ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	if !strings.Contains(side, "▼ core") || !strings.Contains(side, "Targets (2)") || !strings.Contains(side, "Targets (1)") {
		t.Fatalf("expanded tree not rendered:\n%s", side)
	}
	updated, _ = tap(model, "z")
	model = updated
	if model.expanded["core"] || model.expanded["cli"] {
		t.Fatalf("second z did not collapse every member: %#v", model.expanded)
	}
	side = ansi.Strip(model.sidebar(newPalette(false), 40, 60))
	if !strings.Contains(side, "▶ core") || !strings.Contains(side, "▶ cli") {
		t.Fatalf("collapsed tree not rendered:\n%s", side)
	}
	// Inside a member scope z folds just that member.
	model = selectWorkspaceHeader(t, model, "core")
	model.member = "core"
	model.route = TargetsRoute
	updated, _ = tap(model, "z")
	model = updated
	if model.expanded["core"] {
		t.Fatal("z inside a member scope did not fold it")
	}
}

func TestWorkspaceNavigationKeepsSelectionVisible(t *testing.T) {
	model, _ := workspaceConsole(t)
	names := make([]string, 0, 30)
	members := map[string]config.Config{}
	for index := 0; index < 30; index++ {
		name := fmt.Sprintf("m%02d", index)
		names = append(names, name)
		members[name] = config.Config{Targets: map[string]config.Target{"app": {Type: "executable"}}}
	}
	model.config.Workspace.Members = names
	model.probe.MemberConfigs = members
	model.focus = focusNavigation
	model.navCursor = len(model.workspaceNavItems()) - 1
	panel := ansi.Strip(model.navigationPanel(newPalette(false), 44, 16))
	if !strings.Contains(panel, names[len(names)-1]) {
		t.Fatalf("selected member row is not visible after scrolling:\n%s", panel)
	}
	if strings.Contains(panel, "Overview") {
		t.Fatalf("navigation did not scroll away from the top:\n%s", panel)
	}
}

func TestWorkspaceRootTargetsAggregateAllMembers(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.route = TargetsRoute
	model.member = ""
	rows := model.targetRows()
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		labels = append(labels, row.label())
	}
	joined := strings.Join(labels, ",")
	for _, expected := range []string{"core:greeter", "core:greeter_test", "cli:hello"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("aggregate targets missing %q: %s", expected, joined)
		}
	}
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 90), "\n"))
	for _, expected := range []string{"core:greeter", "cli:hello", "filter: all targets", "/ search"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("targets page missing %q:\n%s", expected, view)
		}
	}
}

func TestWorkspaceRootTargetBuildSelectsOwningMember(t *testing.T) {
	model, _ := workspaceConsole(t)
	var requested WorkspaceBuildRequest
	model.services = Services{
		BuildWorkspace: func(_ context.Context, request WorkspaceBuildRequest, progress func(string)) error {
			requested = request
			return nil
		},
	}
	model.route = TargetsRoute
	model.member = ""
	model.cursor = 0
	updated, cmd := model.startSelectedTargetBuild()
	model = updated.(dashboardModel)
	if cmd == nil {
		t.Fatal("build did not start")
	}
	model = feed(t, model, cmd, 8)
	if requested.Member != "core" || len(requested.Targets) != 1 || requested.Targets[0] != "greeter" {
		t.Fatalf("aggregate build request = %#v", requested)
	}
}

func TestWorkspaceRootTargetSearchFiltersMembersAndTargets(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.route = TargetsRoute
	model.member = ""
	model.targetQuery = "cli"
	rows := model.targetRows()
	if len(rows) != 1 || rows[0].label() != "cli:hello" {
		t.Fatalf("member search rows = %#v", rows)
	}
	model.targetQuery = "greeter"
	if rows := model.targetRows(); len(rows) != 2 {
		t.Fatalf("target search rows = %#v", rows)
	}
}

func TestTargetSearchFiltersStandaloneTargets(t *testing.T) {
	model, _ := freshConsole(t)
	model.route = TargetsRoute
	model.openInput("target-search", "Filter targets")
	model.inputText = "unit"
	updated, _ := model.submitInput()
	model = updated.(dashboardModel)
	rows := model.targetRows()
	if len(rows) != 1 || rows[0].name != "unit" {
		t.Fatalf("filtered standalone targets = %#v", rows)
	}
	model.openInput("target-search", "Filter targets")
	model.inputText = ""
	updated, _ = model.submitInput()
	model = updated.(dashboardModel)
	if len(model.targetRows()) != 2 {
		t.Fatalf("clearing the filter did not restore every target: %#v", model.targetRows())
	}
}

func TestWorkspaceScopeFollowsSelectedMember(t *testing.T) {
	model, _ := workspaceConsole(t)
	scopes := []string{}
	model.services = Services{SetScope: func(member string) { scopes = append(scopes, member) }}
	model = selectWorkspaceHeader(t, model, "core")
	model = tapNumber(t, model, "9")
	if model.member != "core" {
		t.Fatalf("member = %q", model.member)
	}
	model = tapNumber(t, model, "1")
	if model.member != "" {
		t.Fatalf("root row did not clear the scope: %q", model.member)
	}
	if len(scopes) < 2 || scopes[len(scopes)-2] != "core" || scopes[len(scopes)-1] != "" {
		t.Fatalf("scope changes = %#v", scopes)
	}
}

func TestWorkspaceMemberPackagesShowOrigin(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "core"
	model.route = PackagesRoute
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 100), "\n"))
	if !strings.Contains(view, "shared from root") {
		t.Fatalf("inherited package not marked as shared:\n%s", view)
	}
	if !strings.Contains(view, "· member") {
		t.Fatalf("member-only package not marked:\n%s", view)
	}
}

func TestWorkspaceMemberPackagesAreEditable(t *testing.T) {
	model, _ := workspaceConsole(t)
	model.member = "core"
	model.route = PackagesRoute
	view := ansi.Strip(strings.Join(model.contentLines(newPalette(false), 90), "\n"))
	if !strings.Contains(view, "fmt") {
		t.Fatalf("member packages not shown:\n%s", view)
	}
	updated, _, handled := model.handleListKeys(tea.Key{Code: 'a', Text: "a"})
	model = updated.(dashboardModel)
	if !handled || !model.inputMode {
		t.Fatalf("member package key was blocked (handled=%v input=%v message=%q)", handled, model.inputMode, model.message)
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
