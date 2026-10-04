package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/diag"
	"trestle/internal/policy"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

type BuildFunc func(context.Context, func(string)) error
type TargetBuildFunc func(context.Context, []string, bool, func(string)) error

type Services struct {
	Build             BuildFunc
	BuildTargets      TargetBuildFunc
	BuildTargetsForce TargetBuildFunc
	AssessTargets     func(context.Context) (config.Config, map[string]policy.Status, policy.Report, error)
	PreviewTask       func(context.Context, string) (config.TaskPreview, error)
	RunTask           func(context.Context, string, config.TaskPreview, func(string)) error
	AddPackage        func(string) error
	InstallPackage    func(context.Context, string, func(string)) error
	SetProfile        func(string) error
	SetCompileFlags   func(string) error
	SearchPackages    func(context.Context, string) ([]vcpkg.Port, error)
	RemovePackage     func(string) error
	SetPackageVersion func(string, string) error
	SetCompiler       func(string) error
	SetWSLCompiler    func(string, string) error
	SetEnvironment    func(string) error
	SetCUDA           func(bool, string) error
	SetCUDAConnection func(bool, string, string, string) error
	SetVulkan         func(bool, string, string, string) error
	SetTestGroup      func(string, string) error
	RunTests          func(context.Context, TestSelection, func(string)) error
	ImportCMake       func(context.Context, func(string)) error
	ImportXmake       func(context.Context, func(string)) error
	SetLanguageFlags  func(string, string) error
	ApplyPreset       func(string) error
	SavePreset        func(string) error
	SetDefaultTargets func([]string) error
	SetProjectSetting func(string, string) error
	DeletePreset      func(string) error
	ConfigureRelease  func(string, []string, string) error
	Release           func(context.Context, func(string)) error
	Doctor            func(context.Context, func(string)) error
}

type TestSelection struct {
	Job   string
	Group string
	File  string
	All   bool
}

type testJob struct {
	Name  string
	Group string
	Files []string
}

type Route string

const (
	OverviewRoute     Route = "Overview"
	TargetsRoute      Route = "Targets"
	TasksRoute        Route = "Tasks"
	DependenciesRoute Route = "Dependencies"
	ToolchainsRoute   Route = "Toolchains"
	PresetsRoute      Route = "Compiler Presets"
	ImportRoute       Route = "Project Import"
	PackagesRoute     Route = "Packages"
	TestsRoute        Route = "Tests"
	BuildRoute        Route = "Build"
	ReleaseRoute      Route = "Release"
	DoctorRoute       Route = "Doctor"
	SettingsRoute     Route = "Settings"
)

var routes = []Route{OverviewRoute, TargetsRoute, TasksRoute, DependenciesRoute, ToolchainsRoute, PresetsRoute, ImportRoute, PackagesRoute, TestsRoute, BuildRoute, ReleaseRoute, DoctorRoute, SettingsRoute}

var routeGlyph = map[Route]string{
	OverviewRoute: "⌂", TargetsRoute: "▦", TasksRoute: "⌘", DependenciesRoute: "◇", ToolchainsRoute: "⚙",
	PresetsRoute: "◫", ImportRoute: "⇣", PackagesRoute: "⬡", TestsRoute: "✓", BuildRoute: "▶", ReleaseRoute: "◆", DoctorRoute: "✚", SettingsRoute: "≡",
}

type focusArea uint8

const (
	focusNavigation focusArea = iota
	focusContent
	focusLog
)

type probeMessage struct {
	Fingerprint  string
	Config       config.Config
	BaseConfig   config.Config
	Statuses     map[string]policy.Status
	Rules        policy.Report
	ProjectName  string
	Sources      int
	Targets      int
	Toolchains   []toolchain.Toolchain
	Components   []toolchain.Component
	Vulkan       bool
	Ninja        bool
	Vcpkg        bool
	VcpkgRoot    string
	CMake        bool
	CMakeProject bool
	Xmake        bool
	XmakeProject bool
	Tests        []testJob
	Error        error
}

type pulseMessage time.Time
type mutationMessage struct {
	label string
	err   error
}
type installEvent struct {
	name string
	line string
	done bool
	err  error
}
type packageSearchMessage struct {
	ports []vcpkg.Port
	err   error
}
type workflowEvent struct {
	label string
	line  string
	done  bool
	err   error
}

type workflowBatch []workflowEvent
type installBatch []installEvent

type dashboardModel struct {
	path               string
	services           Services
	config             config.Config
	baseConfig         config.Config
	probe              probeMessage
	route              Route
	focus              focusArea
	width              int
	height             int
	probing            bool
	building           bool
	searching          bool
	mutating           bool
	operation          string
	pulse              int
	cursor             int
	scroll             int
	logFollow          bool
	logScroll          int
	logsCollapsed      bool
	batchingLogs       bool
	assessing          bool
	discovering        bool
	wslDiscovering     bool
	projectFingerprint string
	settingsCategory   string
	settingsQuery      string
	fullLog            bool
	diskLog            *logView
	latestInstall      bool
	details            bool
	detailText         string
	lastError          error
	history            bool
	runHistory         []runlog.Record
	historyCursor      int
	overlayScroll      int
	helpScroll         int
	detailsScroll      int
	historyScroll      int
	pagePositions      map[Route][2]int
	logQuery           string
	lastRun            string
	message            string
	messageError       bool
	help               bool
	inputMode          bool
	inputText          string
	inputKind          string
	inputPackage       string
	packageResults     []vcpkg.Port
	installEvents      chan installEvent
	installLog         []string
	installCancel      context.CancelFunc
	workflowEvents     chan workflowEvent
	workflowRoute      Route
	workflowCancel     context.CancelFunc
	quitAfterTask      bool
	workflowLog        []string
	workflowErrors     []string
	testFileMode       bool
	rulesView          bool
	pendingTask        string
	pendingTaskPreview config.TaskPreview
}

type palette struct {
	brand, title, text, muted, faint        lipgloss.Style
	accent, selected, good, warning, danger lipgloss.Style
	border                                  lipgloss.Border
}

func newPalette() palette {
	return palette{
		brand:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9")),
		title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f8fafc")),
		text:     lipgloss.NewStyle().Foreground(lipgloss.Color("#dbeafe")),
		muted:    lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")),
		faint:    lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")),
		accent:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7dd3fc")),
		selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ecfeff")).Background(lipgloss.Color("#17324d")),
		good:     lipgloss.NewStyle().Foreground(lipgloss.Color("#86efac")),
		warning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#fcd34d")),
		danger:   lipgloss.NewStyle().Foreground(lipgloss.Color("#fda4af")),
		border:   lipgloss.RoundedBorder(),
	}
}

func RunDashboard(path string, services Services) error {
	_, err := tea.NewProgram(newDashboard(path, services)).Run()
	return err
}

func newDashboard(path string, services Services) dashboardModel {
	return dashboardModel{
		path: path, services: services, route: OverviewRoute, focus: focusNavigation,
		width: 100, height: 30, probing: true, discovering: true, wslDiscovering: true, lastRun: "No build in this session",
		message: "Reading project configuration…",
	}
}

func (model dashboardModel) Init() tea.Cmd {
	return tea.Batch(model.probeCommand(), model.inventoryCommand(), model.wslInventoryCommand(), pulseCommand())
}

func pulseCommand() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(value time.Time) tea.Msg { return pulseMessage(value) })
}

func (model dashboardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case workflowBatch:
		model.batchingLogs = true
		var command tea.Cmd
		for _, event := range value {
			updated, cmd := model.Update(event)
			model = updated.(dashboardModel)
			command = cmd
			if event.done {
				break
			}
		}
		model.batchingLogs = false
		model.followVisibleLog(model.workflowRoute)
		return model, command
	case installBatch:
		model.batchingLogs = true
		var command tea.Cmd
		for _, event := range value {
			updated, cmd := model.Update(event)
			model = updated.(dashboardModel)
			command = cmd
			if event.done {
				break
			}
		}
		model.batchingLogs = false
		model.followVisibleLog(PackagesRoute)
		return model, command
	case tea.WindowSizeMsg:
		model.width, model.height = value.Width, value.Height
		if model.logFollow && model.hasLog() {
			model.logScroll = model.maxLogScroll()
		}
	case pulseMessage:
		model.pulse++
		if model.busy() {
			return model, pulseCommand()
		}
	case inventoryMessage:
		model.discovering = false
		for _, component := range model.probe.Components {
			if component.Execution == "wsl" || component.Family == "WSL" {
				value.components = append(value.components, component)
			}
		}
		model.probe.Components = value.components
		model.probe.Vulkan = false
		for _, component := range value.components {
			if component.Family == "Vulkan" && component.Ready {
				model.probe.Vulkan = true
			}
		}
	case wslInventoryMessage:
		model.wslDiscovering = false
		var components []toolchain.Component
		for _, component := range model.probe.Components {
			if component.Execution != "wsl" && component.Family != "WSL" {
				components = append(components, component)
			}
		}
		model.probe.Components = append(components, value.components...)
	case assessmentMessage:
		if value.fingerprint != model.projectFingerprint {
			return model, nil
		}
		model.assessing = false
		model.probe.Statuses, model.probe.Rules = value.statuses, value.rules
		if value.cfg.SchemaVersion != 0 {
			model.config = value.cfg
		}
		if value.err != nil {
			model.lastError = value.err
			model.setMessage(value.err.Error(), true)
		}
	case probeMessage:
		model.probing = false
		changed := value.Fingerprint != model.projectFingerprint
		value.Components, value.Toolchains, value.Vulkan = model.probe.Components, model.probe.Toolchains, model.probe.Vulkan
		if !changed {
			value.Statuses, value.Rules = model.probe.Statuses, model.probe.Rules
			value.Config = model.config
		}
		model.probe, model.config, model.baseConfig = value, value.Config, value.BaseConfig
		model.projectFingerprint = value.Fingerprint
		if value.Error != nil {
			model.lastError = value.Error
			model.setMessage(value.Error.Error(), true)
		} else if !model.mutating {
			model.setMessage("Project model is ready", false)
		}
		model.clampCursor()
		if changed && value.Error == nil && model.services.AssessTargets != nil {
			model.assessing = true
			return model, model.assessmentCommand()
		}
	case packageSearchMessage:
		model.searching = false
		if value.err != nil {
			model.lastError = value.err
			model.setMessage(value.err.Error(), true)
		} else {
			model.packageResults = value.ports
			model.cursor = len(model.config.Packages)
			model.scroll = max(0, model.cursor-2)
			model.setMessage(fmt.Sprintf("Found %d matching packages", len(value.ports)), false)
		}
	case mutationMessage:
		model.mutating = false
		model.operation = ""
		if value.err != nil {
			value.err = runlog.Failure(model.path, value.label, value.err)
			model.lastError = value.err
			model.setMessage(value.err.Error(), true)
			return model, nil
		}
		model.setMessage(value.label, false)
		return model, model.probeCommand()
	case installEvent:
		if !value.done {
			model.receiveLog(value.line, true)
			model.followVisibleLog(PackagesRoute)
			model.setMessage("Installing dependency · F2 complete output", false)
			return model, waitInstallEvent(model.installEvents)
		}
		if model.installCancel != nil {
			model.installCancel()
		}
		model.mutating, model.operation, model.installEvents, model.installCancel = false, "", nil, nil
		if value.err != nil {
			model.lastError = value.err
			model.receiveLog(diag.Text(value.err), true)
			model.followVisibleLog(PackagesRoute)
			model.setMessage("Install failed · "+value.err.Error(), true)
			if model.quitAfterTask {
				model.quitAfterTask = false
				return model, tea.Quit
			}
			return model, nil
		}
		if model.quitAfterTask {
			model.quitAfterTask = false
			return model, tea.Quit
		}
		model.setMessage("Installed and added "+value.name, false)
		return model, model.probeCommand()
	case workflowEvent:
		if !value.done {
			model.receiveLog(value.line, false)
			model.followVisibleLog(model.workflowRoute)
			if diagnosticLine(value.line) {
				model.workflowErrors = append(model.workflowErrors, value.line)
				if len(model.workflowErrors) > 512 {
					model.workflowErrors = model.workflowErrors[len(model.workflowErrors)-512:]
				}
			}
			model.setMessage(value.label+" running · F2 complete output", false)
			return model, waitWorkflowEvent(model.workflowEvents)
		}
		if model.workflowCancel != nil {
			model.workflowCancel()
		}
		model.mutating, model.operation, model.workflowEvents, model.workflowCancel = false, "", nil, nil
		quitAfterTask := model.quitAfterTask
		model.quitAfterTask = false
		if value.err != nil {
			model.lastError = value.err
			model.receiveLog(diag.Text(value.err), false)
			model.followVisibleLog(model.workflowRoute)
			switch {
			case errors.Is(value.err, context.Canceled):
				model.setMessage(value.label+" cancelled", false)
			case errors.Is(value.err, context.DeadlineExceeded):
				model.setMessage(value.label+" timed out · "+value.err.Error(), true)
			default:
				model.setMessage(value.label+" failed · "+value.err.Error(), true)
			}
			if quitAfterTask {
				return model, tea.Quit
			}
			return model, nil
		}
		model.setMessage(value.label+" completed", false)
		if value.label == "Build" {
			model.lastRun = "Completed just now"
		}
		if quitAfterTask {
			return model, tea.Quit
		}
		return model, model.probeCommand()
	case tea.MouseWheelMsg:
		mouse := value.Mouse()
		delta := 0
		switch mouse.Button {
		case tea.MouseWheelUp:
			delta = -3
		case tea.MouseWheelDown:
			delta = 3
		}
		if delta != 0 {
			if model.help || model.details || model.history {
				model.overlayScroll = max(0, model.overlayScroll+delta)
			} else if model.hasLog() && (model.fullLog || mouse.Y >= model.height-model.logHeight()-2 || model.focus == focusLog) {
				model.focus = focusLog
				model.scrollLog(delta)
			} else {
				model.focus = focusContent
				model.scrollContent(delta)
			}
		}
		return model, nil
	case tea.MouseClickMsg:
		if value.Mouse().Button == tea.MouseLeft {
			model.focus = focusContent
			if model.hasLog() && (model.fullLog || value.Mouse().Y >= model.height-model.logHeight()-2) {
				model.focus = focusLog
			}
		}
		return model, nil
	case tea.KeyPressMsg:
		return model.handleKey(value)
	}
	return model, nil
}

