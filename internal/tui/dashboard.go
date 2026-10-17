// Package tui implements the interactive Trestle console: a Bubble Tea model
// that probes the project, its toolchains and dependencies, and drives build,
// test, package, release and doctor workflows from a single terminal view.
package tui

import (
	"context"
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
	"trestle/internal/deps"
	"trestle/internal/policy"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

// BuildFunc runs the default build for the configured profile, forwarding
// progress lines to the supplied callback.
type BuildFunc func(context.Context, func(string)) error

// TargetBuildFunc builds the named targets, forcing the build when force is
// true, and forwards progress lines to the callback.
type TargetBuildFunc func(context.Context, []string, bool, func(string)) error

// Services is the set of operations the console can perform. The host supplies
// one callback per action; a nil field disables that action, which the console
// then reports as unavailable.
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
	SearchPackages    func(context.Context, string) ([]deps.PackageEntry, error)
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

// TestSelection narrows a test run to a job, a group, a single source file, or
// every job, mirroring the filters the Tests screen offers.
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

// Route identifies one console screen. Its value is the label shown in the
// navigation sidebar.
type Route string

// The console screens, in navigation order.
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
	ports []deps.PackageEntry
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
	packageResults     []deps.PackageEntry
	packagePackages    []deps.Package
	packageLevel       packageLevel
	packageSelected    int
	packageRequested   string
	packageReturnRoute Route
	packageReturnFocus focusArea
	packageReturnCurs  int
	packageReturnScrol int
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
	lightBackground    bool
}

type palette struct {
	brand, title, text, muted, faint        lipgloss.Style
	accent, selected, good, warning, danger lipgloss.Style
	pillBg, borderMuted, borderFocus        string
	border                                  lipgloss.Border
}

// newPalette builds the console visual language. Titles and body text inherit
// the terminal foreground so prose follows the user's theme; brand, semantic
// and border colors are pinned to curated sRGB values per background mode so
// the look survives both light and dark terminals.
func newPalette(light bool) palette {
	if light {
		return palette{
			brand:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0e7490")),
			title:       lipgloss.NewStyle().Bold(true),
			text:        lipgloss.NewStyle(),
			muted:       lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")),
			faint:       lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")),
			accent:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0284c7")),
			selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ecfeff")).Background(lipgloss.Color("#0e7490")),
			good:        lipgloss.NewStyle().Foreground(lipgloss.Color("#15803d")),
			warning:     lipgloss.NewStyle().Foreground(lipgloss.Color("#b45309")),
			danger:      lipgloss.NewStyle().Foreground(lipgloss.Color("#be123c")),
			pillBg:      "#e2e8f0",
			borderMuted: "#cbd5e1",
			borderFocus: "#0284c7",
			border:      lipgloss.RoundedBorder(),
		}
	}
	return palette{
		brand:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9")),
		title:       lipgloss.NewStyle().Bold(true),
		text:        lipgloss.NewStyle(),
		muted:       lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")),
		faint:       lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")),
		accent:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7dd3fc")),
		selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ecfeff")).Background(lipgloss.Color("#17324d")),
		good:        lipgloss.NewStyle().Foreground(lipgloss.Color("#86efac")),
		warning:     lipgloss.NewStyle().Foreground(lipgloss.Color("#fcd34d")),
		danger:      lipgloss.NewStyle().Foreground(lipgloss.Color("#fda4af")),
		pillBg:      "#111d2e",
		borderMuted: "#334155",
		borderFocus: "#38bdf8",
		border:      lipgloss.RoundedBorder(),
	}
}

// palette picks the palette matching the terminal background reported by
// tea.BackgroundColorMsg.
func (model dashboardModel) palette() palette { return newPalette(model.lightBackground) }

// RunDashboard runs the interactive console against the project file at path
// until the user quits, returning any terminal error.
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

