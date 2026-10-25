package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
)

// workspaceNavItem is one row in the workspace tree: a member section header, a
// root-level screen, or a member's screen.
type workspaceNavItem struct {
	label    string
	route    Route
	member   string
	header   bool
	expanded bool
}

// workspaceNavItems flattens the visible tree: the root screens first, then each
// member header followed by its screens when expanded. Member sections start
// collapsed, so the root is the only expanded group on a fresh console.
func (model dashboardModel) workspaceNavItems() []workspaceNavItem {
	if !model.isWorkspace() {
		return nil
	}
	items := []workspaceNavItem{
		{label: "Overview", route: OverviewRoute},
		{label: "Targets (all)", route: TargetsRoute},
		{label: "Packages", route: PackagesRoute},
		{label: "Tasks", route: TasksRoute},
		{label: "Toolchains", route: ToolchainsRoute},
		{label: "Presets", route: PresetsRoute},
		{label: "Doctor", route: DoctorRoute},
		{label: "Settings", route: SettingsRoute},
	}
	for _, name := range model.workspaceMembers() {
		expanded := model.expanded[name]
		items = append(items, workspaceNavItem{label: name, member: name, header: true, expanded: expanded})
		if !expanded {
			continue
		}
		items = append(items, model.memberNavItems(name)...)
	}
	return items
}

// memberNavItems builds the screens inside one member section.
func (model dashboardModel) memberNavItems(name string) []workspaceNavItem {
	member, ok := model.probe.MemberConfigs[name]
	targets, tests, packages := 0, 0, 0
	if ok {
		targets = len(member.Targets)
		packages = len(member.Packages)
		for _, target := range member.Targets {
			if target.Type == "test" {
				tests++
			}
		}
	}
	return []workspaceNavItem{
		{label: fmt.Sprintf("Targets (%d)", targets), route: TargetsRoute, member: name},
		{label: fmt.Sprintf("Packages (%d)", packages), route: PackagesRoute, member: name},
		{label: fmt.Sprintf("Tests (%d)", tests), route: TestsRoute, member: name},
		{label: "Tasks", route: TasksRoute, member: name},
		{label: "Presets", route: PresetsRoute, member: name},
		{label: "Release", route: ReleaseRoute, member: name},
		{label: "Doctor", route: DoctorRoute, member: name},
		{label: "Settings", route: SettingsRoute, member: name},
	}
}

// clampWorkspaceNavCursor keeps the tree selection inside the visible rows.
func (model *dashboardModel) clampWorkspaceNavCursor() {
	items := model.workspaceNavItems()
	if len(items) == 0 {
		model.navCursor = 0
		return
	}
	model.navCursor = min(max(model.navCursor, 0), len(items)-1)
}

// focusWorkspaceMember moves the tree selection to a member's first screen,
// expanding the section so its screens are reachable.
func (model *dashboardModel) focusWorkspaceMember(name string) {
	if name == "" {
		model.navCursor = 0
		return
	}
	if model.expanded == nil {
		model.expanded = map[string]bool{}
	}
	model.expanded[name] = true
	items := model.workspaceNavItems()
	for index, item := range items {
		if !item.header && item.member == name {
			model.navCursor = index
			return
		}
	}
	model.clampWorkspaceNavCursor()
}

// toggleWorkspaceSection folds or unfolds one member section, or every member
// section when name is empty.
func (model *dashboardModel) toggleWorkspaceSection(name string) {
	if !model.isWorkspace() {
		return
	}
	if model.expanded == nil {
		model.expanded = map[string]bool{}
	}
	if name != "" {
		model.expanded[name] = !model.expanded[name]
		model.clampWorkspaceNavCursor()
		return
	}
	anyExpanded := false
	for _, member := range model.workspaceMembers() {
		if model.expanded[member] {
			anyExpanded = true
			break
		}
	}
	for _, member := range model.workspaceMembers() {
		model.expanded[member] = !anyExpanded
	}
	model.clampWorkspaceNavCursor()
}

// toggleWorkspaceCollapse is the z shortcut: it folds the active member section,
// or every member section from the root scope.
func (model *dashboardModel) toggleWorkspaceCollapse() {
	model.toggleWorkspaceSection(model.member)
}

// toggleSelectedWorkspaceSection folds or unfolds the member header the tree
// selection is on.
func (model *dashboardModel) toggleSelectedWorkspaceSection() bool {
	items := model.workspaceNavItems()
	if model.navCursor < 0 || model.navCursor >= len(items) || !items[model.navCursor].header {
		return false
	}
	model.toggleWorkspaceSection(items[model.navCursor].member)
	return true
}