func (model dashboardModel) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.Key()
	if !model.inputMode && model.pendingTask == "" {
		if updated, cmd, handled := model.handlePanels(key.String()); handled {
			return updated, cmd
		}
		if updated, cmd, handled := model.handleSettingsKey(key.String()); handled {
			return updated, cmd
		}
	}
	if model.pendingTask != "" {
		switch key.String() {
		case "y", "enter":
			name := model.pendingTask
			preview := model.pendingTaskPreview
			model.pendingTask = ""
			model.pendingTaskPreview = config.TaskPreview{}
			if model.services.RunTask == nil {
				model.setMessage("Task service is unavailable", true)
				return model, nil
			}
			return model.startWorkflow("Task "+name, func(ctx context.Context, progress func(string)) error {
				return model.services.RunTask(ctx, name, preview, progress)
			})
		case "n", "esc", "q":
			model.pendingTask = ""
			model.pendingTaskPreview = config.TaskPreview{}
			model.setMessage("Task cancelled", false)
		}
		return model, nil
	}
	if model.inputMode {
		return model.handleInput(key)
	}
	if model.help {
		if key.String() == "?" || key.String() == "esc" || key.String() == "q" {
			model.help = false
		}
		return model, nil
	}
	if (key.String() == "esc" || key.String() == "ctrl+c") && model.mutating && model.workflowCancel != nil {
		model.workflowCancel()
		model.setMessage("Cancelling operation…", false)
		return model, nil
	}
	switch key.String() {
	case "q", "ctrl+c":
		if model.mutating && model.workflowCancel != nil {
			model.quitAfterTask = true
			model.workflowCancel()
			model.setMessage("Stopping operation before exit…", false)
			return model, nil
		}
		if model.installCancel != nil {
			model.installCancel()
			model.quitAfterTask = true
			model.setMessage("Stopping installation before exit…", false)
			return model, nil
		}
		if model.workflowCancel != nil {
			model.workflowCancel()
		}
		return model, tea.Quit
	case "?":
		model.saveOverlayPosition()
		model.details, model.history = false, false
		model.help = true
		model.overlayScroll = model.helpScroll
	case "tab":
		model.focus = (model.focus + 1) % 3
		if model.focus == focusContent {
			model.keepCursorVisible()
		}
	case "shift+tab":
		model.focus = (model.focus + 2) % 3
		if model.focus == focusContent {
			model.keepCursorVisible()
		}
	case "left", "h":
		model.focus = focusNavigation
	case "right", "l":
		model.focus = focusContent
	case "enter":
		if model.focus == focusNavigation {
			model.focus = focusContent
			model.keepCursorVisible()
		} else if model.route == PackagesRoute {
			index := model.cursor - len(model.config.Packages)
			if index >= 0 && index < len(model.packageResults) {
				name := model.packageResults[index].Name
				return model.installPackage(name)
			}
		} else if model.route == ToolchainsRoute {
			if component, ok := model.selectedComponent(); ok {
				if component.Family == "CUDA" && component.Ready {
					return model.connectCUDA(component)
				}
				if component.Family == "Vulkan" && component.Ready {
					return model.connectVulkan(component)
				}
				if _, compiler := model.selectedCompiler(); !compiler {
					break
				}
				if component.Execution == "wsl" {
					return model.startMutation("Selecting WSL compiler", "WSL compiler selected: "+component.Name+" · builds run inside "+component.Distribution, func() error {
						if model.services.SetWSLCompiler == nil {
							return fmt.Errorf("WSL compiler service is unavailable")
						}
						return model.services.SetWSLCompiler(component.Distribution, component.Path)
					})
				}
				return model.startMutation("Selecting compiler", "Compiler selected: "+component.Name, func() error { return callOne(model.services.SetCompiler, component.Path, "toolchain service") })
			}
		} else if model.route == ImportRoute {
			return model.startProjectImport()
		} else if model.route == TasksRoute {
			if name, ok := model.selectedTask(); ok {
				if model.services.PreviewTask == nil {
					model.setMessage("Task preview service is unavailable", true)
					return model, nil
				}
				preview, err := model.services.PreviewTask(context.Background(), name)
				if err != nil {
					model.setMessage(err.Error(), true)
					return model, nil
				}
				model.pendingTask = name
				model.pendingTaskPreview = preview
				model.setMessage("Review task and press y to run, n to cancel", false)
			}
			return model, nil
		} else if model.route == TargetsRoute || model.route == BuildRoute {
			return model.startSelectedTargetBuild()
		} else if model.route == TestsRoute {
			return model.runSelectedTest()
		} else if model.route == SettingsRoute {
			model.openSelectedSetting()
			return model, nil
		} else if model.route == PresetsRoute {
			if name, ok := model.selectedPreset(); ok {
				return model.startMutation("Applying compiler preset", "Applied preset "+name, func() error { return callOne(model.services.ApplyPreset, name, "preset service") })
			}
		} else if model.route == ReleaseRoute {
			return model.selectReleaseOptimization()
		} else if model.route == DoctorRoute && model.services.Doctor != nil {
			return model.startWorkflow("Doctor", model.services.Doctor)
		}
	case "up", "k":
		model.move(-1)
	case "down", "j":
		model.move(1)
	case "pgup":
		if model.focus == focusContent && (model.route == SettingsRoute && model.rulesView) {
			model.scrollContent(-max(1, model.viewportHeight()/2))
		} else {
			model.move(-max(1, model.viewportHeight()/2))
		}
	case "pgdown":
		if model.focus == focusContent && (model.route == SettingsRoute && model.rulesView) {
			model.scrollContent(max(1, model.viewportHeight()/2))
		} else {
			model.move(max(1, model.viewportHeight()/2))
		}
	case "home":
		model.cursor, model.scroll = 0, 0
		model.logFollow = false
	case "end", "G":
		if model.focus == focusContent && (model.route == SettingsRoute && model.rulesView) {
			model.scroll = model.maxContentScroll()
			model.logFollow = true
		} else {
			model.cursor = max(0, model.itemCount()-1)
			model.keepCursorVisible()
		}
	case "b":
		if model.route == ReleaseRoute {
			return model.startRelease()
		}
		return model.startBuild()
	case "r":
		if model.route == ReleaseRoute {
			return model.startRelease()
		}
		model.probing = true
		model.setMessage("Refreshing project files…", false)
		if model.route == ToolchainsRoute && !model.discovering && !model.wslDiscovering {
			model.discovering = true
			model.wslDiscovering = true
			return model, tea.Batch(model.probeCommand(), model.inventoryCommand(), model.wslInventoryCommand(), pulseCommand())
		}
		return model, model.probeCommand()
	case "ctrl+r":
		model.probing = true
		if !model.discovering && !model.wslDiscovering {
			model.discovering = true
			model.wslDiscovering = true
			return model, tea.Batch(model.probeCommand(), model.inventoryCommand(), model.wslInventoryCommand(), pulseCommand())
		}
		return model, model.probeCommand()
	case "p":
		profile := "release"
		if model.config.Build.Profile == "release" {
			profile = "debug"
		}
		return model.startMutation("Switching profile", "Profile changed to "+profile, func() error { return callOne(model.services.SetProfile, profile, "profile service") })
	case "a":
		if model.route == TestsRoute {
			return model.startTests(TestSelection{All: true}, "All tests")
		} else if model.route == TargetsRoute || model.route == BuildRoute {
			return model.startTargetBuild(nil, true, "Build all targets")
		} else if model.route == PresetsRoute {
			if name, ok := model.selectedPreset(); ok {
				return model.startMutation("Applying compiler preset", "Applied preset "+name, func() error { return callOne(model.services.ApplyPreset, name, "preset service") })
			}
			model.setMessage("No compiler preset is selected", true)
			return model, nil
		}
		model.route, model.focus = PackagesRoute, focusContent
		model.openInput("install", "Install vcpkg package by name")
	case "i":
		if model.route == ImportRoute {
			return model.startProjectImport()
		} else if model.route == PackagesRoute {
			index := model.cursor - len(model.config.Packages)
			if index >= 0 && index < len(model.packageResults) {
				return model.installPackage(model.packageResults[index].Name)
			}
		}
	case "f":
		if model.route == TargetsRoute || model.route == BuildRoute {
			if name, ok := model.selectedTarget(); ok {
				return model.startForcedTargetBuild(name)
			}
		} else if model.route == TestsRoute {
			model.testFileMode = !model.testFileMode
			model.cursor, model.scroll = 0, 0
			if model.testFileMode {
				model.setMessage("File view · enter runs the job owning that file", false)
			} else {
				model.setMessage("Job view", false)
			}
		} else if model.route == PackagesRoute {
			index := model.cursor - len(model.config.Packages)
			if index >= 0 && index < len(model.packageResults) && len(model.packageResults[index].Features) > 0 {
				model.openInput("features", "Features for "+model.packageResults[index].Name+" (comma separated)")
				model.inputPackage = model.packageResults[index].Name
			}
		}
	case "/", "s":
		model.route, model.focus = PackagesRoute, focusContent
		model.openInput("search", "Search vcpkg registry")
	case "d":
		if model.route == PackagesRoute {
			names := sortedPackageNames(model.config.Packages)
			if model.focus == focusContent && model.cursor >= 0 && model.cursor < len(names) {
				name := names[model.cursor]
				return model.startMutation("Removing dependency "+name, "Removed "+name, func() error {
					return callOne(model.services.RemovePackage, name, "package service")
				})
			}
			model.openInput("remove", "Remove configured package")
		} else if model.route == TargetsRoute {
			if name, ok := model.selectedTarget(); ok {
				return model.startMutation("Setting default target", "Default target: "+name, func() error {
					if model.services.SetDefaultTargets == nil {
						return fmt.Errorf("default target service is unavailable")
					}
					return model.services.SetDefaultTargets([]string{name})
				})
			}
		} else if model.route == PresetsRoute {
			if name, ok := model.selectedPreset(); ok {
				return model.startMutation("Deleting compiler preset", "Deleted preset "+name, func() error { return callOne(model.services.DeletePreset, name, "preset service") })
			}
		}
	case "v":
		if model.route == ToolchainsRoute {
			enabled := model.config.Toolchain.Vulkan == ""
			if enabled {
				if component, ok := model.detectedSDK("Vulkan"); ok {
					return model.connectVulkan(component)
				}
				model.setMessage("No Vulkan SDK was detected", true)
				return model, nil
			}
			return model.startMutation("Disconnecting Vulkan SDK", "Vulkan SDK disconnected", func() error {
				if model.services.SetVulkan == nil {
					return fmt.Errorf("Vulkan service is unavailable")
				}
				return model.services.SetVulkan(false, "", "native", "")
			})
		} else if model.route == SettingsRoute {
			model.rulesView = !model.rulesView
			model.cursor, model.scroll, model.logFollow = 0, 0, false
		} else if model.route == PackagesRoute {
			model.openInput("version", "Set package version (name@version)")
			names := sortedPackageNames(model.config.Packages)
			if model.cursor >= 0 && model.cursor < len(names) {
				model.inputText = names[model.cursor] + "@"
			}
		}
	case "e":
		if model.route == ToolchainsRoute {
			model.openInput("environment", "Environment setup script (type inherit to clear)")
		} else if model.route == SettingsRoute {
			model.openSelectedSetting()
		} else {
			model.route, model.focus = SettingsRoute, focusContent
			model.openInput("flags", "Set compiler flags")
		}
	case "x":
		if model.route == ToolchainsRoute {
			model.openInput("apply-preset", "Apply compiler preset")
		} else if model.route == SettingsRoute {
			model.openInput("cxx-flags", "Set C++-only compiler flags")
		}
	case "n":
		if model.route == ToolchainsRoute || model.route == PresetsRoute {
			model.openInput("save-preset", "Save current compiler setup as preset")
		}
	case "o":
		if model.route == ReleaseRoute {
			model.openInput("release-output", "Release ZIP output path")
			model.inputText = model.config.Package.Output
		} else if model.route == SettingsRoute {
			model.openInput("link-flags", "Set linker flags")
		}
	case "c":
		if model.route == ToolchainsRoute {
			enabled := model.config.Toolchain.CUDA == ""
			component, detected := model.detectedSDKForExecution("CUDA", model.config.Toolchain.Mode)
			if enabled && !detected {
				kind, prompt := "cuda", "CUDA toolkit root (no native toolkit was auto-detected)"
				if model.config.Toolchain.Mode == "wsl" {
					kind, prompt = "cuda-wsl", "CUDA toolkit root inside "+fallback(model.config.Toolchain.WSLDistribution, "the default WSL distribution")
				}
				model.openInput(kind, prompt)
				return model, nil
			}
			if enabled {
				return model.connectCUDA(component)
			}
			label := "CUDA disabled"
			return model.startMutation("Updating CUDA", label, func() error {
				if model.services.SetCUDAConnection != nil {
					return model.services.SetCUDAConnection(false, "", "native", "")
				}
				if model.services.SetCUDA == nil {
					return fmt.Errorf("CUDA service is unavailable")
				}
				return model.services.SetCUDA(false, "")
			})
		} else if model.route == SettingsRoute {
			model.openInput("c-flags", "Set C-only compiler flags")
		}
	case "u":
		if model.route == ToolchainsRoute {
			kind := "cuda"
			if model.config.Toolchain.Mode == "wsl" {
				kind = "cuda-wsl"
			}
			model.openInput(kind, "CUDA toolkit root")
		}
	case "g":
		if model.route == TestsRoute {
			if job, ok := model.selectedTestJob(); ok {
				return model.startTests(TestSelection{Group: job.Group}, "Test group "+job.Group)
			}
		} else {
			model.cursor, model.scroll = 0, 0
		}
	case "m":
		if model.route == TestsRoute {
			if job, ok := model.selectedTestJob(); ok {
				model.openInput("test-group", "Move "+job.Name+" to test group")
				model.inputPackage = job.Name
				model.inputText = job.Group
			}
		}
	case "t":
		if model.route == ReleaseRoute {
			model.openInput("release-targets", "Release targets (comma separated)")
			model.inputText = strings.Join(model.config.Package.Targets, ",")
		}
	default:
		if key.String() == "0" {
			model.selectRoute(9)
		} else if len(key.String()) == 1 && key.String()[0] >= '1' && key.String()[0] <= '9' {
			model.selectRoute(int(key.String()[0] - '1'))
		}
	}
	return model, nil
}