// Init starts the console's opening commands: the project probe, the native and
// WSL toolchain inventories, the animation pulse, and a background-colour query.
func (model dashboardModel) Init() tea.Cmd {
	return tea.Batch(model.probeCommand(), model.inventoryCommand(), model.wslInventoryCommand(), pulseCommand(), func() tea.Msg { return tea.RequestBackgroundColor() })
}

func pulseCommand() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(value time.Time) tea.Msg { return pulseMessage(value) })
}

// Update applies a Bubble Tea message to the console, returning the next model
// and any follow-up command.
func (model dashboardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case workflowBatch:
		return model.applyWorkflowBatch(value)
	case installBatch:
		return model.applyInstallBatch(value)
	case tea.WindowSizeMsg:
		return model.applyWindowSize(value), nil
	case tea.BackgroundColorMsg:
		return model.applyBackgroundColor(value), nil
	case pulseMessage:
		return model.applyPulse()
	case inventoryMessage:
		return model.applyInventory(value), nil
	case wslInventoryMessage:
		return model.applyWSLInventory(value), nil
	case assessmentMessage:
		return model.applyAssessment(value), nil
	case probeMessage:
		return model.applyProbe(value)
	case packageSearchMessage:
		return model.applyPackageSearch(value), nil
	case mutationMessage:
		return model.applyMutation(value)
	case installEvent:
		return model.applyInstallEvent(value)
	case workflowEvent:
		return model.applyWorkflowEvent(value)
	case tea.MouseWheelMsg:
		return model.applyMouseWheel(value), nil
	case tea.MouseClickMsg:
		return model.applyMouseClick(value), nil
	case tea.KeyPressMsg:
		return model.handleKey(value)
	}
	return model, nil
}

// handleKey routes a key press through the console's modal layers and then the
// active screen's key groups, in the order the overlays take precedence.
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
		return model.handlePendingTaskKey(key)
	}
	if model.inputMode {
		return model.handleInput(key)
	}
	if model.help {
		return model.handleHelpKey(key)
	}
	if (key.String() == "esc" || key.String() == "ctrl+c") && model.mutating && model.workflowCancel != nil {
		model.workflowCancel()
		model.setMessage("Cancelling operation…", false)
		return model, nil
	}
	if updated, cmd, handled := model.handleModalKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleFocusKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleMoveKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleActionKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleListKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleToolchainKeys(key); handled {
		return updated, cmd
	}
	if updated, cmd, handled := model.handleSurfaceKeys(key); handled {
		return updated, cmd
	}
	model.selectRouteByNumber(key)
	return model, nil
}

// handleInput edits the active text prompt: enter submits, esc cancels and the
// remaining keys edit the buffer. While searching settings every keystroke also
// refreshes the filter.
func (model dashboardModel) handleInput(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		return model.submitInput()
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
	rows = append(rows, settingRow{"conan.root", "conan home", cfg.Conan.Root}, settingRow{"conan.build_root", "conan build root", cfg.Conan.BuildRoot}, settingRow{"conan.profile", "conan profile", cfg.Conan.Profile})
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
		model.setMessage("Configure [vcpkg].root or [conan].root before installing packages", true)
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
	model.setMessage("Preparing install for "+name+"…", false)
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
	compiler := component.Ready && component.Path != "" && selectableCompilerName(component) && selectableCompilerFamily(component.Family)
	return component, compiler
}

// selectableCompilerName reports whether a component looks like a compiler the
// console can activate: a C++ driver, clang-cl, or the MSVC toolchain, plus any
// compiler reached through WSL.
func selectableCompilerName(component toolchain.Component) bool {
	base := strings.ToLower(filepath.Base(component.Path))
	if strings.Contains(base, "++") || strings.Contains(base, "clang-cl") || base == "cl" || base == "cl.exe" || component.Name == "MSVC" {
		return true
	}
	return component.Family == "WSL" && component.Execution == "wsl"
}

