package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/diag"
	"trestle/internal/runlog"
)

type logView struct {
	reader runlog.Reader
	width  int
	rows   []int // cumulative visual-row offsets; no log text retained
	count  int
	size   int64
}

func wrapText(line string, width int) []string {
	return strings.Split(ansi.Hardwrap(line, max(1, width), true), "\n")
}

func (log *logView) refresh(width int) error {
	stat, err := os.Stat(log.reader.Path)
	if err != nil {
		return err
	}
	if stat.Size() == log.size && width == log.width && len(log.rows) > 0 {
		return nil
	}
	if err := log.reader.Refresh(); err != nil {
		return err
	}
	count := log.reader.Count()
	if width != log.width || count < log.count {
		log.rows = nil
		log.count = 0
		log.width = width
	}
	if len(log.rows) == 0 {
		log.rows = []int{0}
	}
	// Reindex the previous last line too, since it can still be receiving bytes.
	start := max(0, log.count-1)
	log.rows = log.rows[:start+1]
	for index := start; index < count; index += 256 {
		lines, err := log.reader.Page(index, min(256, count-index))
		if err != nil {
			return err
		}
		for _, line := range lines {
			log.rows = append(log.rows, log.rows[len(log.rows)-1]+len(wrapText(line, width)))
		}
	}
	log.count = count
	log.size = stat.Size()
	return nil
}

func (log *logView) total() int {
	if len(log.rows) == 0 {
		return 0
	}
	return log.rows[len(log.rows)-1]
}
func (log *logView) page(start, height int) ([]string, error) {
	if log.total() == 0 {
		return nil, nil
	}
	index := sort.Search(len(log.rows), func(i int) bool { return log.rows[i] > start }) - 1
	index = max(0, index)
	lines, err := log.reader.Page(index, max(1, height))
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range lines {
		result = append(result, wrapText(line, log.width)...)
	}
	offset := max(0, start-log.rows[index])
	if offset >= len(result) {
		return nil, nil
	}
	return result[offset:min(len(result), offset+height)], nil
}

func (model *dashboardModel) receiveLog(line string, install bool) {
	model.latestInstall = install
	for _, part := range strings.Split(line, "\n") {
		var path string
		if strings.HasPrefix(part, "Log: ") {
			path = strings.TrimPrefix(part, "Log: ")
		}
		if strings.HasPrefix(part, "Log saved: ") {
			path = strings.TrimPrefix(part, "Log saved: ")
		}
		if path != "" && (model.diskLog == nil || model.diskLog.reader.Path != path) {
			model.diskLog = &logView{reader: runlog.Reader{Path: path}}
		}
	}
	if install {
		model.installLog = append(model.installLog, line)
		if len(model.installLog) > 512 {
			model.installLog = model.installLog[len(model.installLog)-512:]
		}
	} else {
		model.workflowLog = append(model.workflowLog, line)
		if len(model.workflowLog) > 512 {
			model.workflowLog = model.workflowLog[len(model.workflowLog)-512:]
		}
	}
}

func (model dashboardModel) hasLog() bool {
	return model.diskLog != nil || len(model.installLog) > 0 || len(model.workflowLog) > 0
}
func (model dashboardModel) logWidth() int {
	if model.fullLog {
		return max(1, model.width-2)
	}
	return max(1, model.contentWidth()-2)
}
func (model dashboardModel) logRows() []string {
	var result []string
	lines := model.workflowLog
	if model.latestInstall {
		lines = model.installLog
	}
	for _, line := range lines {
		result = append(result, wrapText(line, model.logWidth())...)
	}
	return result
}
func (model dashboardModel) logTotal() int {
	if model.diskLog != nil {
		if err := model.diskLog.refresh(model.logWidth()); err == nil {
			return model.diskLog.total()
		}
	}
	return len(model.logRows())
}
func (model dashboardModel) mainHeight() int { return max(1, model.height-4) }
func (model dashboardModel) logHeight() int {
	if model.logsCollapsed && !model.fullLog {
		return 3
	}
	if model.fullLog || model.height < 20 {
		return model.mainHeight()
	}
	return max(4, model.mainHeight()*3/5)
}
func (model dashboardModel) logCapacity() int  { return max(1, model.logHeight()-3) }
func (model dashboardModel) maxLogScroll() int { return max(0, model.logTotal()-model.logCapacity()) }
func (model *dashboardModel) scrollLog(delta int) {
	model.logScroll = min(max(0, model.logScroll+delta), model.maxLogScroll())
	model.logFollow = model.logScroll == model.maxLogScroll()
}

