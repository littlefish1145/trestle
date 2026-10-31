package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"trestle/internal/deps"
	"trestle/internal/diag"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

// applyWorkflowBatch folds a burst of workflow log events into the console,
// stopping at the event that finishes the operation. Batching suppresses the
// per-event log follow-up so the view is refreshed once at the end.
func (model dashboardModel) applyWorkflowBatch(batch workflowBatch) (tea.Model, tea.Cmd) {
	model.batchingLogs = true
	var command tea.Cmd
	for _, event := range batch {
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
}

// applyInstallBatch is the install-event twin of applyWorkflowBatch: it applies
// a burst of installation lines and follows the package log once at the end.
func (model dashboardModel) applyInstallBatch(batch installBatch) (tea.Model, tea.Cmd) {
	model.batchingLogs = true
	var command tea.Cmd
	for _, event := range batch {
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
}

// applyWindowSize records the new terminal size and keeps a followed log pinned
// to its bottom.
func (model dashboardModel) applyWindowSize(size tea.WindowSizeMsg) dashboardModel {
	model.width, model.height = size.Width, size.Height
	model.keepCursorVisible()
	if model.logFollow && model.hasLog() {
		model.logScroll = model.maxLogScroll()
	}
	return model
}

// applyBackgroundColor picks the palette for the reported terminal background.
// A missing or unreadable answer falls back to dark, the look the console was
// designed around.
func (model dashboardModel) applyBackgroundColor(color tea.BackgroundColorMsg) dashboardModel {
	model.lightBackground = !color.IsDark()
	return model
}

// applyPulse advances the animation frame, scheduling another pulse while work
// is in flight.
func (model dashboardModel) applyPulse() (tea.Model, tea.Cmd) {
	model.pulse++
	if model.busy() {
		return model, pulseCommand()
	}
	return model, nil
}

// applyInventory merges a native toolchain inventory into the probe, preserving
// any WSL components already discovered and recomputing Vulkan readiness.
func (model dashboardModel) applyInventory(inventory inventoryMessage) dashboardModel {
	model.discovering = false
	for _, component := range model.probe.Components {
		if component.Execution == "wsl" || component.Family == "WSL" {
			inventory.components = append(inventory.components, component)
		}
	}
	model.probe.Components = inventory.components
	model.probe.Vulkan = false
	for _, component := range inventory.components {
		if component.Family == "Vulkan" && component.Ready {
			model.probe.Vulkan = true
		}
	}
	return model
}

// applyWSLInventory replaces the WSL components in the probe with a fresh
// inventory, leaving the native components untouched.
func (model dashboardModel) applyWSLInventory(inventory wslInventoryMessage) dashboardModel {
	model.wslDiscovering = false
	var components []toolchain.Component
	for _, component := range model.probe.Components {
		if component.Execution != "wsl" && component.Family != "WSL" {
			components = append(components, component)
		}
	}
	model.probe.Components = append(components, inventory.components...)
	return model
}

// applyAssessment stores policy statuses and rules for the project fingerprint
// they were computed for, ignoring results that arrive for a stale project.
func (model dashboardModel) applyAssessment(assessment assessmentMessage) dashboardModel {
	if assessment.fingerprint != model.projectFingerprint {
		return model
	}
	model.assessing = false
	model.probe.Statuses, model.probe.Rules = assessment.statuses, assessment.rules
	if assessment.cfg.SchemaVersion != 0 {
		model.config = assessment.cfg
	}
	if assessment.err != nil {
		model.lastError = assessment.err
		model.setMessage(assessment.err.Error(), true)
	}
	return model
}

// applyProbe installs a fresh project probe, carrying forward the component and
// rule state that the probe does not recompute, and starts an assessment when
// the project changed and the host offers the service.
func (model dashboardModel) applyProbe(probe probeMessage) (tea.Model, tea.Cmd) {
	model.probing = false
	changed := probe.Fingerprint != model.projectFingerprint
	probe.Components, probe.Toolchains, probe.Vulkan = model.probe.Components, model.probe.Toolchains, model.probe.Vulkan
	if !changed {
		probe.Statuses, probe.Rules = model.probe.Statuses, model.probe.Rules
		probe.Config = model.config
	}
	model.probe, model.config, model.baseConfig = probe, probe.Config, probe.BaseConfig
	model.projectFingerprint = probe.Fingerprint
	if model.member != "" && !containsString(model.workspaceMembers(), model.member) {
		model.member = ""
		model.rootCollapsed = false
		model.navCursor = 0
		for index, item := range model.workspaceNavItems() {
			if item.route == OverviewRoute && item.member == "" && !item.header {
				model.navCursor = index
				break
			}
		}
	}
	model.clampWorkspaceNavCursor()
	model.applyScope()
	if probe.Error != nil {
		model.lastError = probe.Error
		model.setMessage(probe.Error.Error(), true)
	} else if !model.mutating {
		model.setMessage("Project model is ready", false)
	}
	model.clampCursor()
	if changed && probe.Error == nil && model.services.AssessTargets != nil {
		model.assessing = true
		return model, model.assessmentCommand()
	}
	return model, nil
}

// applyPackageSearch presents the results of an index search, jumping into the
// version picker when the query pinned a specific version.
func (model dashboardModel) applyPackageSearch(message packageSearchMessage) dashboardModel {
	model.searching = false
	if message.err != nil {
		model.lastError = message.err
		model.setMessage(message.err.Error(), true)
		return model
	}
	if len(message.ports) == 0 {
		model.packageResults = nil
		model.setMessage("No matching packages found", false)
		return model
	}
	model.packageResults = message.ports
	// One row per package name, so a library offered by both vcpkg and conan is
	// chosen once instead of twice.
	model.packagePackages = deps.Merge(message.ports)
	model.packageLevel = packageListLevel
	model.cursor = len(model.scopedConfig().Packages)
	model.scroll = max(0, model.cursor-2)
	if model.packageRequested != "" {
		// A pinned version jumps straight into the picker with that release
		// highlighted, ready to install with enter or i.
		if model.enterPackageByName(message.ports) {
			model.setMessage(fmt.Sprintf("%s %s · enter installs this version, esc returns", queryName(message.ports), model.packageRequested), false)
			return model
		}
	}
	model.setMessage(fmt.Sprintf("Found %d package%s · enter chooses a version and source", len(model.packagePackages), plural(len(model.packagePackages))), false)
	return model
}

// applyMutation finishes a configuration mutation, re-probing the project on
// success and recording the failure otherwise.
func (model dashboardModel) applyMutation(mutation mutationMessage) (tea.Model, tea.Cmd) {
	model.mutating = false
	model.operation = ""
	if mutation.err != nil {
		mutation.err = runlog.Failure(model.path, mutation.label, mutation.err)
		model.lastError = mutation.err
		model.setMessage(mutation.err.Error(), true)
		return model, nil
	}
	model.setMessage(mutation.label, false)
	return model, model.probeCommand()
}

// applyInstallEvent advances an installation: streaming lines while it runs and
// settling the console state when it finishes.
func (model dashboardModel) applyInstallEvent(event installEvent) (tea.Model, tea.Cmd) {
	if !event.done {
		model.receiveLog(event.line, true)
		model.followVisibleLog(PackagesRoute)
		model.setMessage("Installing dependency · F2 complete output", false)
		return model, waitInstallEvent(model.installEvents)
	}
	if model.installCancel != nil {
		model.installCancel()
	}
	model.mutating, model.operation, model.installEvents, model.installCancel = false, "", nil, nil
	if event.err != nil {
		model.lastError = event.err
		model.receiveLog(diag.Text(event.err), true)
		model.followVisibleLog(PackagesRoute)
		model.setMessage("Install failed · "+event.err.Error(), true)
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
	model.setMessage("Installed and added "+event.name, false)
	return model, model.probeCommand()
}

// applyWorkflowEvent advances a long-running workflow: streaming lines while it
// runs and reporting the outcome when it finishes.
func (model dashboardModel) applyWorkflowEvent(event workflowEvent) (tea.Model, tea.Cmd) {
	if !event.done {
		model.receiveLog(event.line, false)
		model.followVisibleLog(model.workflowRoute)
		if diagnosticLine(event.line) {
			model.workflowErrors = append(model.workflowErrors, event.line)
			if len(model.workflowErrors) > 512 {
				model.workflowErrors = model.workflowErrors[len(model.workflowErrors)-512:]
			}
		}
		model.setMessage(event.label+" running · F2 complete output", false)
		return model, waitWorkflowEvent(model.workflowEvents)
	}
	if model.workflowCancel != nil {
		model.workflowCancel()
	}
	model.mutating, model.operation, model.workflowEvents, model.workflowCancel = false, "", nil, nil
	quitAfterTask := model.quitAfterTask
	model.quitAfterTask = false
	if event.err != nil {
		model.lastError = event.err
		model.receiveLog(diag.Text(event.err), false)
		model.followVisibleLog(model.workflowRoute)
		switch {
		case errors.Is(event.err, context.Canceled):
			model.setMessage(event.label+" cancelled", false)
		case errors.Is(event.err, context.DeadlineExceeded):
			model.setMessage(event.label+" timed out · "+event.err.Error(), true)
		default:
			model.setMessage(event.label+" failed · "+event.err.Error(), true)
		}
		if quitAfterTask {
			return model, tea.Quit
		}
		return model, nil
	}
	model.setMessage(event.label+" completed", false)
	if event.label == "Build" {
		model.lastRun = "Completed just now"
	}
	if quitAfterTask {
		return model, tea.Quit
	}
	return model, model.probeCommand()
}

// applyMouseWheel scrolls whichever pane the wheel is over: an overlay, the log
// pane, or the content pane.
func (model dashboardModel) applyMouseWheel(wheel tea.MouseWheelMsg) dashboardModel {
	mouse := wheel.Mouse()
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
		} else if model.showOutput() && (model.fullLog || model.mouseInOutput(mouse.X, mouse.Y) || model.focus == focusLog) {
			model.focus = focusLog
			model.scrollLog(delta)
		} else {
			model.focus = focusContent
			model.scrollContent(delta)
		}
	}
	return model
}

// applyMouseClick focuses the log pane when a click lands inside it, and the
// content pane otherwise.
func (model dashboardModel) applyMouseClick(click tea.MouseClickMsg) dashboardModel {
	if click.Mouse().Button == tea.MouseLeft {
		mouse := click.Mouse()
		if model.help || model.details || model.history || mouse.Y < 2 || mouse.Y >= 2+model.mainHeight() {
			return model
		}
		model.focus = focusContent
		if model.showOutput() && (model.fullLog || model.mouseInOutput(mouse.X, mouse.Y)) {
			model.focus = focusLog
		}
	}
	return model
}

func (model dashboardModel) mouseInOutput(x, y int) bool {
	if model.showSidebar() && x <= sidebarWidth(model.width) {
		return false
	}
	if model.height < 20 && model.focus != focusLog {
		return false
	}
	return y >= 2+model.mainHeight()-model.logHeight() && y < 2+model.mainHeight()
}