func (model dashboardModel) handleInput(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		text, kind, packageName := strings.TrimSpace(model.inputText), model.inputKind, model.inputPackage
		model.closeInput()
		if kind == "settings-search" {
			model.settingsQuery = text
			model.settingsCategory = ""
			model.cursor, model.scroll = 0, 0
			return model, nil
		}
		if kind == "log-search" {
			model.logQuery = text
			model.findLog(false)
			return model, nil
		}
		if text == "" && kind != "environment" && kind != "setting" {
			model.setMessage("Nothing changed", false)
			return model, nil
		}
		if kind == "search" {
			if model.services.SearchPackages == nil {
				model.setMessage("package search is unavailable", true)
				return model, nil
			}
			model.searching = true
			model.setMessage("Searching vcpkg registry for “"+text+"”…", false)
			return model, tea.Batch(func() tea.Msg {
				ports, err := model.services.SearchPackages(context.Background(), text)
				return packageSearchMessage{ports: ports, err: err}
			}, pulseCommand())
		}
		var action func() error
		label := "Saved"
		switch kind {
		case "package":
			action, label = func() error { return callOne(model.services.AddPackage, text, "package service") }, "Added "+text
		case "install":
			return model.installPackage(text)
		case "features":
			features := strings.Join(strings.FieldsFunc(text, func(value rune) bool { return value == ',' || value == ' ' }), ",")
			spec := packageName + "[" + features + "]"
			return model.installPackage(spec)
		case "flags":
			action, label = func() error { return callOne(model.services.SetCompileFlags, text, "settings service") }, "Compiler flags updated"
		case "c-flags", "cxx-flags", "link-flags":
			language := strings.TrimSuffix(kind, "-flags")
			action, label = func() error {
				if model.services.SetLanguageFlags == nil {
					return fmt.Errorf("language flag service is unavailable")
				}
				return model.services.SetLanguageFlags(language, text)
			}, strings.ToUpper(language)+" flags updated"
		case "apply-preset":
			action, label = func() error { return callOne(model.services.ApplyPreset, text, "preset service") }, "Applied preset "+text
		case "save-preset":
			action, label = func() error { return callOne(model.services.SavePreset, text, "preset service") }, "Saved preset "+text
		case "setting":
			action, label = func() error {
				if model.services.SetProjectSetting == nil {
					return fmt.Errorf("settings service is unavailable")
				}
				return model.services.SetProjectSetting(packageName, text)
			}, "Updated "+packageName
		case "release-output":
			action, label = func() error {
				if model.services.ConfigureRelease == nil {
					return fmt.Errorf("release service is unavailable")
				}
				return model.services.ConfigureRelease("", nil, text)
			}, "Release output updated"
		case "release-targets":
			targets := splitList(text)
			action, label = func() error {
				if model.services.ConfigureRelease == nil {
					return fmt.Errorf("release service is unavailable")
				}
				return model.services.ConfigureRelease("", targets, "")
			}, "Release targets updated"
		case "environment":
			action, label = func() error { return callOne(model.services.SetEnvironment, text, "environment service") }, "Build environment updated"
		case "cuda":
			action, label = func() error {
				if model.services.SetCUDA == nil {
					return fmt.Errorf("CUDA service is unavailable")
				}
				return model.services.SetCUDA(true, text)
			}, "CUDA enabled"
		case "cuda-wsl":
			action, label = func() error {
				if model.services.SetCUDAConnection == nil {
					return fmt.Errorf("WSL CUDA service is unavailable")
				}
				return model.services.SetCUDAConnection(true, text, "wsl", model.config.Toolchain.WSLDistribution)
			}, "WSL CUDA enabled"
		case "test-group":
			action, label = func() error {
				if model.services.SetTestGroup == nil {
					return fmt.Errorf("test suite service is unavailable")
				}
				return model.services.SetTestGroup(packageName, text)
			}, "Moved "+packageName+" to group "+text
		case "remove":
			action, label = func() error { return callOne(model.services.RemovePackage, text, "package service") }, "Removed "+text
		case "version":
			parts := strings.SplitN(text, "@", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				model.setMessage("Use name@version, for example fmt@11.0.2", true)
				return model, nil
			}
			action = func() error {
				if model.services.SetPackageVersion == nil {
					return fmt.Errorf("package service is unavailable")
				}
				return model.services.SetPackageVersion(parts[0], parts[1])
			}
			label = "Updated " + parts[0] + " to " + parts[1]
		}
		return model.startMutation("Saving changes", label, action)
	case "esc":
		model.closeInput()
		model.setMessage("Cancelled", false)
	case "backspace":
		if model.inputText != "" {
			_, size := utf8.DecodeLastRuneInString(model.inputText)
			model.inputText = model.inputText[:len(model.inputText)-size]
		}
	case "ctrl+u":
		model.inputText = ""
	case "ctrl+w":
		model.inputText = strings.TrimRight(model.inputText, " ")
		if index := strings.LastIndex(model.inputText, " "); index >= 0 {
			model.inputText = model.inputText[:index+1]
		} else {
			model.inputText = ""
		}
	default:
		if key.Text != "" {
			model.inputText += key.Text
		}
	}
	if model.inputMode && model.inputKind == "settings-search" {
		model.settingsQuery = model.inputText
		model.settingsCategory = ""
		model.cursor, model.scroll = 0, 0
	}
	return model, nil
}

func callOne(action func(string) error, value, service string) error {
	if action == nil {
		return fmt.Errorf("%s is unavailable", service)
	}
	return action(value)
}

func (model dashboardModel) startBuild() (tea.Model, tea.Cmd) {
	if model.busy() || model.services.Build == nil {
		return model, nil
	}
	model.route, model.focus = BuildRoute, focusContent
	return model.startWorkflow("Build", model.services.Build)
}

func (model dashboardModel) selectedTarget() (string, bool) {
	names := sortedTargetNames(model.config.Targets)
	if model.cursor < 0 || model.cursor >= len(names) {
		return "", false
	}
	return names[model.cursor], true
}

func (model dashboardModel) selectedTask() (string, bool) {
	names := sortedTaskNames(model.config.Tasks)
	if model.cursor < 0 || model.cursor >= len(names) {
		return "", false
	}
	return names[model.cursor], true
}