// selectableCompilerFamily reports whether a component's family is one the
// console can make the active compiler.
func selectableCompilerFamily(family string) bool {
	switch family {
	case "Clang", "MSVC", "MinGW", "WSL":
		return true
	}
	return false
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
	for _, line := range model.contentLines(model.palette(), max(1, model.contentWidth()-2)) {
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

// itemCounters maps each screen to the function that counts its selectable
// rows, so itemCount stays a single lookup.
var itemCounters = map[Route]func(dashboardModel) int{
	TargetsRoute:      func(model dashboardModel) int { return len(model.config.Targets) },
	TasksRoute:        func(model dashboardModel) int { return len(model.config.Tasks) },
	DependenciesRoute: dashboardModel.dependencyCount,
	ToolchainsRoute:   func(model dashboardModel) int { return len(model.probe.Components) },
	PresetsRoute:      func(model dashboardModel) int { return len(model.config.CompilerPresets) },
	PackagesRoute:     dashboardModel.packageItemCount,
	TestsRoute:        dashboardModel.testItemCount,
	BuildRoute:        func(model dashboardModel) int { return len(model.config.Targets) },
	ReleaseRoute:      func(model dashboardModel) int { return len(model.optimizationNames()) },
	SettingsRoute:     dashboardModel.settingsItemCount,
}

func (model dashboardModel) itemCount() int {
	if count, ok := itemCounters[model.route]; ok {
		return count(model)
	}
	return 0
}

// dependencyCount totals the dependency edges declared by every target.
func (model dashboardModel) dependencyCount() int {
	count := 0
	for _, target := range model.config.Targets {
		count += len(target.Dependencies)
	}
	return count
}

// packageItemCount counts selectable package rows: only the version list on the
// candidate level, otherwise the configured packages plus the search results.
func (model dashboardModel) packageItemCount() int {
	// The candidate level replaces the whole page, including the configured
	// list, so only the version rows are selectable there.
	if model.packageLevel == packageCandidatesLevel {
		return model.packageRows()
	}
	return len(model.config.Packages) + model.packageRows()
}

// testItemCount counts the rows the tests screen shows: files in file view,
// jobs otherwise.
func (model dashboardModel) testItemCount() int {
	if model.testFileMode {
		return len(model.testFiles())
	}
	return len(model.probe.Tests)
}

// settingsItemCount counts the rows the settings screen shows: the group menu,
// or the filtered list, and nothing at all in the rules view.
func (model dashboardModel) settingsItemCount() int {
	if model.rulesView {
		return 0
	}
	if model.settingsCategory == "" && model.settingsQuery == "" {
		return len(model.settingsGroups())
	}
	return len(model.filteredSettings())
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
	for _, line := range model.contentLines(model.palette(), max(1, model.contentWidth()-2)) {
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

// View renders the complete console frame for the current model state.
func (model dashboardModel) View() tea.View {
	content := model.renderDashboard(model.palette())
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
	return style.Copy().Background(lipgloss.Color(p.pillBg)).Padding(0, 1).Render(value)
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
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color(p.borderMuted)).Width(width - 2).Height(max(1, height-2)).Render(strings.Join(lines, "\n"))
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
	borderColor := p.borderMuted
	if model.focus == focusContent {
		borderColor = p.borderFocus
	}
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color(borderColor)).Padding(0, 1).Width(width - 4).Height(max(1, height-2)).Render(body)
}

// routeHeadings names each screen's panel title and subtitle.
var routeHeadings = map[Route][2]string{
	OverviewRoute:     {"Project overview", "live workspace snapshot"},
	TargetsRoute:      {"Targets", "outputs and source sets"},
	TasksRoute:        {"Tasks", "explicit commands and configuration edits"},
	DependenciesRoute: {"Dependency graph", "target and package edges"},
	ToolchainsRoute:   {"Development environment", "compiler, runtime and SDK components"},
	PresetsRoute:      {"Compiler presets", "save and reuse complete compiler parameter sets"},
	ImportRoute:       {"Project import", "CMake File API and version-compatible Xmake introspection"},
	PackagesRoute:     {"Packages", "vcpkg and conan dependencies"},
	TestsRoute:        {"Test suites", "jobs, groups and source-file selection"},
	BuildRoute:        {"Build", "active execution profile"},
	ReleaseRoute:      {"Release", "optimized target build and ZIP packaging"},
	DoctorRoute:       {"Doctor", "actionable environment checks"},
	SettingsRoute:     {"Settings", "resolved project configuration"},
}

func (model dashboardModel) routeHeading() (string, string) {
	if heading, ok := routeHeadings[model.route]; ok {
		return heading[0], heading[1]
	}
	return string(model.route), ""
}

// contentRenderers maps each screen to the function that renders its body. Every
// renderer takes the palette and the usable width; screens that ignore the width
// discard it.
var contentRenderers = map[Route]func(dashboardModel, palette, int) []string{
	OverviewRoute:     dashboardModel.overviewLines,
	TargetsRoute:      dashboardModel.targetLines,
	TasksRoute:        dashboardModel.taskLines,
	DependenciesRoute: dashboardModel.dependencyLines,
	ToolchainsRoute:   dashboardModel.toolchainLines,
	PresetsRoute:      dashboardModel.presetLines,
	ImportRoute:       dashboardModel.importLines,
	PackagesRoute:     dashboardModel.packageLines,
	TestsRoute:        dashboardModel.testLines,
	BuildRoute:        dashboardModel.buildLines,
	ReleaseRoute:      dashboardModel.releaseLines,
	DoctorRoute:       func(model dashboardModel, p palette, _ int) []string { return model.doctorLines(p) },
	SettingsRoute:     dashboardModel.settingsLines,
}

func (model dashboardModel) contentLines(p palette, width int) []string {
	if model.probing && model.probe.ProjectName == "" {
		return model.loadingLines(p)
	}
	if render, ok := contentRenderers[model.route]; ok {
		return render(model, p, width)
	}
	return nil
}

// loadingLines is the placeholder shown until the opening probe reports a
// project name.
func (model dashboardModel) loadingLines(p palette) []string {
	return []string{"", p.accent.Render(model.activityLabel()), p.muted.Render("Reading trestle.toml and probing local development tools…")}
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
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color(p.borderMuted)).Padding(0, 2).Render(p.accent.Render(value) + " " + p.muted.Render(label))
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
		row := fmt.Sprintf("%s %s %-18s  %-12s  %3d sources", model.targetAvailability(name), model.defaultTargetMark(name), name, target.Type, len(target.Sources))
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(row, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			lines = append(lines, model.targetDetailLines(p, width, name, target)...)
		}
	}
	return append(lines, "", p.faint.Render("enter build selected   f force selected   d make default   a build all"))
}