func (model dashboardModel) logPanel(p palette, width, height int) string {
	start := min(model.logScroll, model.maxLogScroll())
	if model.logFollow {
		start = model.maxLogScroll()
	}
	var lines []string
	if model.diskLog != nil {
		var err error
		lines, err = model.diskLog.page(start, max(1, height-3))
		if err != nil {
			lines = wrapText(err.Error(), max(1, width-2))
		}
	} else {
		lines = sliceViewport(model.logRows(), start, max(1, height-3))
	}
	for i, line := range lines {
		lines[i] = p.text.Render(line)
	}
	follow := "paused"
	if model.logFollow {
		follow = "following"
	}
	title := fmt.Sprintf("Output · %s · %d/%d · F2 fullscreen", follow, min(model.logTotal(), start+len(lines)), model.logTotal())
	return fittedPanel(p, title, lines, width, height, model.focus == focusLog)
}

func fittedPanel(p palette, title string, lines []string, width, height int, focused bool) string {
	width = max(1, width)
	height = max(1, height)
	if height < 3 || width < 4 {
		return fitRows(append([]string{title}, lines...), width, height)
	}
	inner := width - 2
	color := lipgloss.Color("#334155")
	if focused {
		color = lipgloss.Color("#38bdf8")
	}
	body := fitRows(append([]string{p.title.Render(title)}, lines...), inner, height-2)
	return lipgloss.NewStyle().Border(p.border).BorderForeground(color).Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(body)
}

func fitRows(lines []string, width, height int) string {
	result := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], max(1, width), "")
		}
		result = append(result, line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line))))
	}
	return strings.Join(result, "\n")
}

func (model dashboardModel) renderDashboard(p palette) string {
	width, height := max(1, model.width), max(1, model.height)
	mainHeight := model.mainHeight()
	var main string
	if model.help || model.details || model.history {
		var title string
		var lines []string
		switch {
		case model.help:
			title = "Keyboard reference · ? / Esc close"
			lines = wrapText("Tab / Shift+Tab: navigation, page, output\nF2: full output\nF3: complete error / item details\nF4: operation history\nF5: collapse or expand output\n↑↓ / j k: navigate focused panel\nPgUp/PgDn: page\nHome/End: first/last output (End resumes follow)\n/ in output: search · n next match\nb: build · r: refresh · p: profile\nEnter: selected action · 1…9 / 0: page\nEsc / Ctrl+C: cancel active operation\nq: stop operation and quit\nInputs: Enter save · Esc cancel · Ctrl+U clear\nTasks: Enter preview · y confirm · n cancel\nPackages: / search · Enter install · f features\nTargets: Enter build · f force · d default\nToolchains: Enter connect · c CUDA · v Vulkan\nTests: Enter selected · g group · a all\nSettings: Enter category / edit · / search all · Esc back · v rules", max(1, width-2))
		case model.details:
			title = "Complete details · F3 / Esc close"
			lines = wrapText(model.detailText, max(1, width-2))
		case model.history:
			title = "Operation history · Enter view · F4 / Esc close"
			for i, record := range model.runHistory {
				marker := "  "
				if i == model.historyCursor {
					marker = "› "
				}
				lines = append(lines, wrapText(marker+record.Started.Local().Format("2006-01-02 15:04:05")+" "+record.Status+" "+record.Operation+"\n  "+record.ID, max(1, width-2))...)
			}
			if len(lines) == 0 {
				lines = []string{"No saved operations yet."}
			}
		}
		main = fittedPanel(p, title, sliceViewport(lines, model.overlayScroll, max(1, mainHeight-3)), width, mainHeight, true)
	} else if model.fullLog {
		main = model.logPanel(p, width, mainHeight)
	} else {
		contentWidth := width
		nav := ""
		navWidth := 0
		if width >= 96 {
			navWidth = sidebarWidth(width)
			contentWidth = width - navWidth - 1
			nav = model.navigationPanel(p, navWidth, mainHeight)
		}
		title, subtitle := model.routeHeading()
		pageHeight := mainHeight
		showLog := true
		if showLog && height >= 20 {
			pageHeight = mainHeight - model.logHeight()
		}
		lines := model.contentLines(p, max(1, contentWidth-2))
		var wrapped []string
		for _, line := range lines {
			wrapped = append(wrapped, wrapText(line, max(1, contentWidth-2))...)
		}
		page := fittedPanel(p, title+" · "+subtitle, sliceViewport(wrapped, model.scroll, max(1, pageHeight-3)), contentWidth, pageHeight, model.focus == focusContent)
		if showLog && height < 20 {
			if model.focus == focusLog {
				page = model.logPanel(p, contentWidth, mainHeight)
			}
		} else if showLog {
			page = lipgloss.JoinVertical(lipgloss.Left, page, model.logPanel(p, contentWidth, model.logHeight()))
		}
		if nav != "" {
			main = lipgloss.JoinHorizontal(lipgloss.Top, nav, " ", page)
		} else {
			main = page
		}
	}
	header := strings.Split(model.header(p, width), "\n")[0]
	navLine := ""
	if width < 96 {
		navLine = model.compactNavigation(p, width)
	} else {
		navLine = p.faint.Render("Tab focus · F2 output · F3 details · F4 history")
	}
	footer := model.focusShortcut(p, width)
	return fitRows(strings.Split(header+"\n"+navLine+"\n"+main+"\n"+model.statusLine(p, width)+"\n"+footer, "\n"), width, height)
}