func sortedTaskNames(tasks map[string]config.Task) []string {
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type settingRow struct{ key, label, value string }

func settingsRows(cfg config.Config) []settingRow {
	rows := []settingRow{
		{"project.name", "Project name", cfg.Project.Name}, {"build.build_dir", "Build directory", cfg.Build.BuildDir}, {"build.c_standard", "C standard", cfg.Build.CStandard}, {"build.cxx_standard", "C++ standard", cfg.Build.CXXStandard}, {"build.compile_flags", "Common compile flags", strings.Join(cfg.Build.CompileFlags, " ")}, {"build.c_flags", "C flags", strings.Join(cfg.Build.CFlags, " ")}, {"build.cxx_flags", "C++ flags", strings.Join(cfg.Build.CXXFlags, " ")}, {"build.link_flags", "Link flags", strings.Join(cfg.Build.LinkFlags, " ")}, {"build.default_targets", "Default targets", strings.Join(cfg.Build.DefaultTargets, ",")}, {"build.compile_commands", "Compile commands", cfg.Build.CompileCommands}, {"toolchain.c", "C compiler", cfg.Toolchain.C}, {"toolchain.cxx", "C++ compiler", cfg.Toolchain.CXX}, {"toolchain.msvc", "MSVC toolset", cfg.Toolchain.MSVC}, {"toolchain.archiver", "Archiver", cfg.Toolchain.Archiver}, {"toolchain.linker", "Linker", cfg.Toolchain.Linker}, {"toolchain.setup", "Environment script", cfg.Toolchain.Setup}, {"toolchain.cuda", "CUDA version", cfg.Toolchain.CUDA}, {"toolchain.cuda_root", "CUDA root override", cfg.Toolchain.CUDARoot}, {"toolchain.cache_dir", "Toolchain cache", cfg.Toolchain.CacheDir}, {"toolchain.cuda_mode", "CUDA mode", cfg.Toolchain.CUDAMode}, {"toolchain.cuda_architectures", "CUDA architectures", strings.Join(cfg.Toolchain.CUDAArchitectures, ",")}, {"vcpkg.root", "vcpkg root", cfg.Vcpkg.Root}, {"vcpkg.triplet", "vcpkg triplet", cfg.Vcpkg.Triplet}, {"vcpkg.crt_linkage", "CRT linkage", cfg.Vcpkg.CRTLinkage}, {"vcpkg.library_linkage", "Library linkage", cfg.Vcpkg.LibraryLinkage}, {"package.output", "Release ZIP", cfg.Package.Output}}
	rows = append(rows, settingRow{"toolchain.mode", "Execution mode", cfg.Toolchain.Mode}, settingRow{"toolchain.wsl_distribution", "WSL distribution", cfg.Toolchain.WSLDistribution}, settingRow{"toolchain.vulkan", "Vulkan SDK", cfg.Toolchain.Vulkan}, settingRow{"toolchain.vulkan_execution", "Vulkan execution", cfg.Toolchain.VulkanExecution}, settingRow{"toolchain.vulkan_wsl_distribution", "Vulkan distribution", cfg.Toolchain.VulkanWSLDistribution}, settingRow{"toolchain.cuda_execution", "CUDA execution", cfg.Toolchain.CUDAExecution}, settingRow{"toolchain.cuda_wsl_distribution", "CUDA distribution", cfg.Toolchain.CUDAWSLDistribution})
	for _, name := range sortedTargetNames(cfg.Targets) {
		target := cfg.Targets[name]
		prefix := "target:" + name + ":"
		label := "[" + name + "] "
		rows = append(rows, settingRow{prefix + "type", label + "Type", target.Type}, settingRow{prefix + "sources", label + "Sources", strings.Join(target.Sources, ",")}, settingRow{prefix + "output_name", label + "Output name", target.OutputName}, settingRow{prefix + "c_standard", label + "C standard", target.CStandard}, settingRow{prefix + "cxx_standard", label + "C++ standard", target.CXXStandard}, settingRow{prefix + "include_dirs", label + "Include dirs", strings.Join(target.IncludeDirs, ",")}, settingRow{prefix + "private_include_dirs", label + "Private includes", strings.Join(target.PrivateIncludeDirs, ",")}, settingRow{prefix + "defines", label + "Defines", strings.Join(target.Defines, ",")}, settingRow{prefix + "private_defines", label + "Private defines", strings.Join(target.PrivateDefines, ",")}, settingRow{prefix + "compile_options", label + "Compile options", strings.Join(target.CompileOptions, " ")}, settingRow{prefix + "c_flags", label + "C flags", strings.Join(target.CFlags, " ")}, settingRow{prefix + "cxx_flags", label + "C++ flags", strings.Join(target.CXXFlags, " ")}, settingRow{prefix + "library_dirs", label + "Library dirs", strings.Join(target.LibraryDirs, ",")}, settingRow{prefix + "libraries", label + "Libraries", strings.Join(target.Libraries, ",")}, settingRow{prefix + "link_options", label + "Link options", strings.Join(target.LinkOptions, " ")}, settingRow{prefix + "export_all_symbols", label + "Export all symbols", fmt.Sprint(target.ExportAllSymbols)})
	}
	presetNames := make([]string, 0, len(cfg.CompilerPresets))
	for name := range cfg.CompilerPresets {
		presetNames = append(presetNames, name)
	}
	sort.Strings(presetNames)
	for _, name := range presetNames {
		preset := cfg.CompilerPresets[name]
		prefix := "preset:" + name + ":"
		label := "{preset " + name + "} "
		rows = append(rows, settingRow{prefix + "c", label + "C compiler", preset.C}, settingRow{prefix + "cxx", label + "C++ compiler", preset.CXX}, settingRow{prefix + "archiver", label + "Archiver", preset.Archiver}, settingRow{prefix + "linker", label + "Linker", preset.Linker}, settingRow{prefix + "setup", label + "Environment", preset.Setup}, settingRow{prefix + "mode", label + "Execution", preset.Mode}, settingRow{prefix + "wsl_distribution", label + "WSL distribution", preset.WSLDistribution}, settingRow{prefix + "compile_flags", label + "Common flags", strings.Join(preset.CompileFlags, " ")}, settingRow{prefix + "c_flags", label + "C flags", strings.Join(preset.CFlags, " ")}, settingRow{prefix + "cxx_flags", label + "C++ flags", strings.Join(preset.CXXFlags, " ")}, settingRow{prefix + "link_flags", label + "Link flags", strings.Join(preset.LinkFlags, " ")})
	}
	return rows
}
func (model *dashboardModel) openSelectedSetting() {
	if model.settingsCategory == "" && model.settingsQuery == "" {
		groups := model.settingsGroups()
		if model.cursor >= 0 && model.cursor < len(groups) {
			model.settingsCategory = groups[model.cursor].key
			model.cursor, model.scroll = 0, 0
		}
		return
	}
	rows := model.filteredSettings()
	if model.cursor < 0 || model.cursor >= len(rows) {
		return
	}
	row := rows[model.cursor]
	model.openInput("setting", "Edit "+row.label)
	model.inputPackage = row.key
	model.inputText = row.value
}
func (model dashboardModel) presetNames() []string {
	names := make([]string, 0, len(model.config.CompilerPresets))
	for name := range model.config.CompilerPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (model dashboardModel) selectedPreset() (string, bool) {
	names := model.presetNames()
	if model.cursor < 0 || model.cursor >= len(names) {
		return "", false
	}
	return names[model.cursor], true
}
func (model dashboardModel) optimizationNames() []string {
	names := []string{"balanced", "speed", "size"}
	for _, name := range model.presetNames() {
		names = append(names, "custom:"+name)
	}
	return names
}
func (model dashboardModel) selectReleaseOptimization() (tea.Model, tea.Cmd) {
	names := model.optimizationNames()
	if model.cursor < 0 || model.cursor >= len(names) {
		return model, nil
	}
	name := names[model.cursor]
	return model.startMutation("Selecting release optimization", "Release optimization: "+name, func() error {
		if model.services.ConfigureRelease == nil {
			return fmt.Errorf("release service is unavailable")
		}
		return model.services.ConfigureRelease(name, nil, "")
	})
}
func (model dashboardModel) startRelease() (tea.Model, tea.Cmd) {
	if model.services.Release == nil {
		model.setMessage("release service is unavailable", true)
		return model, nil
	}
	return model.startWorkflow("Release", model.services.Release)
}
func splitList(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func (model dashboardModel) startSelectedTargetBuild() (tea.Model, tea.Cmd) {
	name, ok := model.selectedTarget()
	if !ok {
		model.setMessage("No target is selected", true)
		return model, nil
	}
	return model.startTargetBuild([]string{name}, false, "Build "+name)
}

func (model dashboardModel) startForcedTargetBuild(name string) (tea.Model, tea.Cmd) {
	if model.services.BuildTargetsForce == nil {
		model.setMessage("Force-build service is unavailable", true)
		return model, nil
	}
	model.route, model.focus = BuildRoute, focusContent
	return model.startWorkflow("Force build "+name, func(ctx context.Context, progress func(string)) error {
		return model.services.BuildTargetsForce(ctx, []string{name}, false, progress)
	})
}

func (model dashboardModel) startTargetBuild(targets []string, all bool, label string) (tea.Model, tea.Cmd) {
	if model.services.BuildTargets == nil {
		model.setMessage("Target build service is unavailable", true)
		return model, nil
	}
	model.route, model.focus = BuildRoute, focusContent
	return model.startWorkflow(label, func(ctx context.Context, progress func(string)) error {
		return model.services.BuildTargets(ctx, targets, all, progress)
	})
}

func (model dashboardModel) startMutation(progress, done string, action func() error) (tea.Model, tea.Cmd) {
	if model.busy() || action == nil {
		return model, nil
	}
	model.mutating = true
	model.operation = progress
	model.setMessage(progress+"…", false)
	return model, tea.Batch(func() tea.Msg { return mutationMessage{label: done, err: action()} }, pulseCommand())
}

func (model dashboardModel) installPackage(name string) (tea.Model, tea.Cmd) {
	if model.services.InstallPackage == nil {
		model.setMessage("Configure [vcpkg].root before installing packages", true)
		return model, nil
	}
	if model.busy() {
		return model, nil
	}
	events := make(chan installEvent, 64)
	installCtx, cancel := context.WithCancel(context.Background())
	model.mutating, model.operation = true, "Installing "+name
	model.installEvents, model.installLog, model.installCancel = events, nil, cancel
	model.logFollow, model.logScroll, model.diskLog, model.latestInstall, model.focus, model.lastError = true, 0, nil, true, focusLog, nil
	model.setMessage("Preparing vcpkg install for "+name+"…", false)
	start := func() tea.Msg {
		go func() {
			err := model.services.InstallPackage(installCtx, name, func(line string) {
				select {
				case events <- installEvent{name: name, line: line}:
				case <-installCtx.Done():
				}
			})
			events <- installEvent{name: name, done: true, err: err}
			close(events)
		}()
		return <-events
	}
	return model, tea.Batch(start, pulseCommand())
}

func waitInstallEvent(events <-chan installEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return installEvent{done: true, err: fmt.Errorf("installer stopped unexpectedly")}
		}
		batch := installBatch{event}
		if event.done {
			return batch
		}
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		for len(batch) < 256 {
			select {
			case next, ok := <-events:
				if !ok {
					return batch
				}
				batch = append(batch, next)
				if next.done {
					return batch
				}
			case <-timer.C:
				return batch
			}
		}
		return batch
	}
}

func (model dashboardModel) startWorkflow(label string, action func(context.Context, func(string)) error) (tea.Model, tea.Cmd) {
	if model.busy() || action == nil {
		return model, nil
	}
	events := make(chan workflowEvent, 64)
	workflowCtx, cancel := context.WithCancel(context.Background())
	model.mutating, model.operation = true, label
	model.workflowEvents, model.workflowLog, model.workflowErrors, model.workflowCancel = events, nil, nil, cancel
	model.workflowRoute = model.route
	model.logFollow, model.logScroll, model.diskLog, model.latestInstall, model.focus, model.lastError = true, 0, nil, false, focusLog, nil
	model.setMessage(label+"…", false)
	start := func() tea.Msg {
		go func() {
			err := action(workflowCtx, func(line string) {
				select {
				case events <- workflowEvent{label: label, line: line}:
				case <-workflowCtx.Done():
				}
			})
			events <- workflowEvent{label: label, done: true, err: err}
			close(events)
		}()
		return <-events
	}
	return model, tea.Batch(start, pulseCommand())
}

func waitWorkflowEvent(events <-chan workflowEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return workflowEvent{label: "Operation", done: true, err: fmt.Errorf("operation stopped unexpectedly")}
		}
		batch := workflowBatch{event}
		if event.done {
			return batch
		}
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		for len(batch) < 256 {
			select {
			case next, ok := <-events:
				if !ok {
					return batch
				}
				batch = append(batch, next)
				if next.done {
					return batch
				}
			case <-timer.C:
				return batch
			}
		}
		return batch
	}
}

func diagnosticLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "failed:") || strings.Contains(lower, "fatal error") ||
		strings.Contains(lower, " error c") || strings.Contains(lower, " error lnk") ||
		strings.Contains(lower, "undefined reference") || strings.Contains(lower, "unresolved external")
}

func (model dashboardModel) startCMakeImport() (tea.Model, tea.Cmd) {
	if model.services.ImportCMake == nil {
		model.setMessage("CMake import service is unavailable", true)
		return model, nil
	}
	if !model.probe.CMakeProject {
		model.setMessage("CMakeLists.txt was not found in this project", true)
		return model, nil
	}
	return model.startWorkflow("CMake import", model.services.ImportCMake)
}

func (model dashboardModel) startProjectImport() (tea.Model, tea.Cmd) {
	if model.probe.XmakeProject {
		if model.services.ImportXmake == nil {
			model.setMessage("Xmake import service is unavailable", true)
			return model, nil
		}
		return model.startWorkflow("Xmake import", model.services.ImportXmake)
	}
	return model.startCMakeImport()
}

func (model dashboardModel) startTests(selection TestSelection, label string) (tea.Model, tea.Cmd) {
	if model.services.RunTests == nil {
		model.setMessage("Test suite service is unavailable", true)
		return model, nil
	}
	return model.startWorkflow(label, func(ctx context.Context, progress func(string)) error {
		return model.services.RunTests(ctx, selection, progress)
	})
}

func (model dashboardModel) runSelectedTest() (tea.Model, tea.Cmd) {
	job, ok := model.selectedTestJob()
	if !ok {
		model.setMessage("No test job is selected", true)
		return model, nil
	}
	if model.testFileMode {
		rows := model.testFiles()
		if model.cursor >= 0 && model.cursor < len(rows) {
			return model.startTests(TestSelection{File: rows[model.cursor].file}, "File test "+filepath.Base(rows[model.cursor].file))
		}
	}
	return model.startTests(TestSelection{Job: job.Name}, "Test job "+job.Name)
}

func (model dashboardModel) selectedCompiler() (toolchain.Component, bool) {
	if model.cursor < 0 || model.cursor >= len(model.probe.Components) {
		return toolchain.Component{}, false
	}
	component := model.probe.Components[model.cursor]
	base := strings.ToLower(filepath.Base(component.Path))
	selectable := strings.Contains(base, "++") || strings.Contains(base, "clang-cl") || base == "cl" || base == "cl.exe" || component.Name == "MSVC"
	if component.Family == "WSL" && component.Execution == "wsl" {
		selectable = true
	}
	compiler := component.Ready && component.Path != "" && selectable && (component.Family == "Clang" || component.Family == "MSVC" || component.Family == "MinGW" || component.Family == "WSL")
	return component, compiler
}

func (model dashboardModel) selectedComponent() (toolchain.Component, bool) {
	if model.cursor < 0 || model.cursor >= len(model.probe.Components) {
		return toolchain.Component{}, false
	}
	return model.probe.Components[model.cursor], true
}

func (model dashboardModel) detectedSDK(family string) (toolchain.Component, bool) {
	preferredExecution := model.config.Toolchain.Mode
	if component, ok := model.detectedSDKForExecution(family, preferredExecution); ok {
		return component, true
	}
	for _, component := range model.probe.Components {
		if component.Ready && component.Family == family {
			return component, true
		}
	}
	return toolchain.Component{}, false
}

func (model dashboardModel) detectedSDKForExecution(family, preferredExecution string) (toolchain.Component, bool) {
	for _, component := range model.probe.Components {
		execution := fallback(component.Execution, "native")
		if component.Ready && component.Family == family && execution == preferredExecution {
			return component, true
		}
	}
	return toolchain.Component{}, false
}

func (model dashboardModel) connectCUDA(component toolchain.Component) (tea.Model, tea.Cmd) {
	execution := fallback(component.Execution, "native")
	return model.startMutation("Connecting CUDA", "CUDA connected: "+component.Name, func() error {
		if model.services.SetCUDAConnection != nil {
			return model.services.SetCUDAConnection(true, component.Detail, execution, component.Distribution)
		}
		if execution != "native" {
			return fmt.Errorf("WSL CUDA service is unavailable")
		}
		if model.services.SetCUDA == nil {
			return fmt.Errorf("CUDA service is unavailable")
		}
		return model.services.SetCUDA(true, component.Detail)
	})
}

func (model dashboardModel) connectVulkan(component toolchain.Component) (tea.Model, tea.Cmd) {
	execution := fallback(component.Execution, "native")
	return model.startMutation("Connecting Vulkan SDK", "Vulkan SDK connected: "+component.Name, func() error {
		if model.services.SetVulkan == nil {
			return fmt.Errorf("Vulkan service is unavailable")
		}
		return model.services.SetVulkan(true, component.Detail, execution, component.Distribution)
	})
}

type testFileRow struct {
	file string
	job  testJob
}

func (model dashboardModel) testFiles() []testFileRow {
	var rows []testFileRow
	for _, job := range model.probe.Tests {
		for _, file := range job.Files {
			rows = append(rows, testFileRow{file: file, job: job})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].file < rows[j].file })
	return rows
}

func (model dashboardModel) selectedTestJob() (testJob, bool) {
	if model.testFileMode {
		rows := model.testFiles()
		if model.cursor >= 0 && model.cursor < len(rows) {
			return rows[model.cursor].job, true
		}
		return testJob{}, false
	}
	if model.cursor >= 0 && model.cursor < len(model.probe.Tests) {
		return model.probe.Tests[model.cursor], true
	}
	return testJob{}, false
}

func (model *dashboardModel) openInput(kind, message string) {
	model.inputMode, model.inputKind, model.inputText, model.inputPackage = true, kind, "", ""
	model.setMessage(message, false)
}
func (model *dashboardModel) closeInput() {
	model.inputMode, model.inputKind, model.inputText, model.inputPackage = false, "", "", ""
}
func (model *dashboardModel) setMessage(message string, isError bool) {
	model.message, model.messageError = message, isError
}
func (model dashboardModel) busy() bool {
	return model.probing || model.building || model.searching || model.mutating
}

func (model *dashboardModel) selectRoute(index int) {
	if index < 0 || index >= len(routes) {
		return
	}
	if model.pagePositions == nil {
		model.pagePositions = map[Route][2]int{}
	}
	model.pagePositions[model.route] = [2]int{model.cursor, model.scroll}
	model.route = routes[index]
	position := model.pagePositions[model.route]
	model.cursor, model.scroll = position[0], position[1]
	model.clampCursor()

}

func (model *dashboardModel) move(delta int) {
	if model.focus == focusNavigation {
		index := routeIndex(model.route) + delta
		for index < 0 {
			index += len(routes)
		}
		model.selectRoute(index % len(routes))
		return
	}
	if model.itemCount() == 0 {
		model.scrollContent(delta)
		return
	}
	model.cursor += delta
	model.clampCursor()
	model.keepCursorVisible()
}

func (model *dashboardModel) clampCursor() {
	count := model.itemCount()
	if count == 0 {
		model.cursor, model.scroll = 0, 0
		return
	}
	model.cursor = min(max(model.cursor, 0), count-1)
	model.scroll = min(max(model.scroll, 0), model.maxContentScroll())
}

func (model *dashboardModel) keepCursorVisible() {
	row := 0
	found := false
	for _, line := range model.contentLines(newPalette(), max(1, model.contentWidth()-2)) {
		if strings.HasPrefix(strings.TrimSpace(ansi.Strip(line)), "› ") {
			found = true
			break
		}
		row += len(wrapText(line, max(1, model.contentWidth()-2)))
	}
	if !found {
		return
	}
	height := model.viewportHeight()
	if row < model.scroll {
		model.scroll = row
	}
	if row >= model.scroll+height {
		model.scroll = row - height + 1
	}
}

func (model dashboardModel) itemCount() int {
	switch model.route {
	case TargetsRoute:
		return len(model.config.Targets)
	case TasksRoute:
		return len(model.config.Tasks)
	case DependenciesRoute:
		count := 0
		for _, target := range model.config.Targets {
			count += len(target.Dependencies)
		}
		return count
	case ToolchainsRoute:
		return len(model.probe.Components)
	case PresetsRoute:
		return len(model.config.CompilerPresets)
	case PackagesRoute:
		return len(model.config.Packages) + len(model.packageResults)
	case TestsRoute:
		if model.testFileMode {
			return len(model.testFiles())
		}
		return len(model.probe.Tests)
	case BuildRoute:
		return len(model.config.Targets)
	case ReleaseRoute:
		return len(model.optimizationNames())
	case SettingsRoute:
		if model.rulesView {
			return 0
		}
		if model.settingsCategory == "" && model.settingsQuery == "" {
			return len(model.settingsGroups())
		}
		return len(model.filteredSettings())
	}
	return 0
}

func (model dashboardModel) viewportHeight() int {
	height := model.mainHeight()
	if model.height >= 20 && !model.fullLog {
		height -= model.logHeight()
	}
	return max(1, height-3)
}

func (model *dashboardModel) scrollContent(delta int) {
	maximum := model.maxContentScroll()
	model.scroll = min(max(model.scroll+delta, 0), maximum)
}

func (model dashboardModel) maxContentScroll() int {
	var wrapped []string
	for _, line := range model.contentLines(newPalette(), max(1, model.contentWidth()-2)) {
		wrapped = append(wrapped, wrapText(line, max(1, model.contentWidth()-2))...)
	}
	return max(0, len(wrapped)-model.viewportHeight())
}

func (model dashboardModel) routeHasLog() bool {
	switch model.route {
	case PackagesRoute:
		return len(model.installLog) > 0
	case BuildRoute, TasksRoute, TestsRoute, ImportRoute, ReleaseRoute:
		return len(model.workflowLog) > 0
	}
	return false
}

func (model *dashboardModel) followVisibleLog(route Route) {
	if model.batchingLogs {
		return
	}
	if model.logFollow && model.hasLog() {
		model.logScroll = model.maxLogScroll()
	}
}

func (model dashboardModel) contentWidth() int {
	if model.width < 96 {
		return model.width
	}
	navWidth := sidebarWidth(model.width)
	return max(30, model.width-navWidth-1)
}

func sidebarWidth(total int) int { return min(34, max(30, total/5)) }

func navigationLabel(route Route) string {
	switch route {
	case PresetsRoute:
		return "Presets"
	case ImportRoute:
		return "Import"
	}
	return string(route)
}

func navigationKey(index int) string {
	if index < 9 {
		return fmt.Sprint(index + 1)
	}
	if index == 9 {
		return "0"
	}
	return " "
}
func routeIndex(route Route) int {
	for index, item := range routes {
		if item == route {
			return index
		}
	}
	return 0
}

func (model dashboardModel) View() tea.View {
	content := model.renderDashboard(newPalette())
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Trestle · Project Console"
	return view
}

