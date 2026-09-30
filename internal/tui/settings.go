package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

type settingsGroup struct{ key, label, detail string }

func (model dashboardModel) settingsGroups() []settingsGroup {
	groups := []settingsGroup{
		{"project.", "Project", "name and workspace identity"},
		{"build.", "Build", "output directories, language standards and flags"},
		{"toolchain.", "Toolchain & SDKs", "compilers, environment, CUDA and Vulkan"},
		{"vcpkg.", "Dependencies", "vcpkg root, target triplet and linkage"},
		{"package.", "Release", "archive output"},
	}
	for _, name := range sortedTargetNames(model.editableConfig().Targets) {
		groups = append(groups, settingsGroup{"target:" + name + ":", "Target · " + name, "sources, flags, libraries and output"})
	}
	for _, name := range model.presetNames() {
		groups = append(groups, settingsGroup{"preset:" + name + ":", "Preset · " + name, "saved compiler settings"})
	}
	return groups
}

func (model dashboardModel) filteredSettings() []settingRow {
	var rows []settingRow
	query := strings.ToLower(strings.TrimSpace(model.settingsQuery))
	for _, row := range settingsRows(model.editableConfig()) {
		if query != "" {
			if strings.Contains(strings.ToLower(row.key+" "+row.label+" "+row.value), query) {
				rows = append(rows, row)
			}
		} else if strings.HasPrefix(row.key, model.settingsCategory) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (model dashboardModel) settingsMenuLines(p palette, width int) []string {
	if model.settingsCategory == "" && model.settingsQuery == "" {
		lines := []string{p.faint.Render("Choose a category · / search all settings")}
		for i, group := range model.settingsGroups() {
			lines = append(lines, model.selectableRow(p, i, ansi.Truncate(fmt.Sprintf("%-24s %s", group.label, group.detail), max(1, width-3), "…")))
		}
		return lines
	}
	label := "Settings / " + strings.TrimSuffix(model.settingsCategory, ".")
	if model.settingsQuery != "" {
		label = "Search: " + model.settingsQuery
	}
	rows := model.filteredSettings()
	lines := []string{p.faint.Render(fmt.Sprintf("%s · %d fields · Esc back · / search", label, len(rows)))}
	for i, row := range rows {
		lines = append(lines, model.selectableRow(p, i, ansi.Truncate(fmt.Sprintf("%-24s %s", row.label, fallback(row.value, "not set")), max(1, width-3), "…")))
	}
	if len(rows) == 0 {
		lines = append(lines, p.warning.Render("No matching settings. / to change search; Esc to clear."))
	}
	return lines
}

func (model dashboardModel) handleSettingsKey(key string) (tea.Model, tea.Cmd, bool) {
	if model.route != SettingsRoute || model.focus == focusLog || model.rulesView {
		return model, nil, false
	}
	switch key {
	case "/", "s":
		model.openInput("settings-search", "Search keys, labels and values")
		model.inputText = model.settingsQuery
		model.focus = focusContent
		return model, nil, true
	case "esc", "backspace":
		if model.settingsCategory != "" || model.settingsQuery != "" {
			model.settingsCategory, model.settingsQuery = "", ""
			model.cursor, model.scroll = 0, 0
			return model, nil, true
		}
	}
	return model, nil, false
}
