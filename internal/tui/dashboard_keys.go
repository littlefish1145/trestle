package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"trestle/internal/config"
)

// handlePendingTaskKey consumes keys while a task preview awaits confirmation.
// It always returns a command because the prompt owns the keyboard.
func (model dashboardModel) handlePendingTaskKey(key tea.Key) (tea.Model, tea.Cmd) {
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

// handleHelpKey dismisses the keyboard-reference overlay, swallowing every key
// while it is open.
func (model dashboardModel) handleHelpKey(key tea.Key) (tea.Model, tea.Cmd) {
	if key.String() == "?" || key.String() == "esc" || key.String() == "q" {
		model.help = false
	}
	return model, nil
}

// handleModalKeys dispatches the keys that quit the console, step back out of a
// screen, or open the help overlay.
func (model dashboardModel) handleModalKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "q", "ctrl+c":
		updated, cmd := model.quitKey()
		return updated, cmd, true
	case "esc", "backspace":
		updated, cmd := model.backKey()
		return updated, cmd, true
	case "?":
		model.saveOverlayPosition()
		model.details, model.history = false, false
		model.help = true
		model.overlayScroll = model.helpScroll
		return model, nil, true
	}
	return model, nil, false
}

// quitKey leaves the console, asking any running workflow or installation to
// cancel first and waiting for it before the process exits.
func (model dashboardModel) quitKey() (tea.Model, tea.Cmd) {
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
}

// backKey is the universal "return" key: it steps back out of the package
// drill-down before it does anything else, so the user always has a way back to
// the previous level.
func (model dashboardModel) backKey() (tea.Model, tea.Cmd) {
	if model.route == PackagesRoute && model.packageLevel == packageCandidatesLevel {
		model.leaveCandidates()
		model.setMessage("Back to packages", false)
		return model, nil
	}
	if model.inputMode {
		model.closeInput()
		model.setMessage("Cancelled", false)
		return model, nil
	}
	if model.help || model.details || model.history {
		model.help, model.details, model.history = false, false, false
		return model, nil
	}
	model.setMessage("Nothing to go back to", false)
	return model, nil
}

// handleFocusKeys dispatches the keys that move focus between the navigation,
// content and log panes.
func (model dashboardModel) handleFocusKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "ctrl+b":
		model.sidebarVisible = !model.sidebarVisible
		if model.sidebarVisible {
			model.focus = focusNavigation
		} else {
			model.focus = focusContent
		}
	case "tab", "shift+tab":
		panes := []focusArea{focusContent}
		if model.sidebarVisible {
			panes = append([]focusArea{focusNavigation}, panes...)
		}
		if model.showOutput() {
			panes = append(panes, focusLog)
		}
		index := 0
		for i, pane := range panes {
			if model.focus == pane {
				index = i
				break
			}
		}
		delta := 1
		if key.String() == "shift+tab" {
			delta = len(panes) - 1
		}
		model.focus = panes[(index+delta)%len(panes)]
	case "left", "h":
		model.sidebarVisible = true
		model.focus = focusNavigation
	case "right", "l":
		model.focus = focusContent
	default:
		return model, nil, false
	}
	model.keepCursorVisible()
	return model, nil, true
}

// handleMoveKeys dispatches cursor and viewport movement, including the
// page/home/end jumps that switch behaviour in the settings rules view.
func (model dashboardModel) handleMoveKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
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
	default:
		return model, nil, false
	}
	return model, nil, true
}

// handleActionKeys dispatches the primary console actions: the context-sensitive
// enter key, build or release, refresh, forced refresh and the profile toggle.
func (model dashboardModel) handleActionKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "enter":
		return model.handleEnterKey()
	case "b":
		if model.route == ReleaseRoute {
			updated, cmd := model.startRelease()
			return updated, cmd, true
		}
		updated, cmd := model.startBuild()
		return updated, cmd, true
	case "r":
		updated, cmd := model.refreshKey()
		return updated, cmd, true
	case "ctrl+r":
		updated, cmd := model.forceRefreshKey()
		return updated, cmd, true
	case "p":
		updated, cmd := model.profileKey()
		return updated, cmd, true
	case "w":
		if !model.isWorkspace() {
			return model, nil, false
		}
		model.openInput("switch-member", "Switch member (empty = all; members: "+strings.Join(model.workspaceMembers(), ", ")+")")
		return model, nil, true
	case "z":
		if !model.isWorkspace() {
			return model, nil, false
		}
		model.toggleWorkspaceCollapse()
		return model, nil, true
	}
	return model, nil, false
}

