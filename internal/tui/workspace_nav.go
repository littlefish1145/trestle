package tui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
)

// workspaceNavItem is one selectable row in the workspace tree: a root-level
// screen, the aggregate build action, or a member's screens.
type workspaceNavItem struct {
	label    string
	route    Route
	member   string
	buildAll bool
}

// workspaceNavItems builds the tree rows in render order: the root section
// first, then one section per member. Non-workspace consoles have no tree.
func (model dashboardModel) workspaceNavItems() []workspaceNavItem {
	if !model.isWorkspace() {
		return nil
	}
	items := []workspaceNavItem{
		{label: "Overview", route: OverviewRoute},
		{label: "Toolchains", route: ToolchainsRoute},
		{label: "Build All", route: BuildRoute, buildAll: true},
	}
	for _, name := range model.workspaceMembers() {
		targets := 0
		if member, ok := model.probe.MemberConfigs[name]; ok {
			targets = len(member.Targets)
		}
		items = append(items,
			workspaceNavItem{label: fmt.Sprintf("Targets (%d)", targets), route: TargetsRoute, member: name},
			workspaceNavItem{label: "Packages", route: PackagesRoute, member: name},
		)
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
	if len(items) > 0 {
		return 0
	}
	return -1
}

// activateWorkspaceNav opens the tree row at index. Every row is a screen, so
// arrows and digits behave like the flat navigation of a standalone project:
// selecting Build All opens the build screen scoped to every member, and `b`
// starts the aggregate build.
func (model *dashboardModel) activateWorkspaceNav(index int) dashboardModel {
	items := model.workspaceNavItems()
	if index < 0 || index >= len(items) {
		return *model
	}
	item := items[index]
	model.member = item.member
	model.selectRoute(routeIndex(item.route))
	return *model
}

// workspaceNavLines renders the workspace tree. The root section groups the
// workspace-wide screens; every member gets its own section with target and
// package screens.
func (model dashboardModel) workspaceNavLines(p palette, width int) []string {
	items := model.workspaceNavItems()
	active := model.workspaceNavIndex()
	lines := []string{p.faint.Render("▼ Root")}
	for index := 0; index < len(items) && index < 3; index++ {
		lines = append(lines, model.workspaceNavRow(p, width, index, items[index], index == active))
	}
	for index := 3; index < len(items); index += 2 {
		lines = append(lines, p.faint.Render("▼ "+items[index].member))
		lines = append(lines, model.workspaceNavRow(p, width, index, items[index], index == active))
		if index+1 < len(items) {
			lines = append(lines, model.workspaceNavRow(p, width, index+1, items[index+1], index+1 == active))
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

// memberScopeLocked reports whether a package mutation must be refused because a
// member is selected: console mutations write the root configuration, so a
// member's package set is edited in its own trestle.toml.
func (model dashboardModel) memberScopeLocked() bool {
	return model.route == PackagesRoute && model.isWorkspace() && model.member != ""
}

// memberScopeMessage explains why a member-scoped package change is refused.
func (model dashboardModel) memberScopeMessage() string {
	return "Packages for member " + model.member + " are edited in " + model.member +
		"/trestle.toml; press w and clear the member to manage the workspace root's packages"
}