func (model dashboardModel) navigationPanel(p palette, width, height int) string {
	var lines []string
	for index, route := range routes {
		line := navigationKey(index) + " " + navigationLabel(route)
		if model.route == route {
			line = p.selected.Render("› " + line)
		} else {
			line = p.muted.Render("  " + line)
		}
		lines = append(lines, line)
	}
	start := max(0, routeIndex(model.route)-max(1, height-3)+1)
	return fittedPanel(p, "Workspace", sliceViewport(lines, start, max(1, height-3)), width, height, model.focus == focusNavigation)
}

func (model dashboardModel) focusShortcut(p palette, width int) string {
	if model.focus == focusLog {
		return ansi.Truncate(p.faint.Render("? help  F2 full  / search  End follow  Esc cancel  q quit"), width, "")
	}
	line := ansi.Strip(model.shortcutLine(p, max(width, 160)))
	return ansi.Truncate(p.faint.Render("? help  "+strings.TrimSpace(strings.Split(line, "? help")[0])+"  F2 log  F3 details  F5 collapse"), width, "")
}

func (model *dashboardModel) showDetails() {
	model.saveOverlayPosition()
	previous := model.detailText
	model.detailText = model.message
	if model.lastError != nil && model.messageError {
		model.detailText = diag.Text(model.lastError)
	} else if model.route == ToolchainsRoute {
		if item, ok := model.selectedComponent(); ok {
			model.detailText = fmt.Sprintf("%s\nPath: %s\nVersion: %s\nExecution: %s\nDistribution: %s\n%s", item.Name, item.Path, item.Version, item.Execution, item.Distribution, item.Detail)
		}
	} else if model.route == SettingsRoute {
		rows := model.filteredSettings()
		if model.cursor >= 0 && model.cursor < len(rows) && (model.settingsCategory != "" || model.settingsQuery != "") {
			row := rows[model.cursor]
			model.detailText = row.label + "\nKey: " + row.key + "\n\n" + fallback(row.value, "not set") + "\n\nEnter or e to edit; / to search all settings."
		}
	}
	model.details = true
	model.help, model.history = false, false
	model.overlayScroll = model.detailsScroll
	if previous != model.detailText {
		model.overlayScroll = 0
	}
}

func (model *dashboardModel) saveOverlayPosition() {
	if model.help {
		model.helpScroll = model.overlayScroll
	} else if model.details {
		model.detailsScroll = model.overlayScroll
	} else if model.history {
		model.historyScroll = model.overlayScroll
	}
}

func (model dashboardModel) historyOffset(index int) int {
	rows := 0
	for _, record := range model.runHistory[:min(index, len(model.runHistory))] {
		rows += len(wrapText("  "+record.Started.Local().Format("2006-01-02 15:04:05")+" "+record.Status+" "+record.Operation+"\n  "+record.ID, max(1, model.width-2)))
	}
	return max(0, rows-model.mainHeight()+5)
}