// refreshKey re-probes the project and probes, starting a release on the release
// screen instead.
func (model dashboardModel) refreshKey() (tea.Model, tea.Cmd) {
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
}

// forceRefreshKey re-probes the project without showing the refresh message or
// starting a release.
func (model dashboardModel) forceRefreshKey() (tea.Model, tea.Cmd) {
	model.probing = true
	if !model.discovering && !model.wslDiscovering {
		model.discovering = true
		model.wslDiscovering = true
		return model, tea.Batch(model.probeCommand(), model.inventoryCommand(), model.wslInventoryCommand(), pulseCommand())
	}
	return model, model.probeCommand()
}

// profileKey toggles the active build profile between debug and release.
func (model dashboardModel) profileKey() (tea.Model, tea.Cmd) {
	profile := "release"
	if model.config.Build.Profile == "release" {
		profile = "debug"
	}
	return model.startMutation("Switching profile", "Profile changed to "+profile, func() error {
		return callOne(model.services.SetProfile, profile, "profile service")
	})
}

// handleEnterKey activates the current selection: on the navigation pane it
// moves focus into the content, otherwise it runs the action the active route
// offers for the selected row.
func (model dashboardModel) handleEnterKey() (tea.Model, tea.Cmd, bool) {
	if model.focus == focusNavigation {
		if model.isWorkspace() {
			model.clampWorkspaceNavCursor()
			if model.toggleSelectedWorkspaceSection() {
				return model, nil, true
			}
			model.openWorkspaceNavLeaf(model.navCursor)
		}
		model.focus = focusContent
		model.keepCursorVisible()
		return model, nil, true
	}
	switch model.route {
	case PackagesRoute:
		return model.enterPackages()
	case ToolchainsRoute:
		return model.enterToolchains()
	case ImportRoute:
		updated, cmd := model.startProjectImport()
		return updated, cmd, true
	case TasksRoute:
		return model.enterTasks()
	case TargetsRoute, BuildRoute:
		updated, cmd := model.startSelectedTargetBuild()
		return updated, cmd, true
	case TestsRoute:
		updated, cmd := model.runSelectedTest()
		return updated, cmd, true
	case SettingsRoute:
		model.openSelectedSetting()
		return model, nil, true
	case PresetsRoute:
		return model.enterPreset()
	case ReleaseRoute:
		if model.isWorkspace() && model.member == "" {
			updated, cmd := model.startRelease()
			return updated, cmd, true
		}
		updated, cmd := model.selectReleaseOptimization()
		return updated, cmd, true
	case DoctorRoute:
		return model.enterDoctor()
	}
	return model, nil, true
}

// enterPackages handles enter on the packages screen: choosing a candidate
// version installs it, while on the list level it drills into the versions.
func (model dashboardModel) enterPackages() (tea.Model, tea.Cmd, bool) {
	if model.packageLevel == packageCandidatesLevel {
		pkg, candidate, ok := model.selectedCandidate()
		if !ok {
			return model, nil, true
		}
		updated, cmd := model.installPackage(referenceFor(pkg, candidate))
		return updated, cmd, true
	}
	if model.enterCandidates() {
		return model, nil, true
	}
	return model, nil, true
}

// enterToolchains connects the selected compiler or SDK. A ready CUDA or Vulkan
// component connects to its service; a selectable compiler becomes the active
// one, inside WSL or natively.
func (model dashboardModel) enterToolchains() (tea.Model, tea.Cmd, bool) {
	component, ok := model.selectedComponent()
	if !ok {
		return model, nil, true
	}
	if component.Family == "CUDA" && component.Ready {
		updated, cmd := model.connectCUDA(component)
		return updated, cmd, true
	}
	if component.Family == "Vulkan" && component.Ready {
		updated, cmd := model.connectVulkan(component)
		return updated, cmd, true
	}
	if _, compiler := model.selectedCompiler(); !compiler {
		return model, nil, true
	}
	if component.Execution == "wsl" {
		updated, cmd := model.startMutation("Selecting WSL compiler", "WSL compiler selected: "+component.Name+" · builds run inside "+component.Distribution, func() error {
			if model.services.SetWSLCompiler == nil {
				return fmt.Errorf("WSL compiler service is unavailable")
			}
			return model.services.SetWSLCompiler(component.Distribution, component.Path)
		})
		return updated, cmd, true
	}
	updated, cmd := model.startMutation("Selecting compiler", "Compiler selected: "+component.Name, func() error {
		return callOne(model.services.SetCompiler, component.Path, "toolchain service")
	})
	return updated, cmd, true
}