// targetAvailability marks a target row with a check, or a warning when the
// policy probe reported it as not ready.
func (model dashboardModel) targetAvailability(name string) string {
	if status, ok := model.probe.Statuses[name]; ok && !status.Ready {
		return "!"
	}
	return "✓"
}

// defaultTargetMark stars a target when it is one of the default build targets.
func (model dashboardModel) defaultTargetMark(name string) string {
	for _, item := range model.config.Build.DefaultTargets {
		if item == name {
			return "★"
		}
	}
	return " "
}

// targetDetailLines describes the selected target: its output name, dependency
// count, and every reason a policy check failed.
func (model dashboardModel) targetDetailLines(p palette, width int, name string, target config.Target) []string {
	output := target.OutputName
	if output == "" {
		output = name
	}
	lines := []string{p.faint.Render("     output " + output + " · " + fmt.Sprint(len(target.Dependencies)) + " dependencies")}
	for _, reason := range model.probe.Statuses[name].Reasons {
		lines = appendWrappedLog(lines, p.warning, "! "+reason, width)
	}
	return lines
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
			lines = append(lines, model.taskDetailLines(p, width, name, task)...)
		}
	}
	if model.pendingTask != "" {
		lines = append(lines, "", p.warning.Render("Run "+model.pendingTask+"?  y / enter confirm · n / esc cancel"))
	} else {
		lines = append(lines, "", p.faint.Render("enter preview · esc / ctrl+c cancel a running task"))
	}
	return lines
}

