package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"trestle/internal/deps"
)

// submitInput commits the active text prompt. Most kinds run a configuration
// mutation; the search, install, features and version kinds have bespoke
// follow-ups.
func (model dashboardModel) submitInput() (tea.Model, tea.Cmd) {
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
	if kind == "switch-member" {
		return model.applyMemberSwitch(text)
	}
	if model.memberScopeLocked() && (kind == "install" || kind == "features" || kind == "version" || kind == "package") {
		model.setMessage(model.memberScopeMessage(), true)
		return model, nil
	}
	if text == "" && kind != "environment" && kind != "setting" {
		model.setMessage("Nothing changed", false)
		return model, nil
	}
	if kind == "search" {
		return model.submitPackageSearch(text)
	}
	if kind == "install" {
		return model.installPackage(text)
	}
	if kind == "features" {
		features := strings.Join(strings.FieldsFunc(text, func(value rune) bool { return value == ',' || value == ' ' }), ",")
		spec := packageName + "[" + features + "]"
		return model.installPackage(spec)
	}
	if kind == "version" {
		return model.submitPackageVersion(text)
	}
	action, label := model.inputAction(kind, text, packageName)
	return model.startMutation("Saving changes", label, action)
}

// applyMemberSwitch selects the workspace member subsequent operations target.
// An empty value returns to building every member. The selection is session
// state, so no configuration mutation runs.
func (model dashboardModel) applyMemberSwitch(text string) (tea.Model, tea.Cmd) {
	if text != "" {
		found := false
		for _, member := range model.workspaceMembers() {
			if member == text {
				found = true
				break
			}
		}
		if !found {
			model.setMessage("Unknown member "+text+"; members: "+strings.Join(model.workspaceMembers(), ", "), true)
			return model, nil
		}
	}
	model.member = text
	if text == "" {
		model.setMessage("Workspace: operating on all members", false)
		return model, nil
	}
	model.setMessage("Member: "+text, false)
	return model, nil
}

// submitPackageSearch launches an asynchronous index search for the entered
// term, remembering a pinned version so the results page can highlight it.
func (model dashboardModel) submitPackageSearch(text string) (tea.Model, tea.Cmd) {
	if model.services.SearchPackages == nil {
		model.setMessage("package search is unavailable", true)
		return model, nil
	}
	model.searching = true
	model.packageResults = nil
	// A "name:version" term remembers the version so the page can highlight it
	// and the quick install action can target it.
	model.packageRequested = deps.ParseQuery(text).Version
	model.cursor, model.scroll = max(0, len(model.config.Packages)-1), 0
	model.setMessage("Searching vcpkg and conan indexes for “"+text+"”…", false)
	return model, tea.Batch(func() tea.Msg {
		ports, err := model.services.SearchPackages(context.Background(), text)
		return packageSearchMessage{ports: ports, err: err}
	}, pulseCommand())
}

// submitPackageVersion applies a name@version pin, rejecting input that does not
// name both parts.
func (model dashboardModel) submitPackageVersion(text string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(text, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		model.setMessage("Use name@version, for example fmt@11.0.2", true)
		return model, nil
	}
	action := func() error {
		if model.services.SetPackageVersion == nil {
			return fmt.Errorf("package service is unavailable")
		}
		return model.services.SetPackageVersion(parts[0], parts[1])
	}
	label := "Updated " + parts[0] + " to " + parts[1]
	return model.startMutation("Saving changes", label, action)
}

// inputAction maps an accepted input kind to the configuration mutation it
// performs and the label shown on success. A nil callback is the no-op the
// default "Saved" label pairs with.
func (model dashboardModel) inputAction(kind, text, packageName string) (func() error, string) {
	switch kind {
	case "package":
		return func() error { return callOne(model.services.AddPackage, text, "package service") }, "Added " + text
	case "flags":
		return func() error { return callOne(model.services.SetCompileFlags, text, "settings service") }, "Compiler flags updated"
	case "c-flags", "cxx-flags", "link-flags":
		language := strings.TrimSuffix(kind, "-flags")
		return func() error {
			if model.services.SetLanguageFlags == nil {
				return fmt.Errorf("language flag service is unavailable")
			}
			return model.services.SetLanguageFlags(language, text)
		}, strings.ToUpper(language) + " flags updated"
	case "apply-preset":
		return func() error { return callOne(model.services.ApplyPreset, text, "preset service") }, "Applied preset " + text
	case "save-preset":
		return func() error { return callOne(model.services.SavePreset, text, "preset service") }, "Saved preset " + text
	case "setting":
		return func() error {
			if model.services.SetProjectSetting == nil {
				return fmt.Errorf("settings service is unavailable")
			}
			return model.services.SetProjectSetting(packageName, text)
		}, "Updated " + packageName
	case "release-output":
		return func() error {
			if model.services.ConfigureRelease == nil {
				return fmt.Errorf("release service is unavailable")
			}
			return model.services.ConfigureRelease("", nil, text)
		}, "Release output updated"
	case "release-targets":
		targets := splitList(text)
		return func() error {
			if model.services.ConfigureRelease == nil {
				return fmt.Errorf("release service is unavailable")
			}
			return model.services.ConfigureRelease("", targets, "")
		}, "Release targets updated"
	case "environment":
		return func() error { return callOne(model.services.SetEnvironment, text, "environment service") }, "Build environment updated"
	case "cuda":
		return func() error {
			if model.services.SetCUDA == nil {
				return fmt.Errorf("CUDA service is unavailable")
			}
			return model.services.SetCUDA(true, text)
		}, "CUDA enabled"
	case "cuda-wsl":
		return func() error {
			if model.services.SetCUDAConnection == nil {
				return fmt.Errorf("WSL CUDA service is unavailable")
			}
			return model.services.SetCUDAConnection(true, text, "wsl", model.config.Toolchain.WSLDistribution)
		}, "WSL CUDA enabled"
	case "test-group":
		return func() error {
			if model.services.SetTestGroup == nil {
				return fmt.Errorf("test suite service is unavailable")
			}
			return model.services.SetTestGroup(packageName, text)
		}, "Moved " + packageName + " to group " + text
	case "remove":
		return func() error { return callOne(model.services.RemovePackage, text, "package service") }, "Removed " + text
	}
	return nil, "Saved"
}