// enterTasks previews the selected task and arms the y/n confirmation prompt.
func (model dashboardModel) enterTasks() (tea.Model, tea.Cmd, bool) {
	name, ok := model.selectedTask()
	if !ok {
		return model, nil, true
	}
	if model.services.PreviewTask == nil {
		model.setMessage("Task preview service is unavailable", true)
		return model, nil, true
	}
	preview, err := model.services.PreviewTask(context.Background(), name)
	if err != nil {
		model.setMessage(err.Error(), true)
		return model, nil, true
	}
	model.pendingTask = name
	model.pendingTaskPreview = preview
	model.setMessage("Review task and press y to run, n to cancel", false)
	return model, nil, true
}

// enterPreset applies the selected compiler preset.
func (model dashboardModel) enterPreset() (tea.Model, tea.Cmd, bool) {
	name, ok := model.selectedPreset()
	if !ok {
		return model, nil, true
	}
	updated, cmd := model.startMutation("Applying compiler preset", "Applied preset "+name, func() error {
		return callOne(model.services.ApplyPreset, name, "preset service")
	})
	return updated, cmd, true
}

// enterDoctor runs the doctor checks when the host provides the service.
func (model dashboardModel) enterDoctor() (tea.Model, tea.Cmd, bool) {
	if model.isWorkspace() && model.services.DoctorWorkspace != nil {
		member := model.member
		updated, cmd := model.startWorkflow("Doctor", func(ctx context.Context, progress func(string)) error {
			return model.services.DoctorWorkspace(ctx, member, progress)
		})
		return updated, cmd, true
	}
	if model.services.Doctor == nil {
		return model, nil, true
	}
	updated, cmd := model.startWorkflow("Doctor", model.services.Doctor)
	return updated, cmd, true
}

// handleListKeys dispatches the per-screen list actions: install or search
// packages, add or remove dependencies, and save presets.
func (model dashboardModel) handleListKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if (model.route == TargetsRoute || model.route == BuildRoute) && key.String() == "/" {
		model.openInput("target-search", "Filter targets by name (member:target; empty clears)")
		model.inputText = model.targetQuery
		return model, nil, true
	}
	switch key.String() {
	case "a":
		return model.addKey()
	case "n":
		return model.newPresetKey()
	case "i":
		return model.installKey()
	case "f":
		return model.featuresKey()
	case "/", "s":
		model.route, model.focus = PackagesRoute, focusContent
		model.openInput("search", "Search vcpkg and conan (name:version pins a release, e.g. fmt:11.2.0)")
		return model, nil, true
	case "d":
		return model.deleteKey()
	}
	return model, nil, false
}

// addKey runs the context-sensitive a action: run all tests or build all
// targets, apply the selected preset, or open the install-by-name prompt.
func (model dashboardModel) addKey() (tea.Model, tea.Cmd, bool) {
	switch {
	case model.route == TestsRoute:
		updated, cmd := model.startTests(TestSelection{All: true}, "All tests")
		return updated, cmd, true
	case model.route == TargetsRoute || model.route == BuildRoute:
		updated, cmd := model.startTargetBuild(nil, true, "Build all targets")
		return updated, cmd, true
	case model.route == PresetsRoute:
		if name, ok := model.selectedPreset(); ok {
			updated, cmd := model.startMutation("Applying compiler preset", "Applied preset "+name, func() error {
				return callOne(model.services.ApplyPreset, name, "preset service")
			})
			return updated, cmd, true
		}
		model.setMessage("No compiler preset is selected", true)
		return model, nil, true
	}
	model.route, model.focus = PackagesRoute, focusContent
	model.openInput("install", "Install package by name (vcpkg port or conan name/version)")
	return model, nil, true
}

// newPresetKey jumps the cursor to the first search result, or opens the
// save-preset prompt on the toolchain and preset screens.
func (model dashboardModel) newPresetKey() (tea.Model, tea.Cmd, bool) {
	if model.route == PackagesRoute && model.packageLevel == packageListLevel && len(model.packagePackages) > 0 {
		model.cursor = len(model.scopedConfig().Packages)
		model.scroll = max(0, model.cursor-2)
		model.setMessage("Cursor jumped to first search result", false)
	} else if model.route == ToolchainsRoute || model.route == PresetsRoute {
		model.openInput("save-preset", "Save current compiler setup as preset")
	}
	return model, nil, true
}