// openWorkspaceNavLeaf switches the console to the selected screen, carrying the
// row's member scope into the host so mutations write the right trestle.toml.
func (model *dashboardModel) openWorkspaceNavLeaf(index int) {
	items := model.workspaceNavItems()
	if index < 0 || index >= len(items) || items[index].header {
		return
	}
	item := items[index]
	model.navCursor = index
	model.member = item.member
	model.applyScope()
	model.selectRoute(routeIndex(item.route))
}

// activateWorkspaceNav handles a leaf activation by index (digits): headers are
// toggled instead of opened.
func (model *dashboardModel) activateWorkspaceNav(index int) dashboardModel {
	items := model.workspaceNavItems()
	if index < 0 || index >= len(items) {
		return *model
	}
	if items[index].header {
		model.navCursor = index
		model.toggleWorkspaceSection(items[index].member)
		return *model
	}
	model.openWorkspaceNavLeaf(index)
	return *model
}

// workspaceNavLeafIndex maps a 1-based digit to the nth visible leaf row. Section
// headers are not numbered.
func (model dashboardModel) workspaceNavLeafIndex(digit int) int {
	leaf := 0
	for index, item := range model.workspaceNavItems() {
		if item.header {
			continue
		}
		leaf++
		if leaf == digit {
			return index
		}
	}
	return -1
}

// workspaceNavLines renders the workspace tree. The root header is static; every
// member header is a selectable row that toggles its section.
func (model dashboardModel) workspaceNavLines(p palette, width int) []string {
	items := model.workspaceNavItems()
	lines := []string{p.faint.Render("▼ Root")}
	leaf := 0
	for index, item := range items {
		active := index == model.navCursor
		if item.header {
			glyph := "▶"
			if item.expanded {
				glyph = "▼"
			}
			prefix := "   "
			if active {
				prefix = " ▸ "
			}
			label := prefix + glyph + " " + item.label
			if active {
				lines = append(lines, p.selected.Copy().Width(max(1, width-4)).Render(label))
			} else {
				lines = append(lines, p.faint.Render(label))
			}
			continue
		}
		leaf++
		lines = append(lines, model.workspaceNavRow(p, width, leaf, item, active))
	}
	return lines
}

// workspaceNavRow renders one numbered tree row, highlighting the active row.
// number is the leaf's 1-based ordinal, so digits map to it directly.
func (model dashboardModel) workspaceNavRow(p palette, width, number int, item workspaceNavItem, active bool) string {
	key := "·"
	if number < 10 {
		key = fmt.Sprint(number)
	} else if number == 10 {
		key = "0"
	}
	marker := "   "
	if active {
		marker = " ▸ "
	}
	label := ansi.Truncate(marker+key+" "+item.label, width-4, "…")
	if active {
		return p.selected.Copy().Width(max(1, width-4)).Render(label)
	}
	return p.muted.Render(label)
}

// scopedConfig returns the configuration the target and package screens show:
// the selected member's merged configuration, or the workspace root's.
func (model dashboardModel) scopedConfig() config.Config {
	if model.member != "" {
		if member, ok := model.probe.MemberConfigs[model.member]; ok {
			return member
		}
	}
	return model.config
}

// scopedTestJobs returns the test jobs of the selected member, or the root
// project's jobs for a standalone console.
func (model dashboardModel) scopedTestJobs() []testJob {
	if !model.isWorkspace() || model.member == "" {
		return model.probe.Tests
	}
	member, ok := model.probe.MemberConfigs[model.member]
	if !ok {
		return nil
	}
	jobs := make([]testJob, 0)
	for _, name := range sortedTargetNames(member.Targets) {
		target := member.Targets[name]
		if target.Type != "test" {
			continue
		}
		group := target.TestGroup
		if group == "" {
			group = "default"
		}
		jobs = append(jobs, testJob{Name: name, Group: group, Files: append([]string{}, target.Sources...)})
	}
	return jobs
}

// scopedTasks returns the tasks the Tasks screen shows: the selected member's
// tasks, or the root project's tasks.
func (model dashboardModel) scopedTasks() map[string]config.Task {
	if model.member != "" {
		if member, ok := model.probe.MemberConfigs[model.member]; ok {
			return member.Tasks
		}
	}
	return model.config.Tasks
}

// scopedPresets returns the presets the Presets screen shows: a member's merged
// preset set (root presets plus overrides), or the root project's presets.
func (model dashboardModel) scopedPresets() map[string]config.CompilerPreset {
	return model.scopedConfig().CompilerPresets
}

// applyScope tells the host which configuration file subsequent single-project
// operations must read and write. Workspace builds, tests and doctor runs carry
// the member themselves and ignore this.
func (model *dashboardModel) applyScope() {
	if model.services.SetScope != nil {
		model.services.SetScope(model.member)
	}
}