// taskDetailLines describes the selected task: its command, working directory,
// timeout, the values it sets, and the configuration changes it would commit.
func (model dashboardModel) taskDetailLines(p palette, width int, name string, task config.Task) []string {
	preview := model.pendingTaskPreview
	if model.pendingTask != name {
		preview = config.TaskPreview{}
	}
	var lines []string
	if len(task.Command) > 0 {
		lines = appendWrappedLog(lines, p.text, "command: "+fmt.Sprintf("%q", task.Command), width)
	}
	workingDir := fallback(preview.WorkingDir, fallback(task.WorkingDir, "project directory"))
	lines = appendWrappedLog(lines, p.muted, "working directory: "+workingDir, width)
	lines = appendWrappedLog(lines, p.muted, "timeout: "+fallback(task.Timeout, "none"), width)
	for _, key := range sortedMapKeys(task.Set) {
		lines = appendWrappedLog(lines, p.muted, "set "+key+" = "+task.Set[key], width)
	}
	if model.pendingTask == name {
		lines = append(lines, taskChangeLines(p, width, preview)...)
	}
	return lines
}

// taskChangeLines lists the configuration values a confirmed task would change.
func taskChangeLines(p palette, width int, preview config.TaskPreview) []string {
	lines := []string{"", p.faint.Render(" CONFIGURATION CHANGES AFTER SUCCESS")}
	if len(preview.Changes) == 0 {
		lines = append(lines, p.muted.Render("  no configuration value changes"))
	}
	for _, change := range preview.Changes {
		lines = appendWrappedLog(lines, p.text, change.Field+": "+fallback(change.Before, "(empty)")+" → "+fallback(change.After, "(empty)"), width)
	}
	return append(lines, p.faint.Render("  Files changed by the command itself are not rolled back."))
}

// sortedMapKeys returns the keys of a string map in sorted order.
func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	lines := model.toolchainHeaderLines(p)
	if len(model.probe.Components) == 0 {
		lines = append(lines, p.warning.Render("!  No native development components detected"), p.faint.Render("   WSL mode may still use a compiler installed inside the selected distribution."))
	}
	for index, component := range model.probe.Components {
		lines = append(lines, model.toolchainComponentLines(p, width, index, component)...)
	}
	return append(lines, "", p.faint.Render("enter connect native or WSL compiler/SDK   c CUDA   v Vulkan   x apply preset"))
}

// toolchainHeaderLines renders the active-toolchain summary block above the
// detected components.
func (model dashboardModel) toolchainHeaderLines(p palette) []string {
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
	return []string{p.faint.Render(" ACTIVE TOOLCHAIN"), keyValue(p, "Execution", execution), keyValue(p, "Compiler", fallback(model.config.Toolchain.CXX, "auto")), keyValue(p, "Preset", preset), keyValue(p, "Environment", fallback(model.config.Toolchain.Setup, "inherited")), keyValue(p, "CUDA", cudaState), keyValue(p, "Vulkan", vulkanState), "", p.faint.Render(" DETECTED · ENTER CONNECTS A COMPILER OR SDK")}
}

// toolchainComponentLines renders one detected component row, plus the path and
// WSL notice shown when it is selected.
func (model dashboardModel) toolchainComponentLines(p palette, width, index int, component toolchain.Component) []string {
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
	lines := []string{model.selectableRow(p, index, ansi.Truncate(row, width-2, "…"))}
	return append(lines, model.toolchainComponentDetails(p, width, index, component)...)
}

