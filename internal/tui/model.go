package tui

import (
	"fmt"
	"strings"

	"trestle/internal/toolchain"
)

type Tab int

const (
	ProjectTab Tab = iota
	ToolchainTab
	BuildTab
	PackageTab
)

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

type Action int

const (
	NextAction Action = iota
	PreviousAction
	ToggleEditAction
	ConfirmAction
	InputAction
)

type Model struct {
	state State
}

func New(projectName, profile, compiler string, candidates []toolchain.Toolchain) Model {
	if profile == "" {
		profile = "debug"
	}
	return Model{state: State{ProjectName: projectName, Profile: profile, Compiler: compiler, Archiver: firstArchiver(candidates)}}
}

func (model Model) State() State { return model.state }

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