func (model dashboardModel) header(p palette, width int) string {
	project := model.probe.ProjectName
	if project == "" {
		project = "Loading project…"
	}
	left := p.brand.Render(" TRESTLE ") + " " + p.title.Render(project)
	right := model.pill(p, fallback(model.config.Build.Profile, "auto"), "accent") + " " + model.pill(p, model.activityLabel(), model.activityTone())
	space := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	first := left + strings.Repeat(" ", space) + right
	return ansi.Truncate(first, width, "") + "\n" + ansi.Truncate(p.faint.Render(" Unified workspace / build / dependency console"), width, "")
}

func (model dashboardModel) pill(p palette, value, tone string) string {
	style := p.faint
	switch tone {
	case "accent":
		style = p.accent
	case "good":
		style = p.good
	case "warning":
		style = p.warning
	case "danger":
		style = p.danger
	}
	return style.Copy().Background(lipgloss.Color("#111d2e")).Padding(0, 1).Render(value)
}

func (model dashboardModel) activityLabel() string {
	if model.busy() {
		frames, label := []string{"◐", "◓", "◑", "◒"}, "Inspecting"
		switch {
		case model.building:
			label = "Building"
		case model.searching:
			label = "Searching"
		case model.mutating:
			label = fallback(model.operation, "Working")
		}
		return frames[model.pulse%len(frames)] + " " + label
	}
	if model.probe.Error != nil || model.messageError {
		return "Needs attention"
	}
	return "Ready"
}

func (model dashboardModel) activityTone() string {
	if model.busy() {
		return "accent"
	}
	if model.probe.Error != nil || model.messageError {
		return "danger"
	}
	return "good"
}

func (model dashboardModel) sidebar(p palette, width, height int) string {
	lines := []string{p.faint.Render(" WORKSPACE"), ""}
	for index, route := range routes {
		label := fmt.Sprintf(" %s  %s  %s", navigationKey(index), routeGlyph[route], navigationLabel(route))
		if route == model.route {
			marker := "  "
			if model.focus == focusNavigation {
				marker = "▎ "
			}
			label = p.selected.Copy().Width(max(1, width-4)).Render(ansi.Truncate(marker+label, width-4, "…"))
		} else {
			label = p.muted.Render(ansi.Truncate("   "+label, width-4, "…"))
		}
		lines = append(lines, label)
	}
	lines = append(lines, "", p.faint.Render(" tab  switch focus"), p.faint.Render(" ↑↓   navigate"))
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color("#334155")).Width(width - 2).Height(max(1, height-2)).Render(strings.Join(lines, "\n"))
}

func (model dashboardModel) compactNavigation(p palette, width int) string {
	items := make([]string, 0, len(routes))
	for index, route := range routes {
		label := navigationLabel(route)
		if key := navigationKey(index); key != " " {
			label = key + ":" + label
		}
		if route == model.route {
			label = p.selected.Render(" " + label + " ")
		} else {
			label = p.faint.Render(label)
		}
		items = append(items, label)
	}
	return ansi.Truncate(strings.Join(items, "  "), width, " …")
}

func (model dashboardModel) contentPanel(p palette, width, height int) string {
	innerWidth, innerHeight := max(18, width-4), max(3, height-4)
	title, subtitle := model.routeHeading()
	heading := p.title.Render(routeGlyph[model.route]+"  "+title) + "  " + p.faint.Render(subtitle)
	lines := sliceViewport(model.contentLines(p, innerWidth), model.scroll, max(1, innerHeight-2))
	body := heading + "\n" + p.faint.Render(strings.Repeat("─", max(1, min(innerWidth, 72)))) + "\n" + strings.Join(lines, "\n")
	borderColor := lipgloss.Color("#334155")
	if model.focus == focusContent {
		borderColor = lipgloss.Color("#38bdf8")
	}
	return lipgloss.NewStyle().Border(p.border).BorderForeground(borderColor).Padding(0, 1).Width(width - 4).Height(max(1, height-2)).Render(body)
}

func (model dashboardModel) routeHeading() (string, string) {
	switch model.route {
	case OverviewRoute:
		return "Project overview", "live workspace snapshot"
	case TargetsRoute:
		return "Targets", "outputs and source sets"
	case TasksRoute:
		return "Tasks", "explicit commands and configuration edits"
	case DependenciesRoute:
		return "Dependency graph", "target and package edges"
	case ToolchainsRoute:
		return "Development environment", "compiler, runtime and SDK components"
	case PresetsRoute:
		return "Compiler presets", "save and reuse complete compiler parameter sets"
	case ImportRoute:
		return "Project import", "CMake File API and version-compatible Xmake introspection"
	case PackagesRoute:
		return "Packages", "vcpkg dependencies"
	case TestsRoute:
		return "Test suites", "jobs, groups and source-file selection"
	case BuildRoute:
		return "Build", "active execution profile"
	case ReleaseRoute:
		return "Release", "optimized target build and ZIP packaging"
	case DoctorRoute:
		return "Doctor", "actionable environment checks"
	case SettingsRoute:
		return "Settings", "resolved project configuration"
	}
	return string(model.route), ""
}

func (model dashboardModel) contentLines(p palette, width int) []string {
	if model.probing && model.probe.ProjectName == "" {
		return []string{"", p.accent.Render(model.activityLabel()), p.muted.Render("Reading trestle.toml and probing local development tools…")}
	}
	switch model.route {
	case OverviewRoute:
		return model.overviewLines(p, width)
	case TargetsRoute:
		return model.targetLines(p, width)
	case TasksRoute:
		return model.taskLines(p, width)
	case DependenciesRoute:
		return model.dependencyLines(p, width)
	case ToolchainsRoute:
		return model.toolchainLines(p, width)
	case PresetsRoute:
		return model.presetLines(p, width)
	case ImportRoute:
		return model.importLines(p, width)
	case PackagesRoute:
		return model.packageLines(p, width)
	case TestsRoute:
		return model.testLines(p, width)
	case BuildRoute:
		return model.buildLines(p, width)
	case ReleaseRoute:
		return model.releaseLines(p, width)
	case DoctorRoute:
		return model.doctorLines(p)
	case SettingsRoute:
		return model.settingsLines(p, width)
	}
	return nil
}

func (model dashboardModel) overviewLines(p palette, width int) []string {
	stats := []string{model.stat(p, fmt.Sprint(model.probe.Targets), "targets"), model.stat(p, fmt.Sprint(model.probe.Sources), "sources"), model.stat(p, fmt.Sprint(len(model.config.Packages)), "packages")}
	var lines []string
	if width >= 55 {
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, stats[0], "  ", stats[1], "  ", stats[2]))
	} else {
		lines = append(lines, stats...)
	}
	ready := 0
	for _, component := range model.probe.Components {
		if component.Ready {
			ready++
		}
	}
	lines = append(lines,
		"", p.title.Render("Getting started"),
		p.text.Render("Toolchains → Doctor → Packages → Build"),
		p.muted.Render("Select a compiler, check readiness, install dependencies, then build."),
		"", p.title.Render("Environment"),
		statusRow(p, model.probe.Ninja, "Ninja backend", "not found on PATH"),
		statusRow(p, model.probe.Vulkan, "Vulkan SDK", "optional; not detected"),
		statusRow(p, ready > 0, fmt.Sprintf("%d development components detected", ready), "no compiler or SDK detected"),
		statusRow(p, model.probe.Vcpkg, "vcpkg package installer", "online search works; installation needs vcpkg on PATH"),
		"", p.title.Render("Next action"),
	)
	if model.probe.Error != nil {
		lines = append(lines, p.danger.Render("Fix project configuration before building"), p.muted.Render(model.probe.Error.Error()))
	} else {
		lines = append(lines, p.text.Render("Press b to build the active profile"), p.muted.Render("Configuration, environment diagnostics, and actions stay in one console."))
	}
	return lines
}

func (model dashboardModel) stat(p palette, value, label string) string {
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color("#26364d")).Padding(0, 2).Render(p.accent.Render(value) + " " + p.muted.Render(label))
}

func statusRow(p palette, ok bool, success, failure string) string {
	if ok {
		return p.good.Render("✓") + "  " + p.text.Render(success)
	}
	return p.warning.Render("!") + "  " + p.muted.Render(failure)
}

func (model dashboardModel) targetLines(p palette, width int) []string {
	names := sortedTargetNames(model.config.Targets)
	if len(names) == 0 {
		return emptyState(p, "No targets configured", "Add a [targets.<name>] section to trestle.toml.")
	}
	var lines []string
	for index, name := range names {
		target := model.config.Targets[name]
		availability := "✓"
		if status, ok := model.probe.Statuses[name]; ok && !status.Ready {
			availability = "!"
		}
		defaultMark := " "
		for _, item := range model.config.Build.DefaultTargets {
			if item == name {
				defaultMark = "★"
			}
		}
		row := fmt.Sprintf("%s %s %-18s  %-12s  %3d sources", availability, defaultMark, name, target.Type, len(target.Sources))
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(row, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			output := target.OutputName
			if output == "" {
				output = name
			}
			lines = append(lines, p.faint.Render("     output "+output+" · "+fmt.Sprint(len(target.Dependencies))+" dependencies"))
			for _, reason := range model.probe.Statuses[name].Reasons {
				lines = appendWrappedLog(lines, p.warning, "! "+reason, width)
			}
		}
	}
	return append(lines, "", p.faint.Render("enter build selected   f force selected   d make default   a build all"))
}

func (model dashboardModel) taskLines(p palette, width int) []string {
	names := sortedTaskNames(model.config.Tasks)
	if len(names) == 0 {
		return emptyState(p, "No tasks configured", "Add a [tasks.<name>] section to trestle.toml.")
	}
	lines := []string{p.warning.Render("来源不明的 DSL 任务可能损害您的设备。仅运行可信项目中的任务。"), ""}
	for index, name := range names {
		task := model.config.Tasks[name]
		if model.pendingTask == name {
			task = model.pendingTaskPreview.Task
		}
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(name+"  "+task.Description, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			preview := model.pendingTaskPreview
			if model.pendingTask != name {
				preview = config.TaskPreview{}
			}
			if len(task.Command) > 0 {
				lines = appendWrappedLog(lines, p.text, "command: "+fmt.Sprintf("%q", task.Command), width)
			}
			workingDir := fallback(preview.WorkingDir, fallback(task.WorkingDir, "project directory"))
			lines = appendWrappedLog(lines, p.muted, "working directory: "+workingDir, width)
			lines = appendWrappedLog(lines, p.muted, "timeout: "+fallback(task.Timeout, "none"), width)
			keys := make([]string, 0, len(task.Set))
			for key := range task.Set {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				lines = appendWrappedLog(lines, p.muted, "set "+key+" = "+task.Set[key], width)
			}
			if model.pendingTask == name {
				lines = append(lines, "", p.faint.Render(" CONFIGURATION CHANGES AFTER SUCCESS"))
				if len(preview.Changes) == 0 {
					lines = append(lines, p.muted.Render("  no configuration value changes"))
				}
				for _, change := range preview.Changes {
					lines = appendWrappedLog(lines, p.text, change.Field+": "+fallback(change.Before, "(empty)")+" → "+fallback(change.After, "(empty)"), width)
				}
				lines = append(lines, p.faint.Render("  Files changed by the command itself are not rolled back."))
			}
		}
	}
	if model.pendingTask != "" {
		lines = append(lines, "", p.warning.Render("Run "+model.pendingTask+"?  y / enter confirm · n / esc cancel"))
	} else {
		lines = append(lines, "", p.faint.Render("enter preview · esc / ctrl+c cancel a running task"))
	}
	return lines
}

