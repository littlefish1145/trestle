package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type consoleCommand struct {
	label, detail, keywords string
	action                  string
	route                   Route
	target                  targetRow
	name                    string
}

func (model dashboardModel) consoleCommands() []consoleCommand {
	commands := []consoleCommand{
		{label: "Build default targets", detail: "b · active profile", keywords: "compile 构建 编译 默认", action: "build"},
		{label: "Build all targets", detail: "all targets in scope", keywords: "compile 构建 全部", action: "build-all"},
		{label: "Run all tests", detail: "all tests in scope", keywords: "test 测试", action: "test"},
		{label: "Check project environment", detail: "doctor", keywords: "diagnose 诊断 环境", action: "doctor"},
		{label: "Refresh project", detail: "ctrl+r", keywords: "reload 刷新", action: "refresh"},
		{label: "Switch build profile", detail: "p · debug / release", keywords: "配置 切换", action: "profile"},
		{label: "Search packages", detail: "vcpkg / conan", keywords: "dependency install 依赖 包 搜索", action: "packages"},
		{label: "Show complete output", detail: "F2", keywords: "logs 日志 输出", action: "output"},
		{label: "Show operation history", detail: "F4", keywords: "recent 历史", action: "history"},
		{label: "Toggle navigation", detail: "ctrl+b", keywords: "sidebar 侧栏 导航", action: "navigation"},
		{label: "Keyboard reference", detail: "?", keywords: "help 帮助 快捷键", action: "help"},
	}
	for _, route := range routes {
		commands = append(commands, consoleCommand{label: "Open " + navigationLabel(route), detail: "page", keywords: string(route), action: "route", route: route})
	}
	// The palette searches every target in the current scope, independent of a page filter.
	lookup := model
	lookup.targetQuery = ""
	for _, row := range lookup.targetRows() {
		commands = append(commands, consoleCommand{label: "Build " + row.label(), detail: "target · " + row.target.Type, keywords: "compile 构建 目标", action: "target", target: row})
	}
	for _, name := range sortedTaskNames(model.scopedTasks()) {
		commands = append(commands, consoleCommand{label: "Preview task " + name, detail: "confirm before running", keywords: "run task 任务", action: "task", name: name})
	}
	if model.isWorkspace() {
		commands = append(commands, consoleCommand{label: "Switch to all members", detail: "workspace", keywords: "scope workspace 全部 成员", action: "member"})
		for _, name := range model.workspaceMembers() {
			commands = append(commands, consoleCommand{label: "Switch to " + name, detail: "workspace member", keywords: "scope workspace 成员", action: "member", name: name})
		}
	}
	return commands
}

func (model dashboardModel) matchingCommands() []consoleCommand {
	query := strings.ToLower(strings.TrimSpace(model.commandQuery))
	commands := model.consoleCommands()
	if query == "" {
		return commands
	}
	type match struct {
		command consoleCommand
		score   int
	}
	var matches []match
	for _, command := range commands {
		label := strings.ToLower(command.label)
		search := label + " " + strings.ToLower(command.detail+" "+command.keywords)
		score, ok := commandMatch(search, query)
		if !ok {
			continue
		}
		if strings.Contains(label, query) {
			score -= 1000
		}
		matches = append(matches, match{command, score})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score < matches[j].score })
	result := make([]consoleCommand, len(matches))
	for i, match := range matches {
		result[i] = match.command
	}
	return result
}

// Match words in any order, with subsequence matching for abbreviated names.
func commandMatch(text, query string) (int, bool) {
	score := 0
	for _, word := range strings.Fields(query) {
		if index := strings.Index(text, word); index >= 0 {
			score += utf8.RuneCountInString(text[:index])
			continue
		}
		remaining := []rune(word)
		for _, char := range text {
			score++
			if char == remaining[0] {
				remaining = remaining[1:]
				if len(remaining) == 0 {
					break
				}
			}
		}
		if len(remaining) != 0 {
			return 0, false
		}
		score += 100
	}
	return score, true
}

func (model dashboardModel) handleCommandKey(key tea.Key) (tea.Model, tea.Cmd) {
	count := len(model.matchingCommands())
	switch key.String() {
	case "esc", "ctrl+c", "ctrl+p":
		model.commandsOpen = false
	case "enter":
		commands := model.matchingCommands()
		if count > 0 {
			command := commands[min(model.commandCursor, count-1)]
			model.commandsOpen = false
			return model.executeConsoleCommand(command)
		}
	case "up", "shift+tab":
		model.commandCursor = max(0, model.commandCursor-1)
	case "down", "tab", "ctrl+n":
		model.commandCursor = min(max(0, count-1), model.commandCursor+1)
	case "pgup":
		model.commandCursor = max(0, model.commandCursor-model.commandCapacity())
	case "pgdown":
		model.commandCursor = min(max(0, count-1), model.commandCursor+model.commandCapacity())
	case "home":
		model.commandCursor = 0
	case "end":
		model.commandCursor = max(0, count-1)
	case "backspace":
		if model.commandQuery != "" {
			_, size := utf8.DecodeLastRuneInString(model.commandQuery)
			model.commandQuery = model.commandQuery[:len(model.commandQuery)-size]
		}
		model.commandCursor = 0
	case "ctrl+u":
		model.commandQuery, model.commandCursor = "", 0
	case "ctrl+w":
		model.commandQuery = strings.TrimRight(model.commandQuery, " ")
		if index := strings.LastIndex(model.commandQuery, " "); index >= 0 {
			model.commandQuery = model.commandQuery[:index+1]
		} else {
			model.commandQuery = ""
		}
		model.commandCursor = 0
	default:
		if key.Text != "" {
			model.commandQuery += key.Text
			model.commandCursor = 0
		}
	}
	return model, nil
}