// toolchainComponentDetails is the path and WSL notice shown under the selected
// component row.
func (model dashboardModel) toolchainComponentDetails(p palette, width, index int, component toolchain.Component) []string {
	switch {
	case index == model.cursor && component.Path != "" && model.focus == focusContent:
		lines := []string{p.faint.Render("     " + ansi.Truncate(component.Path, max(10, width-6), "…"))}
		if component.Execution == "wsl" {
			lines = append(lines, p.warning.Render(ansi.Truncate("     Notice: selecting this compiler runs build commands inside WSL "+component.Distribution, max(10, width-3), "…")))
		}
		return lines
	case index == model.cursor && !component.Ready && component.Family == "WSL" && model.focus == focusContent:
		return []string{p.warning.Render("     " + ansi.Truncate(compactLine(component.Detail), max(10, width-6), "…"))}
	}
	return nil
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
	viewLines := model.testJobLines
	if model.testFileMode {
		mode = "FILE VIEW"
		viewLines = model.testFileLines
	}
	lines := []string{p.faint.Render(" " + mode)}
	lines = append(lines, viewLines(p, width)...)
	return append(lines, "", p.faint.Render("enter run selected   g run group   a run all   f job/file view   m assign group"))
}

// testFileLines renders one row per test source file in file view.
func (model dashboardModel) testFileLines(p palette, width int) []string {
	var lines []string
	for index, row := range model.testFiles() {
		text := fmt.Sprintf("%-34s  job %-18s  group %s", filepath.Base(row.file), row.job.Name, row.job.Group)
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(text, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			lines = append(lines, p.faint.Render("     "+ansi.Truncate(row.file, max(12, width-6), "…")))
		}
	}
	return lines
}

// testJobLines renders one row per test job in job view.
func (model dashboardModel) testJobLines(p palette, width int) []string {
	var lines []string
	for index, job := range model.probe.Tests {
		text := fmt.Sprintf("%-24s  group %-16s  %d files", job.Name, job.Group, len(job.Files))
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(text, width-3, "…")))
		if index == model.cursor && model.focus == focusContent && len(job.Files) > 0 {
			lines = append(lines, p.faint.Render("     "+ansi.Truncate(strings.Join(job.Files, ", "), max(12, width-6), "…")))
		}
	}
	return lines
}

func (model dashboardModel) packageLines(p palette, width int) []string {
	if model.packageLevel == packageCandidatesLevel {
		lines := model.packageCandidateLines(p, width)
		return append(lines, "", p.faint.Render(model.packageHint()))
	}
	names := sortedPackageNames(model.config.Packages)
	lines := model.packageSearchHeader(p)
	if len(names) == 0 {
		lines = append(lines, p.muted.Render("No packages configured."))
	} else {
		lines = append(lines, p.faint.Render(" CONFIGURED"))
		lines = append(lines, model.configuredPackageLines(p, width, names)...)
	}
	if len(model.packagePackages) > 0 {
		lines = append(lines, "", p.faint.Render(fmt.Sprintf(" MATCHES · %d PACKAGES · ENTER TO CHOOSE A VERSION", len(model.packagePackages))))
		lines = append(lines, model.searchedPackageLines(p, width, len(names))...)
	}
	return append(lines, "", p.faint.Render(model.packageHint()))
}

// packageSearchHeader is the online-index status line and the searching notice.
func (model dashboardModel) packageSearchHeader(p palette) []string {
	installer := "installer unavailable"
	if model.probe.Vcpkg {
		installer = "installer " + model.probe.VcpkgRoot
	}
	lines := []string{p.good.Render("● online index") + p.faint.Render("  vcpkg.io + conan.center · "+installer), ""}
	if model.searching {
		lines = append(lines, p.accent.Render("◌ Searching package indexes…")+p.faint.Render("  esc cancels, results appear in place"), "")
	}
	return lines
}

// configuredPackageLines renders the rows for the packages pinned in the config.
func (model dashboardModel) configuredPackageLines(p palette, width int, names []string) []string {
	var lines []string
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
		detail := pkg.Triplet
		if detail == "" {
			detail = fallback(providerOf(model.config.Packages, name), "vcpkg")
		}
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(fmt.Sprintf("%-22s %-18s %s", name+featureText, version, detail), width-3, "…")))
	}
	return lines
}