// installKey opens the project import or installs the selected package: a
// candidate version installs directly, while on the list level it installs the
// preferred version without opening the picker.
func (model dashboardModel) installKey() (tea.Model, tea.Cmd, bool) {
	switch {
	case model.route == ImportRoute:
		updated, cmd := model.startProjectImport()
		return updated, cmd, true
	case model.route == PackagesRoute:
		if model.packageLevel == packageCandidatesLevel {
			pkg, candidate, ok := model.selectedCandidate()
			if !ok {
				return model, nil, true
			}
			updated, cmd := model.installPackage(referenceFor(pkg, candidate))
			return updated, cmd, true
		}
		// On the package list "i" is the quick action: install the newest
		// version right away, without stepping into the version picker.
		pkg, ok := model.selectedPackage()
		if !ok {
			return model, nil, true
		}
		candidate, ok := pkg.Preferred(model.packageRequested)
		if !ok {
			model.setMessage("No installable version is available for "+pkg.Name, true)
			return model, nil, true
		}
		notice := "Installing " + pkg.Name + " " + fallback(candidate.Version, "latest")
		model.setMessage(notice+" from "+candidate.Provider+"…", false)
		updated, cmd := model.installPackage(referenceFor(pkg, candidate))
		return updated, cmd, true
	}
	return model, nil, true
}

// featuresKey force-builds the selected target, toggles the tests job/file view,
// or opens the feature prompt for the selected package.
func (model dashboardModel) featuresKey() (tea.Model, tea.Cmd, bool) {
	if model.route == TargetsRoute || model.route == BuildRoute {
		if row, ok := model.selectedTargetRow(); ok {
			updated, cmd := model.startForcedTargetBuild(row)
			return updated, cmd, true
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
		if model.packageLevel != packageListLevel {
			return model, nil, true
		}
		if pkg, ok := model.selectedPackage(); ok {
			features := candidateFeatures(model, pkg)
			if len(features) > 0 {
				model.openInput("features", "Features for "+pkg.Name+" (comma separated)")
				model.inputPackage = pkg.Name
			}
		}
	}
	return model, nil, true
}

// deleteKey removes the selected dependency or preset, or makes the selected
// target the default.
func (model dashboardModel) deleteKey() (tea.Model, tea.Cmd, bool) {
	switch {
	case model.route == PackagesRoute:
		names := sortedPackageNames(model.config.Packages)
		if model.focus == focusContent && model.cursor >= 0 && model.cursor < len(names) {
			name := names[model.cursor]
			updated, cmd := model.startMutation("Removing dependency "+name, "Removed "+name, func() error {
				return callOne(model.services.RemovePackage, name, "package service")
			})
			return updated, cmd, true
		}
		model.openInput("remove", "Remove configured package")
	case model.route == TargetsRoute:
		if row, ok := model.selectedTargetRow(); ok {
			if row.member != "" && row.member != model.member {
				// An aggregate row from another member: make that member the
				// scope so the default target is written to its file.
				model.member = row.member
				model.applyScope()
			}
			updated, cmd := model.startMutation("Setting default target", "Default target: "+row.name, func() error {
				if model.services.SetDefaultTargets == nil {
					return fmt.Errorf("default target service is unavailable")
				}
				return model.services.SetDefaultTargets([]string{row.name})
			})
			return updated, cmd, true
		}
	case model.route == PresetsRoute:
		if name, ok := model.selectedPreset(); ok {
			updated, cmd := model.startMutation("Deleting compiler preset", "Deleted preset "+name, func() error {
				return callOne(model.services.DeletePreset, name, "preset service")
			})
			return updated, cmd, true
		}
	}
	return model, nil, true
}

// handleToolchainKeys dispatches the toolchain and settings editors: Vulkan,
// CUDA, environment, compiler flags and preset application.
func (model dashboardModel) handleToolchainKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "v":
		return model.vulkanKey()
	case "e":
		if model.route == ToolchainsRoute {
			model.openInput("environment", "Environment setup script (type inherit to clear)")
		} else if model.route == SettingsRoute {
			model.openSelectedSetting()
		} else {
			model.route, model.focus = SettingsRoute, focusContent
			model.openInput("flags", "Set compiler flags")
		}
		return model, nil, true
	case "x":
		if model.route == ToolchainsRoute {
			model.openInput("apply-preset", "Apply compiler preset")
		} else if model.route == SettingsRoute {
			model.openInput("cxx-flags", "Set C++-only compiler flags")
		}
		return model, nil, true
	case "o":
		if model.route == ReleaseRoute {
			model.openInput("release-output", "Release ZIP output path")
			model.inputText = model.config.Package.Output
		} else if model.route == SettingsRoute {
			model.openInput("link-flags", "Set linker flags")
		}
		return model, nil, true
	case "c":
		return model.cudaKey()
	case "u":
		if model.route == ToolchainsRoute {
			kind := "cuda"
			if model.config.Toolchain.Mode == "wsl" {
				kind = "cuda-wsl"
			}
			model.openInput(kind, "CUDA toolkit root")
		}
		return model, nil, true
	}
	return model, nil, false
}