// workspaceBuildLines is the workspace Build screen: every member with its
// target count, ready for an aggregate build. Member targets live on their own
// Targets rows in the tree.
func (model dashboardModel) workspaceBuildLines(p palette, width int) []string {
	lines := []string{"", "  " + model.buildStateLabel(p), "", p.faint.Render(" MEMBERS · ↑↓ TREE · b BUILDS ALL"), ""}
	for index, name := range model.workspaceMembers() {
		targets := 0
		if member, ok := model.probe.MemberConfigs[name]; ok {
			targets = len(member.Targets)
		}
		text := fmt.Sprintf("%-18s %3d targets", name, targets)
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(text, width-3, "…")))
	}
	if len(model.config.Targets) > 0 {
		lines = append(lines, "", p.faint.Render(" ROOT TARGETS"))
		for _, name := range sortedTargetNames(model.config.Targets) {
			lines = append(lines, p.muted.Render("   "+name+"  "+model.config.Targets[name].Type))
		}
	}
	lines = append(lines, "", p.faint.Render("b build every member   a build every target   enter build every member"))
	if model.lastRun != "" {
		lines = append(lines, "", p.faint.Render(" LAST RUN"), p.text.Render(model.lastRun))
	}
	return lines
}

// targetRow is one row of the Targets/Build screens: a target belonging to the
// active member, or - at the workspace root - a target of any member, addressed
// as "member:target".
type targetRow struct {
	member string // "" for a standalone project or the active member's own scope
	name   string
	target config.Target
}

// label is how the row is shown: qualified with the member at the workspace
// root, plain inside a member scope.
func (row targetRow) label() string {
	if row.member == "" {
		return row.name
	}
	return row.member + ":" + row.name
}

// targetRows returns the rows the Targets and Build screens show, filtered by
// the active target search. A workspace root aggregates every member's targets
// so one screen can search and build the whole workspace.
func (model dashboardModel) targetRows() []targetRow {
	query := strings.ToLower(strings.TrimSpace(model.targetQuery))
	match := func(label string) bool {
		return query == "" || strings.Contains(strings.ToLower(label), query)
	}
	var rows []targetRow
	if model.isWorkspace() && model.member == "" {
		for _, memberName := range model.workspaceMembers() {
			member, ok := model.probe.MemberConfigs[memberName]
			if !ok {
				continue
			}
			for _, name := range sortedTargetNames(member.Targets) {
				row := targetRow{member: memberName, name: name, target: member.Targets[name]}
				if match(row.label()) {
					rows = append(rows, row)
				}
			}
		}
		return rows
	}
	cfg := model.scopedConfig()
	for _, name := range sortedTargetNames(cfg.Targets) {
		row := targetRow{member: model.member, name: name, target: cfg.Targets[name]}
		if match(row.label()) {
			rows = append(rows, row)
		}
	}
	return rows
}

// selectedTargetRow returns the row under the content cursor.
func (model dashboardModel) selectedTargetRow() (targetRow, bool) {
	rows := model.targetRows()
	if model.cursor < 0 || model.cursor >= len(rows) {
		return targetRow{}, false
	}
	return rows[model.cursor], true
}

// targetQueryLabel names the active filter for the page header hints.
func (model dashboardModel) targetQueryLabel() string {
	if strings.TrimSpace(model.targetQuery) == "" {
		return "all targets"
	}
	return "filter: " + model.targetQuery
}

// defaultTargetMark stars a target when it is one of the scope's default build
// targets.
func (model dashboardModel) defaultTargetMark(row targetRow) string {
	defaults := model.scopedConfig().Build.DefaultTargets
	if row.member != "" {
		if member, ok := model.probe.MemberConfigs[row.member]; ok {
			defaults = member.Build.DefaultTargets
		}
	}
	for _, item := range defaults {
		if item == row.name {
			return "★"
		}
	}
	return " "
}

// targetAvailability marks a target row with a check, or a warning when the
// policy probe reported it as not ready.
func (model dashboardModel) targetAvailability(row targetRow) string {
	if status, ok := model.probe.Statuses[row.name]; ok && !status.Ready {
		return "!"
	}
	return "✓"
}

// startTargetRowBuild builds one row, carrying its member when the row belongs
// to another member than the active scope.
func (model dashboardModel) startTargetRowBuild(row targetRow, force bool) (tea.Model, tea.Cmd) {
	member := model.member
	if row.member != "" {
		member = row.member
	}
	if model.isWorkspace() && model.services.BuildWorkspace != nil {
		model.route, model.focus = BuildRoute, focusContent
		label := "Build " + row.label()
		if force {
			label = "Force build " + row.label()
		}
		return model.startWorkflow(label, func(ctx context.Context, progress func(string)) error {
			return model.services.BuildWorkspace(ctx, WorkspaceBuildRequest{Member: member, Targets: []string{row.name}, Force: force}, progress)
		})
	}
	if force {
		return model.startForcedTargetBuild(row)
	}
	return model.startTargetBuild([]string{row.name}, false, "Build "+row.name)
}