// searchedPackageLines renders the merged search results below the configured
// list, offsetting row indices past it.
func (model dashboardModel) searchedPackageLines(p palette, width, offset int) []string {
	var lines []string
	for index, pkg := range model.packagePackages {
		marker := "○"
		_, configured := model.config.Packages[pkg.Name]
		if pkg.Installed || configured {
			marker = "✓"
		}
		// A name offered by several providers shows them all, so the row
		// makes clear the choice happens on the next level.
		sources := providerSummary(pkg)
		lines = append(lines, model.selectableRow(p, offset+index, ansi.Truncate(fmt.Sprintf("%s  %-24s %s", marker, pkg.Name, sources), width-3, "…")))
		if offset+index == model.cursor && model.focus == focusContent {
			lines = append(lines, model.searchedPackageDetailLines(p, width, pkg)...)
		}
	}
	return lines
}

// searchedPackageDetailLines describes the selected search result: its
// description and the number of versions the version picker offers.
func (model dashboardModel) searchedPackageDetailLines(p palette, width int, pkg deps.Package) []string {
	var lines []string
	if pkg.Description != "" {
		lines = append(lines, p.text.Render("     "+ansi.Truncate(pkg.Description, max(12, width-7), "…")))
	}
	lines = append(lines, p.faint.Render("     "+fmt.Sprintf("%d version%s · enter to pick one · esc to return", len(pkg.Candidates), plural(len(pkg.Candidates)))))
	return lines
}