func (model dashboardModel) dependencyLines(p palette, width int) []string {
	type edge struct{ from, to, scope string }
	var edges []edge
	for _, name := range sortedTargetNames(model.config.Targets) {
		for _, dependency := range model.config.Targets[name].Dependencies {
			to := dependency.Target
			if to == "" {
				to = dependency.Package
			}
			edges = append(edges, edge{name, to, dependency.Scope})
		}
	}
	if len(edges) == 0 {
		return emptyState(p, "No dependency edges", "Targets are currently independent.")
	}
	lines := make([]string, 0, len(edges))
	for index, edge := range edges {
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(fmt.Sprintf("%-18s  ──%-9s──▶  %s", edge.from, edge.scope, edge.to), width-3, "…")))
	}
	return lines
}

func (model dashboardModel) toolchainLines(p palette, width int) []string {
	cudaState := "disabled"
	if model.config.Toolchain.CUDA != "" {
		cudaState = fallback(model.config.Toolchain.CUDAExecution, "native") + " · " + model.config.Toolchain.CUDA
	}
	vulkanState := "auto-detect native"
	if model.config.Toolchain.Vulkan != "" {
		vulkanState = fallback(model.config.Toolchain.VulkanExecution, "native") + " · " + model.config.Toolchain.Vulkan
	}
	preset := fallback(model.config.Toolchain.Preset, "none")
	execution := "Windows native"
	if model.config.Toolchain.Mode == "wsl" {
		execution = "WSL · " + fallback(model.config.Toolchain.WSLDistribution, "default distribution")
	}
	lines := []string{p.faint.Render(" ACTIVE TOOLCHAIN"), keyValue(p, "Execution", execution), keyValue(p, "Compiler", fallback(model.config.Toolchain.CXX, "auto")), keyValue(p, "Preset", preset), keyValue(p, "Environment", fallback(model.config.Toolchain.Setup, "inherited")), keyValue(p, "CUDA", cudaState), keyValue(p, "Vulkan", vulkanState), "", p.faint.Render(" DETECTED · ENTER CONNECTS A COMPILER OR SDK")}
	if len(model.probe.Components) == 0 {
		lines = append(lines, p.warning.Render("!  No native development components detected"), p.faint.Render("   WSL mode may still use a compiler installed inside the selected distribution."))
	}
	for index, component := range model.probe.Components {
		marker := p.good.Render("●")
		if !component.Ready {
			marker = p.faint.Render("○")
		}
		version := component.Version
		if version == "" {
			version = component.Detail
		}
		version = compactLine(version)
		row := marker + "  " + fmt.Sprintf("%-10s %-22s %s", component.Family, component.Name, version)
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(row, width-2, "…")))
		if index == model.cursor && component.Path != "" && model.focus == focusContent {
			lines = append(lines, p.faint.Render("     "+ansi.Truncate(component.Path, max(10, width-6), "…")))
			if component.Execution == "wsl" {
				lines = append(lines, p.warning.Render(ansi.Truncate("     Notice: selecting this compiler runs build commands inside WSL "+component.Distribution, max(10, width-3), "…")))
			}
		} else if index == model.cursor && !component.Ready && component.Family == "WSL" && model.focus == focusContent {
			lines = append(lines, p.warning.Render("     "+ansi.Truncate(compactLine(component.Detail), max(10, width-6), "…")))
		}
	}
	return append(lines, "", p.faint.Render("enter connect native or WSL compiler/SDK   c CUDA   v Vulkan   x apply preset"))
}

func (model dashboardModel) presetLines(p palette, width int) []string {
	names := model.presetNames()
	lines := []string{p.faint.Render(" CUSTOM COMPILER PRESETS")}
	if len(names) == 0 {
		lines = append(lines, p.muted.Render("  No custom presets. Configure Settings, then press n to save the current toolchain and flags."))
	}
	for index, name := range names {
		preset := model.config.CompilerPresets[name]
		summary := fmt.Sprintf("%-20s  %s  C:%d C++:%d link:%d", name, fallback(filepath.Base(preset.CXX), "current compiler"), len(preset.CFlags), len(preset.CXXFlags), len(preset.LinkFlags))
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(summary, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			lines = append(lines, p.faint.Render("     common: "+ansi.Truncate(strings.Join(preset.CompileFlags, " "), max(10, width-15), "…")), p.faint.Render("     C++:    "+ansi.Truncate(strings.Join(preset.CXXFlags, " "), max(10, width-15), "…")), p.faint.Render("     link:   "+ansi.Truncate(strings.Join(preset.LinkFlags, " "), max(10, width-15), "…")))
		}
	}
	return append(lines, "", p.faint.Render("enter/a apply   n save current settings as preset   d delete"))
}

func (model dashboardModel) importLines(p palette, width int) []string {
	lines := []string{
		statusRow(p, model.probe.CMakeProject, "CMakeLists.txt detected", "No CMakeLists.txt in the project root"),
		statusRow(p, model.probe.CMake, "CMake executable available", "CMake is not available on PATH"),
		statusRow(p, model.probe.XmakeProject, "xmake.lua detected", "No xmake.lua in the project root"),
		statusRow(p, model.probe.Xmake, "Xmake executable available", "Xmake is not available on PATH"),
		"", p.title.Render("Import pipeline"),
		p.text.Render("CMake File API / Xmake targets  →  trestle.toml"),
		p.muted.Render("The importer runs in .trestle/import/cmake and leaves your normal build tree untouched."),
		"", p.title.Render("Mapped model"),
		p.muted.Render("Executable, static/shared/module library targets; sources, includes, defines and target edges."),
		p.faint.Render("Generated-only, utility, interface and object targets are reported and skipped."),
	}
	return append(lines, "", p.accent.Render("Press enter or i to auto-import this project"))
}

func (model dashboardModel) testLines(p palette, width int) []string {
	if len(model.probe.Tests) == 0 {
		return emptyState(p, "No test jobs configured", "Add a target with type = \"test\", or import test executables and mark them as tests.")
	}
	mode := "JOB VIEW"
	if model.testFileMode {
		mode = "FILE VIEW"
	}
	lines := []string{p.faint.Render(" " + mode)}
	if model.testFileMode {
		for index, row := range model.testFiles() {
			text := fmt.Sprintf("%-34s  job %-18s  group %s", filepath.Base(row.file), row.job.Name, row.job.Group)
			lines = append(lines, model.selectableRow(p, index, ansi.Truncate(text, width-3, "…")))
			if index == model.cursor && model.focus == focusContent {
				lines = append(lines, p.faint.Render("     "+ansi.Truncate(row.file, max(12, width-6), "…")))
			}
		}
	} else {
		for index, job := range model.probe.Tests {
			text := fmt.Sprintf("%-24s  group %-16s  %d files", job.Name, job.Group, len(job.Files))
			lines = append(lines, model.selectableRow(p, index, ansi.Truncate(text, width-3, "…")))
			if index == model.cursor && model.focus == focusContent && len(job.Files) > 0 {
				lines = append(lines, p.faint.Render("     "+ansi.Truncate(strings.Join(job.Files, ", "), max(12, width-6), "…")))
			}
		}
	}
	return append(lines, "", p.faint.Render("enter run selected   g run group   a run all   f job/file view   m assign group"))
}

func (model dashboardModel) packageLines(p palette, width int) []string {
	names := sortedPackageNames(model.config.Packages)
	installer := "installer unavailable"
	if model.probe.Vcpkg {
		installer = "installer " + model.probe.VcpkgRoot
	}
	lines := []string{p.good.Render("● online index") + p.faint.Render("  vcpkg.io · "+installer), ""}
	if len(names) == 0 {
		lines = append(lines, p.muted.Render("No packages configured."))
	} else {
		lines = append(lines, p.faint.Render(" CONFIGURED"))
		for index, name := range names {
			pkg := model.config.Packages[name]
			version := pkg.Version
			if version == "" {
				version = "latest compatible"
			}
			featureText := ""
			if len(pkg.Features) > 0 {
				featureText = " [" + strings.Join(pkg.Features, ",") + "]"
			}
			lines = append(lines, model.selectableRow(p, index, ansi.Truncate(fmt.Sprintf("%-22s %-18s %s", name+featureText, version, pkg.Triplet), width-3, "…")))
		}
	}
	if len(model.packageResults) > 0 {
		lines = append(lines, "", p.faint.Render(" VCPKG.IO RESULTS · ENTER TO DOWNLOAD & INSTALL"))
		for index, port := range model.packageResults {
			marker := "○"
			_, configured := model.config.Packages[port.Name]
			if port.Installed || configured {
				marker = "✓"
			}
			lines = append(lines, model.selectableRow(p, len(names)+index, ansi.Truncate(fmt.Sprintf("%s  %-22s %s", marker, port.Name, port.Version), width-3, "…")))
			if len(names)+index == model.cursor && model.focus == focusContent {
				lines = append(lines, p.text.Render("     "+ansi.Truncate(port.Description, max(12, width-7), "…")))
				metadata := []string{"source " + fallback(port.Source, "vcpkg.io")}
				if port.License != "" {
					metadata = append(metadata, "license "+port.License)
				}
				if port.Updated != "" {
					metadata = append(metadata, "updated "+port.Updated)
				}
				lines = append(lines, p.faint.Render("     "+strings.Join(metadata, " · ")))
				if len(port.Features) > 0 {
					lines = append(lines, p.faint.Render("     features: "+ansi.Truncate(strings.Join(port.Features, ", "), max(12, width-16), "…")))
				}
				if len(port.Dependencies) > 0 {
					lines = append(lines, p.faint.Render("     depends:  "+ansi.Truncate(strings.Join(port.Dependencies, ", "), max(12, width-16), "…")))
				}
				if port.PackageURL != "" {
					lines = append(lines, p.faint.Render("     details:  "+ansi.Truncate(port.PackageURL, max(12, width-16), "…")))
				}
			}
		}
	}
	return append(lines, "", p.faint.Render("/ search website   enter/i install   f features   a install by name"))
}

func (model dashboardModel) buildLines(p palette, width int) []string {
	state := p.good.Render("Ready")
	if model.mutating && model.operation == "Build" {
		state = p.accent.Render(model.activityLabel())
	} else if model.messageError && strings.HasPrefix(model.message, "Build failed") {
		state = p.danger.Render("Failed")
	}
	lines := []string{"", "  " + state, "", p.faint.Render(" ACTIVE PROFILE"), keyValue(p, "Profile", fallback(model.config.Build.Profile, "debug")), keyValue(p, "Default targets", fallback(strings.Join(model.config.Build.DefaultTargets, ", "), "automatic")), keyValue(p, "Build directory", fallback(model.config.Build.BuildDir, "build/{profile}")), keyValue(p, "C++ standard", fallback(model.config.Build.CXXStandard, "compiler default")), keyValue(p, "Compiler", fallback(model.config.Toolchain.CXX, "auto-detect")), "", p.faint.Render(" TARGETS · ENTER BUILDS SELECTED")}
	for index, name := range sortedTargetNames(model.config.Targets) {
		availability := "✓"
		if status, ok := model.probe.Statuses[name]; ok && !status.Ready {
			availability = "!"
		}
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(availability+" "+name+"  "+model.config.Targets[name].Type, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			for _, reason := range model.probe.Statuses[name].Reasons {
				lines = appendWrappedLog(lines, p.warning, "! "+reason, width)
			}
		}
	}
	lines = append(lines, "", p.faint.Render(" LAST RUN"), p.text.Render(model.lastRun))
	return append(lines, "", p.muted.Render("enter selected · f force selected · b defaults · a all · r refresh"))
}

