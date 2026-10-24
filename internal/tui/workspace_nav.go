package tui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
)

// workspaceNavItem is one selectable row in the workspace tree: a root-level
// screen or a member's screen.
type workspaceNavItem struct {
	label  string
	route  Route
	member string
}

// workspaceNavSection is one collapsible group in the workspace tree: the root
// section or one member.
type workspaceNavSection struct {
	name  string // "" for the root section
	items []workspaceNavItem
}

// workspaceNavSections builds the tree groups. The root group holds the
// workspace-wide screens (aggregate targets, shared packages, toolchains,
// presets, tasks, doctor, settings); every member group holds the screens that
// edit that member's own trestle.toml.
func (model dashboardModel) workspaceNavSections() []workspaceNavSection {
	if !model.isWorkspace() {
		return nil
	}
	sections := []workspaceNavSection{{items: []workspaceNavItem{
		{label: "Overview", route: OverviewRoute},
		{label: "Targets (all)", route: TargetsRoute},
		{label: "Packages", route: PackagesRoute},
		{label: "Tasks", route: TasksRoute},
		{label: "Toolchains", route: ToolchainsRoute},
		{label: "Presets", route: PresetsRoute},
		{label: "Doctor", route: DoctorRoute},
		{label: "Settings", route: SettingsRoute},
	}}}
	for _, name := range model.workspaceMembers() {
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
		sections = append(sections, workspaceNavSection{name: name, items: []workspaceNavItem{
			{label: fmt.Sprintf("Targets (%d)", targets), route: TargetsRoute, member: name},
			{label: fmt.Sprintf("Packages (%d)", packages), route: PackagesRoute, member: name},
			{label: fmt.Sprintf("Tests (%d)", tests), route: TestsRoute, member: name},
			{label: "Tasks", route: TasksRoute, member: name},
			{label: "Presets", route: PresetsRoute, member: name},
			{label: "Release", route: ReleaseRoute, member: name},
			{label: "Doctor", route: DoctorRoute, member: name},
			{label: "Settings", route: SettingsRoute, member: name},
		}})
	}
	return sections
}

// workspaceNavItems flattens the visible tree rows.
func (model dashboardModel) workspaceNavItems() []workspaceNavItem {
	var items []workspaceNavItem
	for _, section := range model.workspaceNavSections() {
		items = append(items, section.items...)
	}
	return items
}

// workspaceNavIndex returns the row matching the active screen, defaulting to the
// first row when the active screen is not part of the tree.
func (model dashboardModel) workspaceNavIndex() int {
	items := model.workspaceNavItems()
	for index, item := range items {
		if item.route == model.route && item.member == model.member {
			return index
		}
	}
	if model.route == BuildRoute {
		// Build is reached with `b` and has no tree row of its own; point the
		// highlight at the scope's Targets row so the tree stays in sync.
		for index, item := range items {
			if item.route == TargetsRoute && item.member == model.member {
				return index
			}
		}
	}
	if len(items) > 0 {
		return 0
	}
	return -1
}

// activateWorkspaceNav opens the tree row at index, carrying the row's member
// scope into the host so mutations write the right trestle.toml.
func (model *dashboardModel) activateWorkspaceNav(index int) dashboardModel {
	items := model.workspaceNavItems()
	if index < 0 || index >= len(items) {
		return *model
	}
	item := items[index]
	model.member = item.member
	model.applyScope()
	model.selectRoute(routeIndex(item.route))
	return *model
}

// workspaceNavLines renders the workspace tree section by section.
func (model dashboardModel) workspaceNavLines(p palette, width int) []string {
	active := model.workspaceNavIndex()
	var lines []string
	index := 0
	for _, section := range model.workspaceNavSections() {
		name := section.name
		if name == "" {
			name = "Root"
		}
		lines = append(lines, p.faint.Render("▼ "+name))
		for _, item := range section.items {
			lines = append(lines, model.workspaceNavRow(p, width, index, item, index == active))
			index++
		}
	}
	return lines
}

// workspaceNavRow renders one numbered tree row, highlighting the active screen.
func (model dashboardModel) workspaceNavRow(p palette, width, index int, item workspaceNavItem, active bool) string {
	number := "·"
	if index < 9 {
		number = fmt.Sprint(index + 1)
	} else if index == 9 {
		number = "0"
	}
	marker := "   "
	if active {
		marker = " ▸ "
	}
	label := ansi.Truncate(marker+number+" "+item.label, width-4, "…")
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