// vulkanKey connects or disconnects the Vulkan SDK, toggles the settings rules
// view, or opens the package version prompt.
func (model dashboardModel) vulkanKey() (tea.Model, tea.Cmd, bool) {
	switch {
	case model.route == ToolchainsRoute:
		enabled := model.config.Toolchain.Vulkan == ""
		if enabled {
			if component, ok := model.detectedSDK("Vulkan"); ok {
				updated, cmd := model.connectVulkan(component)
				return updated, cmd, true
			}
			model.setMessage("No Vulkan SDK was detected", true)
			return model, nil, true
		}
		updated, cmd := model.startMutation("Disconnecting Vulkan SDK", "Vulkan SDK disconnected", func() error {
			if model.services.SetVulkan == nil {
				return fmt.Errorf("Vulkan service is unavailable")
			}
			return model.services.SetVulkan(false, "", "native", "")
		})
		return updated, cmd, true
	case model.route == SettingsRoute:
		model.rulesView = !model.rulesView
		model.cursor, model.scroll, model.logFollow = 0, 0, false
	case model.route == PackagesRoute:
		model.openInput("version", "Set package version (name@version)")
		names := sortedPackageNames(model.config.Packages)
		if model.cursor >= 0 && model.cursor < len(names) {
			model.inputText = names[model.cursor] + "@"
		}
	}
	return model, nil, true
}

// cudaKey connects, configures or disables CUDA depending on the route and the
// current toolchain state.
func (model dashboardModel) cudaKey() (tea.Model, tea.Cmd, bool) {
	if model.route == ToolchainsRoute {
		enabled := model.config.Toolchain.CUDA == ""
		component, detected := model.detectedSDKForExecution("CUDA", model.config.Toolchain.Mode)
		if enabled && !detected {
			kind, prompt := "cuda", "CUDA toolkit root (no native toolkit was auto-detected)"
			if model.config.Toolchain.Mode == "wsl" {
				kind, prompt = "cuda-wsl", "CUDA toolkit root inside "+fallback(model.config.Toolchain.WSLDistribution, "the default WSL distribution")
			}
			model.openInput(kind, prompt)
			return model, nil, true
		}
		if enabled {
			updated, cmd := model.connectCUDA(component)
			return updated, cmd, true
		}
		label := "CUDA disabled"
		updated, cmd := model.startMutation("Updating CUDA", label, func() error {
			if model.services.SetCUDAConnection != nil {
				return model.services.SetCUDAConnection(false, "", "native", "")
			}
			if model.services.SetCUDA == nil {
				return fmt.Errorf("CUDA service is unavailable")
			}
			return model.services.SetCUDA(false, "")
		})
		return updated, cmd, true
	} else if model.route == SettingsRoute {
		model.openInput("c-flags", "Set C-only compiler flags")
	}
	return model, nil, true
}

// handleSurfaceKeys dispatches the remaining screen actions: run a test group,
// reassign a test to a group, and edit the release targets.
func (model dashboardModel) handleSurfaceKeys(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "g":
		if model.route == TestsRoute {
			if job, ok := model.selectedTestJob(); ok {
				updated, cmd := model.startTests(TestSelection{Group: job.Group}, "Test group "+job.Group)
				return updated, cmd, true
			}
		} else {
			model.cursor, model.scroll = 0, 0
		}
		return model, nil, true
	case "m":
		if model.route == TestsRoute {
			if job, ok := model.selectedTestJob(); ok {
				model.openInput("test-group", "Move "+job.Name+" to test group")
				model.inputPackage = job.Name
				model.inputText = job.Group
			}
		}
		return model, nil, true
	case "t":
		if model.route == ReleaseRoute {
			model.openInput("release-targets", "Release targets (comma separated)")
			model.inputText = strings.Join(model.config.Package.Targets, ",")
		}
		return model, nil, true
	}
	return model, nil, false
}

// selectRouteByNumber maps a digit key to a screen: 1-9 to the first nine routes
// and 0 to the tenth. In a workspace the digits address the tree rows instead.
// Any other key is ignored.
func (model *dashboardModel) selectRouteByNumber(key tea.Key) {
	index := -1
	if key.String() == "0" {
		index = 9
	} else if len(key.String()) == 1 && key.String()[0] >= '1' && key.String()[0] <= '9' {
		index = int(key.String()[0] - '1')
	}
	if index < 0 {
		return
	}
	if model.isWorkspace() {
		model.jumpWorkspaceSectionRow(index + 1)
		return
	}
	if index < len(routes) {
		model.selectRoute(index)
	}
}
