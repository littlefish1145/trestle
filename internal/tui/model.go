package tui

import (
	"fmt"
	"strings"

	"trestle/internal/toolchain"
)

// Tab identifies one page of the configuration editor.
type Tab int

// The editor pages, in tab order.
const (
	ProjectTab Tab = iota
	ToolchainTab
	BuildTab
	PackageTab
)

// State is the editable snapshot the editor renders: the project identity, the
// selected profile and compiler, the active tab, and any pending error.
type State struct {
	ProjectName string
	Profile     string
	Compiler    string
	Archiver    string
	Triplet     string
	Tab         Tab
	Editing     bool
	Done        bool
	Error       string
}

// Action is a key-level command the editor accepts from its host.
type Action int

// The input actions the editor understands.
const (
	NextAction Action = iota
	PreviousAction
	ToggleEditAction
	ConfirmAction
	InputAction
)

// Model is a self-contained terminal editor for the core project settings. It
// performs no I/O: the host feeds actions in and prints the rendered frame.
type Model struct {
	state State
}

// New returns an editor seeded with the project name, profile and compiler. An
// empty profile defaults to "debug", and the archiver is taken from the first
// detected toolchain when one is available.
func New(projectName, profile, compiler string, candidates []toolchain.Toolchain) Model {
	if profile == "" {
		profile = "debug"
	}
	return Model{state: State{ProjectName: projectName, Profile: profile, Compiler: compiler, Archiver: firstArchiver(candidates)}}
}

// State returns a copy of the editor's current values.
func (model Model) State() State { return model.state }

// Update applies an action to the editor. For InputAction, value replaces the
// field of the active tab; other actions ignore value.
func (model *Model) Update(action Action, value string) {
	switch action {
	case NextAction:
		model.state.Tab = Tab((int(model.state.Tab) + 1) % 4)
	case PreviousAction:
		model.state.Tab = Tab((int(model.state.Tab) + 3) % 4)
	case ToggleEditAction:
		model.state.Editing = !model.state.Editing
	case ConfirmAction:
		model.state.Editing = false
		model.state.Done = true
	case InputAction:
		model.state.Editing = false
	}
	if value != "" && action == InputAction {
		switch model.state.Tab {
		case ProjectTab:
			model.state.ProjectName = value
		case ToolchainTab:
			model.state.Compiler = value
		case BuildTab:
			model.state.Profile = value
		case PackageTab:
			model.state.Triplet = value
		}
	}
	model.state.Error = ""
}

// Render draws the editor as a multi-line plain-text frame, including the tab
// markers and an error line when one is set.
func (model Model) Render() string {
	var output strings.Builder
	fmt.Fprintln(&output, "Trestle configuration")
	fmt.Fprintln(&output, "====================")
	fmt.Fprintln(&output, model.tabLine(ProjectTab, "Project", model.state.ProjectName))
	fmt.Fprintln(&output, model.tabLine(ToolchainTab, "Toolchain", model.state.Compiler))
	fmt.Fprintln(&output, model.tabLine(BuildTab, "Build", model.state.Profile))
	fmt.Fprintln(&output, model.tabLine(PackageTab, "Packages", model.state.Triplet))
	if model.state.Editing {
		fmt.Fprintf(&output, "> edit %s: ", model.currentLabel())
	} else {
		fmt.Fprintln(&output, "Enter edit, Tab next, Shift+Tab previous, Ctrl+S confirm")
	}
	if model.state.Error != "" {
		fmt.Fprintf(&output, "error: %s\n", model.state.Error)
	}
	return output.String()
}

func (model Model) tabLine(tab Tab, label, value string) string {
	marker := " "
	if model.state.Tab == tab {
		marker = ">"
	}
	return fmt.Sprintf("%s %-10s %s", marker, label, value)
}

func (model Model) currentLabel() string {
	switch model.state.Tab {
	case ProjectTab:
		return "project name"
	case ToolchainTab:
		return "compiler"
	case BuildTab:
		return "profile"
	default:
		return "triplet"
	}
}

func firstArchiver(candidates []toolchain.Toolchain) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Archiver
}