func (model dashboardModel) executeConsoleCommand(command consoleCommand) (tea.Model, tea.Cmd) {
	switch command.action {
	case "build", "build-all", "test", "doctor", "profile", "target", "task", "member":
		if model.busy() {
			model.setMessage("An operation is running · Esc cancels it before starting another", false)
			return model, nil
		}
	}
	model.saveOverlayPosition()
	model.help, model.details, model.history, model.fullLog = false, false, false, false
	switch command.action {
	case "build":
		return model.startBuild()
	case "build-all":
		return model.startTargetBuild(nil, true, "Build all targets")
	case "test":
		model.selectRoute(routeIndex(TestsRoute))
		return model.startTests(TestSelection{All: true}, "All tests")
	case "doctor":
		model.selectRoute(routeIndex(DoctorRoute))
		updated, cmd, _ := model.enterDoctor()
		return updated, cmd
	case "refresh":
		return model.forceRefreshKey()
	case "profile":
		return model.profileKey()
	case "packages":
		model.selectRoute(routeIndex(PackagesRoute))
		model.focus = focusContent
		model.openInput("search", "Search vcpkg and conan")
	case "output", "history":
		key := "f2"
		if command.action == "history" {
			key = "f4"
		}
		updated, cmd, _ := model.handleFunctionKey(key)
		return updated, cmd
	case "navigation":
		updated, cmd, _ := model.handleFocusKeys(tea.Key{Code: 'b', Mod: tea.ModCtrl})
		return updated, cmd
	case "help":
		model.help, model.details, model.history = true, false, false
		model.overlayScroll = model.helpScroll
	case "route":
		model.selectRoute(routeIndex(command.route))
		model.help, model.details, model.history, model.fullLog = false, false, false, false
		model.focus = focusContent
		model.keepCursorVisible()
	case "target":
		return model.startTargetRowBuild(command.target, false)
	case "task":
		model.selectRoute(routeIndex(TasksRoute))
		model.focus = focusContent
		for i, name := range sortedTaskNames(model.scopedTasks()) {
			if name == command.name {
				model.cursor = i
				break
			}
		}
		model.keepCursorVisible()
		updated, cmd, _ := model.enterTasks()
		return updated, cmd
	case "member":
		updated, cmd := model.applyMemberSwitch(command.name)
		model = updated.(dashboardModel)
		model.cursor, model.scroll, model.focus = 0, 0, focusContent
		model.pagePositions = nil
		return model, cmd
	}
	return model, nil
}

func (model dashboardModel) commandCapacity() int { return max(1, min(12, model.mainHeight()-5)) }

func (model dashboardModel) commandPanel(p palette, width, height int) string {
	commands := model.matchingCommands()
	inner := max(1, min(88, width-4))
	query := model.commandQuery
	if query == "" {
		query = "Search actions, pages, targets, tasks…"
	}
	rows := []string{p.title.Render("Commands"), "", p.accent.Render("› ") + p.text.Render(inputTail(query, max(1, inner-3))) + p.accent.Render("▏"), ""}
	start := min(max(0, model.commandCursor-model.commandCapacity()/2), max(0, len(commands)-model.commandCapacity()))
	for i := start; i < min(len(commands), start+model.commandCapacity()); i++ {
		command := commands[i]
		label := command.label
		if inner >= 60 {
			label = ansi.Truncate(label, max(1, inner-ansi.StringWidth(command.detail)-5), "…")
			label += strings.Repeat(" ", max(2, inner-ansi.StringWidth(label)-ansi.StringWidth(command.detail)-2)) + command.detail
		}
		label = ansi.Truncate(label, max(1, inner-2), "…")
		if i == model.commandCursor {
			label = p.selected.Copy().Width(inner).Render("› " + label)
		} else {
			label = p.muted.Render("  " + label)
		}
		rows = append(rows, label)
	}
	if len(commands) == 0 {
		rows = append(rows, p.muted.Render("No matching commands. Try another name."))
	}
	rows = append(rows, "", p.faint.Render(fmt.Sprintf("%d matches · ↑↓ select · enter run · esc close", len(commands))))
	indent := strings.Repeat(" ", max(0, (width-inner)/2))
	for i, row := range rows {
		rows[i] = indent + ansi.Truncate(row, inner, "")
	}
	return fitRows(rows, width, height)
}

func inputTail(text string, width int) string {
	length := ansi.StringWidth(text)
	if length <= width {
		return text
	}
	// A cut can land inside a double-width grapheme. Advance to its boundary
	// rather than letting that whole grapheme push the input cursor offscreen.
	for remove := length - width + 1; ; remove++ {
		tail := ansi.TruncateLeft(text, remove, "")
		if ansi.StringWidth(tail) <= max(0, width-1) {
			return "…" + tail
		}
	}
}