// providerSummary lists the providers offering a package, so one merged row can
// still show that both vcpkg and conan have it.
func providerSummary(pkg deps.Package) string {
	seen := map[string]bool{}
	var names []string
	for _, candidate := range pkg.Candidates {
		provider := fallback(candidate.Provider, "vcpkg")
		if seen[provider] {
			continue
		}
		seen[provider] = true
		names = append(names, provider)
	}
	if len(names) == 0 {
		return "no versions"
	}
	return strings.Join(names, " + ")
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (model dashboardModel) buildLines(p palette, width int) []string {
	lines := []string{"", "  " + model.buildStateLabel(p), "", p.faint.Render(" ACTIVE PROFILE"), keyValue(p, "Profile", fallback(model.config.Build.Profile, "debug")), keyValue(p, "Default targets", fallback(strings.Join(model.config.Build.DefaultTargets, ", "), "automatic")), keyValue(p, "Build directory", fallback(model.config.Build.BuildDir, "build/{profile}")), keyValue(p, "C++ standard", fallback(model.config.Build.CXXStandard, "compiler default")), keyValue(p, "Compiler", fallback(model.config.Toolchain.CXX, "auto-detect")), "", p.faint.Render(" TARGETS · ENTER BUILDS SELECTED")}
	lines = append(lines, model.buildTargetLines(p, width)...)
	lines = append(lines, "", p.faint.Render(" LAST RUN"), p.text.Render(model.lastRun))
	return append(lines, "", p.muted.Render("enter selected · f force selected · b defaults · a all · r refresh"))
}

// buildStateLabel renders the build screen's state chip: running, failed or
// ready.
func (model dashboardModel) buildStateLabel(p palette) string {
	switch {
	case model.mutating && model.operation == "Build":
		return p.accent.Render(model.activityLabel())
	case model.messageError && strings.HasPrefix(model.message, "Build failed"):
		return p.danger.Render("Failed")
	}
	return p.good.Render("Ready")
}

// buildTargetLines renders the target rows and any failed-check reasons for the
// selected one.
func (model dashboardModel) buildTargetLines(p palette, width int) []string {
	var lines []string
	for index, name := range sortedTargetNames(model.config.Targets) {
		lines = append(lines, model.selectableRow(p, index, ansi.Truncate(model.targetAvailability(name)+" "+name+"  "+model.config.Targets[name].Type, width-3, "…")))
		if index == model.cursor && model.focus == focusContent {
			for _, reason := range model.probe.Statuses[name].Reasons {
				lines = appendWrappedLog(lines, p.warning, "! "+reason, width)
			}
		}
	}
	return lines
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

// selectableRowHighlighted renders a row for a version the user explicitly
// requested, keeping the cursor treatment but marking it in the accent colour.
func (model dashboardModel) selectableRowHighlighted(p palette, index int, row string) string {
	if index == model.cursor && model.focus == focusContent {
		return p.selected.Render("› " + row + " ")
	}
	if index == model.cursor {
		return p.text.Render("  " + row)
	}
	return p.accent.Render("  " + row)
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
		keyValue(p, "/", "search the vcpkg.io and conan.center indexes"), keyValue(p, "enter / i", "run the selected screen action"),
		keyValue(p, "toolchains", "enter connect · e environment · c CUDA · v Vulkan"),
		keyValue(p, "tests", "enter job/file · g group · a all · f view"), keyValue(p, "packages", "enter choose version · esc go back · f features · a install · d remove"),
		keyValue(p, "targets", "enter build · f force build (skip availability check)"), keyValue(p, "tasks", "enter preview · y confirm · n cancel"),
		keyValue(p, "q", "leave Project Console"), "", p.faint.Render("Inputs: enter saves · esc cancels · ctrl+u clears · ctrl+w deletes a word"),
	}
	return lipgloss.NewStyle().Border(p.border).BorderForeground(lipgloss.Color(p.borderFocus)).Padding(1, 2).Width(max(20, width-6)).Height(max(3, height-4)).Render(strings.Join(lines, "\n"))
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

// inputLabels names every input prompt kind for the status line.
var inputLabels = map[string]string{
	"search":          "Search packages",
	"settings-search": "Search settings",
	"log-search":      "Search complete output",
	"package":         "Add package",
	"install":         "Install package",
	"features":        "Package features",
	"remove":          "Remove package",
	"version":         "Package version",
	"flags":           "Compiler flags",
	"c-flags":         "C flags",
	"cxx-flags":       "C++ flags",
	"link-flags":      "Link flags",
	"apply-preset":    "Apply preset",
	"save-preset":     "Save preset",
	"setting":         "Project setting",
	"release-output":  "ZIP output",
	"release-targets": "Release targets",
	"environment":     "Setup script",
	"cuda":            "CUDA root",
	"cuda-wsl":        "WSL CUDA root",
	"test-group":      "Test group",
}

func (model dashboardModel) inputLabel() string {
	if label, ok := inputLabels[model.inputKind]; ok {
		return label
	}
	return "Input"
}

// shortcutContexts is the route-specific key hint shown in the footer.
var shortcutContexts = map[Route]string{
	ToolchainsRoute: "↵ native or WSL compiler/SDK   c CUDA   v Vulkan   x apply preset",
	PresetsRoute:    "↵ apply   n save current   d delete",
	ImportRoute:     "↵ / i auto import CMake/Xmake   r refresh",
	PackagesRoute:   "/ web search   ↵ install   f features   a by name   n jump to results",
	TestsRoute:      "↵ selected   g group   a all   f files   m group",
	SettingsRoute:   "↵ category / edit   / search   Esc back   v rules",
	BuildRoute:      "↵ selected   f force   b default   a all",
	TasksRoute:      "↵ preview   y confirm   esc cancel running",
	ReleaseRoute:    "↵ optimization   r release ZIP   t targets   o output",
	TargetsRoute:    "↵ build selected   f force   d default   a all",
}

// shortcutContext is the key hint line shown in the footer for the active route,
// falling back to the global actions.
func (model dashboardModel) shortcutContext() string {
	if context, ok := shortcutContexts[model.route]; ok {
		return context
	}
	return "b build   r refresh   p profile"
}

func (model dashboardModel) shortcutLine(p palette, width int) string {
	left, right := p.faint.Render(" "+model.shortcutContext()), p.faint.Render("? help   wheel scroll   q quit ")
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

// providerOf names the package manager backing a configured package. An empty
// provider means vcpkg, the historical default.
func providerOf(values map[string]config.Package, name string) string {
	if pkg, ok := values[name]; ok && pkg.Provider != "" {
		return pkg.Provider
	}
	return "vcpkg"
}