func (model dashboardModel) handlePanels(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "f2":
		model.fullLog = !model.fullLog
		model.focus = focusLog
		if model.logFollow {
			model.logScroll = model.maxLogScroll()
		}
		return model, nil, true
	case "f5":
		model.logsCollapsed = !model.logsCollapsed
		if model.logFollow {
			model.logScroll = model.maxLogScroll()
		}
		return model, nil, true
	case "f3":
		if model.details {
			model.saveOverlayPosition()
			model.details = false
		} else {
			model.showDetails()
		}
		return model, nil, true
	case "f4":
		model.saveOverlayPosition()
		model.history = !model.history
		model.help, model.details = false, false
		model.overlayScroll = model.historyScroll
		if model.history {
			records, err := runlog.List(model.path)
			if err != nil {
				model.setMessage(err.Error(), true)
			} else {
				model.runHistory = records
				model.historyCursor = min(model.historyCursor, max(0, len(records)-1))
			}
		}
		return model, nil, true
	}
	if model.help || model.details || model.history {
		switch key {
		case "esc", "?", "q":
			model.saveOverlayPosition()
			model.help, model.details, model.history = false, false, false
		case "up", "k":
			if model.history {
				model.historyCursor = max(0, model.historyCursor-1)
				model.overlayScroll = model.historyOffset(model.historyCursor)
			} else {
				model.overlayScroll = max(0, model.overlayScroll-1)
			}
		case "down", "j":
			if model.history {
				model.historyCursor = min(max(0, len(model.runHistory)-1), model.historyCursor+1)
				model.overlayScroll = model.historyOffset(model.historyCursor)
			} else {
				model.overlayScroll++
			}
		case "pgup":
			model.overlayScroll = max(0, model.overlayScroll-max(1, model.mainHeight()-3))
		case "pgdown":
			model.overlayScroll += max(1, model.mainHeight()-3)
		case "home":
			model.overlayScroll = 0
		case "enter":
			if model.history && len(model.runHistory) > 0 {
				record := model.runHistory[model.historyCursor]
				model.diskLog = &logView{reader: runlog.Reader{Path: record.LogPath}}
				model.history = false
				model.fullLog = true
				model.focus = focusLog
				model.logFollow = false
				model.logScroll = 0
				model.setMessage(record.Status+" · "+record.Operation, false)
			}
		}
		return model, nil, true
	}
	if key == "esc" && model.installCancel != nil {
		model.installCancel()
		model.setMessage("Cancelling installation…", false)
		return model, nil, true
	}
	if model.focus != focusLog {
		return model, nil, false
	}
	switch key {
	case "up", "k":
		model.scrollLog(-1)
	case "down", "j":
		model.scrollLog(1)
	case "pgup":
		model.scrollLog(-model.logCapacity())
	case "pgdown":
		model.scrollLog(model.logCapacity())
	case "home":
		model.logScroll = 0
		model.logFollow = false
	case "end", "G":
		model.logScroll = model.maxLogScroll()
		model.logFollow = true
	case "/":
		model.openInput("log-search", "Search complete output")
	case "n":
		model.findLog(true)
	case "esc":
		if model.fullLog {
			model.fullLog = false
		} else {
			return model, nil, false
		}
	default:
		return model, nil, false
	}
	return model, nil, true
}

func (model *dashboardModel) findLog(next bool) {
	start := 0
	if next {
		start = model.logScroll + 1
	}
	if model.diskLog != nil {
		if err := model.diskLog.refresh(model.logWidth()); err != nil {
			model.setMessage(err.Error(), true)
			return
		}
		line := sort.Search(len(model.diskLog.rows), func(i int) bool { return model.diskLog.rows[i] >= start })
		found, err := model.diskLog.reader.Find(model.logQuery, line)
		if err != nil {
			model.setMessage(err.Error(), true)
			return
		}
		if found >= 0 && found < len(model.diskLog.rows) {
			model.logScroll = model.diskLog.rows[found]
			model.logFollow = false
			return
		}
	} else {
		for i, line := range model.logRows() {
			if i >= start && strings.Contains(strings.ToLower(ansi.Strip(line)), strings.ToLower(model.logQuery)) {
				model.logScroll = i
				model.logFollow = false
				return
			}
		}
	}
	model.setMessage("No more matches for "+model.logQuery, false)
}