func (model dashboardModel) releaseLines(p palette, width int) []string {
	lines := []string{p.faint.Render(" RELEASE PLAN"), keyValue(p, "ZIP output", model.config.Package.Output), keyValue(p, "Targets", fallback(strings.Join(model.config.Package.Targets, ", "), strings.Join(model.config.Build.DefaultTargets, ", "))), keyValue(p, "Selected optimization", fallback(model.config.Package.Optimization, "balanced")), "", p.faint.Render(" OPTIMIZATION PRESETS · ENTER SELECTS")}
	details := map[string]string{"balanced": "-O2 /O2 · safe default", "speed": "LTO · maximum throughput", "size": "dead-code elimination · smaller ZIP"}
	for index, name := range model.optimizationNames() {
		description := details[name]
		if strings.HasPrefix(name, "custom:") {
			description = "custom compiler and flag preset"
		}
		marker := " "
		if name == model.config.Package.Optimization {
			marker = "★"
		}
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(fmt.Sprintf("%s %-24s %s", marker, name, description), width-3, "…")))
	}
	return append(lines, "", p.accent.Render("r / b build release ZIP"), p.faint.Render("enter choose optimization   t targets   o ZIP output"))
}

func (model dashboardModel) doctorLines(p palette) []string {
	lines := []string{statusRow(p, model.probe.Error == nil, "trestle.toml parsed", "configuration could not be loaded"), statusRow(p, model.probe.Ninja, "Ninja is available", "Ninja is required for builds")}
	lines = append(lines, statusRow(p, model.probe.Vcpkg, "vcpkg installer: "+fallback(model.probe.VcpkgRoot, "detected"), "vcpkg installer is not configured"))
	compilerReady := false
	for _, component := range model.probe.Components {
		if component.Ready && (component.Family == "Clang" || component.Family == "MSVC" || component.Family == "MinGW") {
			compilerReady = true
			break
		}
	}
	lines = append(lines, statusRow(p, compilerReady, "C/C++ compiler detected", "No supported compiler detected"))
	if strings.Contains(strings.ToLower(model.config.Toolchain.CXX), "clang-cl") {
		lines = append(lines, statusRow(p, model.config.Toolchain.Setup != "", "MSVC ABI environment configured", "clang-cl needs a vcvars setup script"))
	}
	lines = append(lines, "", p.accent.Render("Enter: run complete checks and save diagnostics"), p.title.Render("Resolved toolchain"), keyValue(p, "Compiler", fallback(model.config.Toolchain.CXX, "auto")), keyValue(p, "Linker", fallback(model.config.Toolchain.Linker, "compiler default")), keyValue(p, "Environment", fallback(model.config.Toolchain.Setup, "inherited process environment")))
	if model.probe.Rules.ToolchainError != "" {
		lines = append(lines, p.warning.Render(model.probe.Rules.ToolchainError))
	}
	for _, name := range sortedTargetNames(model.config.Targets) {
		if readiness, ok := model.probe.Statuses[name]; ok {
			lines = append(lines, statusRow(p, readiness.Ready, name+": ready", name+": "+strings.Join(readiness.Reasons, "; ")))
		}
	}
	return lines
}

func (model dashboardModel) settingsLines(p palette, width int) []string {
	if model.rulesView {
		return model.ruleLines(p, width)
	}
	return model.settingsMenuLines(p, width)
}

func (model dashboardModel) editableConfig() config.Config {
	if model.baseConfig.SchemaVersion != 0 {
		return model.baseConfig
	}
	return model.config
}

func (model dashboardModel) ruleLines(p palette, width int) []string {
	lines := []string{p.faint.Render(" RESOLVED RULES · v BACK TO SETTINGS"), "", p.title.Render("Effective selection")}
	for _, item := range []struct{ label, value string }{
		{"Configured C", model.config.Toolchain.C}, {"Configured C++", model.config.Toolchain.CXX},
		{"Resolved C", model.probe.Rules.Toolchain.CC}, {"Resolved C++", model.probe.Rules.Toolchain.CXX},
		{"Resolved linker", model.probe.Rules.Toolchain.Linker}, {"Resolved archiver", model.probe.Rules.Toolchain.Archiver},
		{"Mode", model.config.Toolchain.Mode}, {"WSL distribution", model.config.Toolchain.WSLDistribution},
		{"Project compile flags", strings.Join(model.config.Build.CompileFlags, " ")},
		{"Project C flags", strings.Join(model.config.Build.CFlags, " ")},
		{"Project C++ flags", strings.Join(model.config.Build.CXXFlags, " ")},
		{"Project link flags", strings.Join(model.config.Build.LinkFlags, " ")},
		{"Default targets", strings.Join(model.config.Build.DefaultTargets, ", ")},
	} {
		lines = appendWrappedLog(lines, p.text, item.label+": "+fallback(item.value, "(none)"), width)
	}
	if model.probe.Rules.ToolchainError != "" {
		lines = appendWrappedLog(lines, p.warning, "Toolchain detection: "+model.probe.Rules.ToolchainError, width)
	}
	lines = append(lines, "", p.title.Render("Rules in TOML order"))
	if len(model.probe.Rules.Rules) == 0 {
		lines = append(lines, p.muted.Render("  No conditional rules configured."))
	}
	for _, result := range model.probe.Rules.Rules {
		state, style := "skipped", p.muted
		if result.Matched {
			state, style = "matched", p.good
		}
		label := fmt.Sprintf("#%d %s", result.Index, state)
		if result.Target != "" {
			label += " · target " + result.Target
		}
		lines = append(lines, style.Render("  "+label))
		lines = appendWrappedLog(lines, p.faint, "when: "+result.When, width)
		for _, change := range result.Changes {
			lines = appendWrappedLog(lines, p.text, change.Field+": "+fallback(change.Before, "(empty)")+" → "+fallback(change.After, "(empty)"), width)
		}
	}
	lines = append(lines, "", p.title.Render("Final changes from trestle.toml"))
	if len(model.probe.Rules.Changes) == 0 {
		lines = append(lines, p.muted.Render("  No configuration values changed."))
	}
	for _, change := range model.probe.Rules.Changes {
		lines = appendWrappedLog(lines, p.text, change.Field+": "+fallback(change.Before, "(empty)")+" → "+fallback(change.After, "(empty)"), width)
	}
	return append(lines, "", p.faint.Render("Target-specific C/C++/link flags apply only to that target."))
}

func (model dashboardModel) selectableRow(p palette, index int, row string) string {
	if index == model.cursor && model.focus == focusContent {
		return p.selected.Render("› " + row + " ")
	}
	if index == model.cursor {
		return p.text.Render("  " + row)
	}
	return p.muted.Render("  " + row)
}

func emptyState(p palette, title, detail string) []string {
	return []string{"", p.muted.Render("  ◌  ") + p.title.Render(title), p.faint.Render("     " + detail)}
}
func keyValue(p palette, key, value string) string {
	return fmt.Sprintf("  %-18s %s", p.muted.Render(key), p.text.Render(value))
}
func fallback(value, replacement string) string {
	if strings.TrimSpace(value) == "" {
		return replacement
	}
	return value
}

func compactLine(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	return strings.Join(strings.Fields(value), " ")
}

func (model dashboardModel) helpView(p palette, width, height int) string {
	lines := []string{
		p.title.Render("Keyboard reference") + "  " + p.faint.Render("press ? or esc to close"), "",
		keyValue(p, "↑ ↓ / j k", "navigate routes or rows"), keyValue(p, "tab / ← →", "switch navigation and content focus"),
		keyValue(p, "1 … 9 / 0", "jump directly to a screen"), keyValue(p, "b", "build the active profile"),
		keyValue(p, "r", "refresh configuration and probes"), keyValue(p, "p", "toggle debug/release profile"),
		keyValue(p, "/", "search the official vcpkg.io index"), keyValue(p, "enter / i", "run the selected screen action"),
		keyValue(p, "toolchains", "enter connect · e environment · c CUDA · v Vulkan"),
		keyValue(p, "tests", "enter job/file · g group · a all · f view"), keyValue(p, "packages", "f features · a install · d remove"),
		keyValue(p, "targets", "enter build · f force build (skip availability check)"), keyValue(p, "tasks", "enter preview · y confirm · n cancel"),
		keyValue(p, "q", "leave Project Console"), "", p.faint.Render("Inputs: enter saves · esc cancels · ctrl+u clears · ctrl+w deletes a word"),
	}
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color("#38bdf8")).Padding(1, 2).Width(max(20, width-6)).Height(max(3, height-4)).Render(strings.Join(lines, "\n"))
}

func (model dashboardModel) statusLine(p palette, width int) string {
	icon, style := p.good.Render("●"), p.muted
	if model.messageError {
		icon, style = p.danger.Render("●"), p.danger
	} else if model.busy() {
		icon, style = p.accent.Render("●"), p.text
	}
	line := icon + "  " + style.Render(model.message)
	if model.inputMode {
		line = p.faint.Render(model.inputLabel()+"  ") + p.accent.Render("› ") + p.text.Render(model.inputText) + p.accent.Render("▏") + p.faint.Render("   enter save · esc cancel")
	}
	return ansi.Truncate(line, width, "…")
}

func (model dashboardModel) inputLabel() string {
	switch model.inputKind {
	case "search":
		return "Search packages"
	case "settings-search":
		return "Search settings"
	case "log-search":
		return "Search complete output"
	case "package":
		return "Add package"
	case "install":
		return "Install package"
	case "features":
		return "Package features"
	case "remove":
		return "Remove package"
	case "version":
		return "Package version"
	case "flags":
		return "Compiler flags"
	case "c-flags":
		return "C flags"
	case "cxx-flags":
		return "C++ flags"
	case "link-flags":
		return "Link flags"
	case "apply-preset":
		return "Apply preset"
	case "save-preset":
		return "Save preset"
	case "setting":
		return "Project setting"
	case "release-output":
		return "ZIP output"
	case "release-targets":
		return "Release targets"
	case "environment":
		return "Setup script"
	case "cuda":
		return "CUDA root"
	case "cuda-wsl":
		return "WSL CUDA root"
	case "test-group":
		return "Test group"
	}
	return "Input"
}

func (model dashboardModel) shortcutLine(p palette, width int) string {
	contextKeys := "b build   r refresh   p profile"
	switch model.route {
	case ToolchainsRoute:
		contextKeys = "↵ native or WSL compiler/SDK   c CUDA   v Vulkan   x apply preset"
	case PresetsRoute:
		contextKeys = "↵ apply   n save current   d delete"
	case ImportRoute:
		contextKeys = "↵ / i auto import CMake/Xmake   r refresh"
	case PackagesRoute:
		contextKeys = "/ web search   ↵ install   f features   a by name"
	case TestsRoute:
		contextKeys = "↵ selected   g group   a all   f files   m group"
	case SettingsRoute:
		contextKeys = "↵ category / edit   / search   Esc back   v rules"
	case BuildRoute:
		contextKeys = "↵ selected   f force   b default   a all"
	case TasksRoute:
		contextKeys = "↵ preview   y confirm   esc cancel running"
	case ReleaseRoute:
		contextKeys = "↵ optimization   r release ZIP   t targets   o output"
	case TargetsRoute:
		contextKeys = "↵ build selected   f force   d default   a all"
	}
	left, right := p.faint.Render(" "+contextKeys), p.faint.Render("? help   wheel scroll   q quit ")
	return ansi.Truncate(left+strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))+right, width, "")
}

func appendWrappedLog(lines []string, style lipgloss.Style, line string, width int) []string {
	limit := max(12, width-3)
	for _, part := range strings.Split(ansi.Wrap(line, limit, " "), "\n") {
		length := ansi.StringWidth(part)
		if length == 0 {
			lines = append(lines, style.Render("  "))
			continue
		}
		for start := 0; start < length; start += limit {
			lines = append(lines, style.Render("  "+ansi.Cut(part, start, min(length, start+limit))))
		}
	}
	return lines
}

func sliceViewport(lines []string, offset, height int) []string {
	if len(lines) == 0 {
		return nil
	}
	offset = min(max(offset, 0), max(0, len(lines)-1))
	return lines[offset:min(len(lines), offset+max(1, height))]
}

func sortedTargetNames(values map[string]config.Target) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedPackageNames(values map[string]config.Package) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
