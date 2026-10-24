// Package vscode generates and maintains the VS Code editor configuration for a
// Trestle project: a .vscode/tasks.json document with build, test, release, and
// run tasks. The generated document is cross-platform; run tasks carry a
// Windows override for the .exe suffix instead of being rewritten per host, and
// merges never touch tasks the user wrote by hand.
package vscode

import (
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/config"
)

// Version is the tasks.json format revision written by this package.
const Version = "2.0.0"

// BuildTaskLabel names the task every generated run task depends on.
const BuildTaskLabel = "Trestle: Build"

// taskLabelPrefix marks the tasks trestle owns. A merge replaces tasks carrying
// this prefix and leaves every other entry untouched.
const taskLabelPrefix = "Trestle: "

// targetInputID is the pickString input behind "Trestle: Build Current Target".
const targetInputID = "trestleTarget"

// Document is one .vscode/tasks.json file.
type Document struct {
	Version string  `json:"version"`
	Tasks   []Task  `json:"tasks"`
	Inputs  []Input `json:"inputs,omitempty"`
}

// Task is the subset of a VS Code task definition this package emits. Fields an
// existing user task may carry beyond these survive a merge untouched.
type Task struct {
	Label          string        `json:"label"`
	Type           string        `json:"type"`
	Command        string        `json:"command"`
	Args           []string      `json:"args,omitempty"`
	DependsOn      string        `json:"dependsOn,omitempty"`
	Group          *Group        `json:"group,omitempty"`
	ProblemMatcher []string      `json:"problemMatcher"`
	Presentation   *Presentation `json:"presentation,omitempty"`
	Options        *TaskOptions  `json:"options,omitempty"`
	Windows        *TaskOverride `json:"windows,omitempty"`
}

// Group maps a task to a VS Code task group such as build or test.
type Group struct {
	Kind      string `json:"kind"`
	IsDefault bool   `json:"isDefault"`
}

// Presentation controls how a task's terminal is shown.
type Presentation struct {
	Reveal string `json:"reveal"`
	Panel  string `json:"panel"`
	Focus  bool   `json:"focus,omitempty"`
}

// TaskOptions carries the task's working directory.
type TaskOptions struct {
	Cwd string `json:"cwd"`
}

// TaskOverride replaces selected task properties on one platform.
type TaskOverride struct {
	Command string `json:"command,omitempty"`
}

// Input is one VS Code input prompt.
type Input struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Options     []string `json:"options"`
	Default     string   `json:"default"`
}

// Target is one buildable target reduced to the facts tasks.json needs.
type Target struct {
	// Name is the configuration key shown in the target picker. Workspace
	// targets are qualified as "member:target".
	Name string
	// OutputName is the artifact name without directory or extension.
	OutputName string
	// Executable targets get a run task.
	Executable bool
	// BinDir overrides Project.BinDir, so workspace members can run their
	// artifacts from their own build directory.
	BinDir string
}

// Project carries the project facts the generator works from.
type Project struct {
	// BinDir is the artifact directory relative to the workspace root, with
	// forward slashes, such as "build/debug/bin". An empty value disables the
	// run tasks because the artifacts cannot be addressed portably.
	BinDir string
	// Targets lists every configured target.
	Targets []Target
	// DefaultTarget is the pickString value selected first.
	DefaultTarget string
	// Native reports whether the toolchain runs natively. WSL builds produce
	// Linux binaries, so their run tasks get no Windows override.
	Native bool
	// Members lists the workspace members in declaration order. Members get
	// their own build task, and run tasks depend on it. Standalone projects
	// leave it empty and keep the original task set.
	Members []string
}

// FromConfig reduces a loaded configuration to the generator's view of the
// project. root is the directory holding trestle.toml.
func FromConfig(root string, cfg config.Config) Project {
	profile := cfg.Build.Profile
	if profile == "" {
		profile = "debug"
	}
	buildDir := cfg.Build.BuildDir
	if buildDir == "" {
		buildDir = "build"
	}
	project := Project{Native: !strings.EqualFold(cfg.Toolchain.Mode, "wsl")}
	if absoluteRoot, err := filepath.Abs(root); err == nil {
		absoluteBuild := buildDir
		if !filepath.IsAbs(absoluteBuild) {
			absoluteBuild = filepath.Join(absoluteRoot, absoluteBuild)
		}
		// A build directory on another volume cannot be expressed with
		// ${workspaceFolder}, so BinDir stays empty and run tasks are skipped.
		if relative, err := filepath.Rel(absoluteRoot, absoluteBuild); err == nil {
			project.BinDir = filepath.ToSlash(filepath.Join(relative, profile, "bin"))
		}
	}
	for name, target := range cfg.Targets {
		output := target.OutputName
		if output == "" {
			output = name
		}
		project.Targets = append(project.Targets, Target{
			Name:       name,
			OutputName: strings.ReplaceAll(output, "\\", "/"),
			Executable: target.Type == "executable",
		})
	}
	sort.Slice(project.Targets, func(i, j int) bool { return project.Targets[i].Name < project.Targets[j].Name })
	project.DefaultTarget = defaultTarget(cfg.Build.DefaultTargets, project.Targets)
	return project
}

// FromWorkspace reduces a workspace to the generator's view: every member's
// targets are qualified as "member:target" so the target picker and build
// arguments address the right member, and each target carries its member's bin
// directory for run tasks.
func FromWorkspace(root string, wc *config.WorkspaceContext) Project {
	project := Project{Native: !strings.EqualFold(wc.RootConfig.Toolchain.Mode, "wsl")}
	absoluteRoot, rootErr := filepath.Abs(root)
	for _, name := range wc.MemberOrder {
		member := wc.Member(name)
		if member == nil {
			continue
		}
		project.Members = append(project.Members, name)
		profile := member.Build.Profile
		if profile == "" {
			profile = "debug"
		}
		buildDir := member.Build.BuildDir
		if buildDir == "" {
			buildDir = "build"
		}
		memberBin := filepath.Join(buildDir, profile, name, "bin")
		binDir := ""
		if rootErr == nil {
			if relative, err := filepath.Rel(absoluteRoot, memberBin); err == nil {
				binDir = filepath.ToSlash(relative)
			}
		}
		names := make([]string, 0, len(member.Targets))
		for targetName := range member.Targets {
			names = append(names, targetName)
		}
		sort.Strings(names)
		for _, targetName := range names {
			target := member.Targets[targetName]
			output := target.OutputName
			if output == "" {
				output = targetName
			}
			project.Targets = append(project.Targets, Target{
				Name:       name + ":" + targetName,
				OutputName: strings.ReplaceAll(output, "\\", "/"),
				Executable: target.Type == "executable",
				BinDir:     binDir,
			})
		}
	}
	sort.Slice(project.Targets, func(i, j int) bool { return project.Targets[i].Name < project.Targets[j].Name })
	project.DefaultTarget = project.Targets[0].Name
	if len(project.Targets) == 0 {
		project.DefaultTarget = ""
	}
	return project
}

// defaultTarget picks the configured default target when it exists, falling
// back to the first target in sorted order.
func defaultTarget(configured []string, targets []Target) string {
	for _, name := range configured {
		for _, target := range targets {
			if target.Name == name {
				return name
			}
		}
	}
	if len(targets) > 0 {
		return targets[0].Name
	}
	return ""
}

// Generate builds the tasks document for project: the core build, test, and
// release tasks, a target picker, and one run task per executable target.
func Generate(project Project) Document {
	targets := append([]Target(nil), project.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	project.Targets = targets

	document := Document{Version: Version, Tasks: []Task{
		shellTask(BuildTaskLabel, []string{"build"}, &Group{Kind: "build", IsDefault: true}),
		shellTask("Trestle: Build All", []string{"build", "--all"}, nil),
	}}
	for _, member := range project.Members {
		document.Tasks = append(document.Tasks, shellTask("Trestle: Build "+member, []string{"build", "-p", member}, nil))
	}
	if len(project.Targets) > 0 {
		document.Tasks = append(document.Tasks, shellTask("Trestle: Build Current Target", []string{"build", "${input:" + targetInputID + "}"}, nil))
		document.Inputs = append(document.Inputs, targetInput(project))
	}
	for _, target := range project.Targets {
		if !target.Executable {
			continue
		}
		if target.BinDir == "" && project.BinDir == "" {
			continue
		}
		document.Tasks = append(document.Tasks, runTask(project, target))
	}
	document.Tasks = append(document.Tasks,
		shellTask("Trestle: Test", []string{"test", "--all"}, &Group{Kind: "test", IsDefault: true}),
		shellTask("Trestle: Release", []string{"release"}, nil),
	)
	return document
}

// shellTask builds one task that invokes the trestle executable in the
// workspace root.
func shellTask(label string, args []string, group *Group) Task {
	return Task{
		Label:          label,
		Type:           "shell",
		Command:        "trestle",
		Args:           args,
		Group:          group,
		ProblemMatcher: []string{},
		Presentation:   &Presentation{Reveal: "always", Panel: "shared"},
		Options:        &TaskOptions{Cwd: "${workspaceFolder}"},
	}
}

// runTask builds the task that executes one target's artifact. It is a process
// task so a workspace path containing spaces survives without quoting, and the
// Windows override appends the .exe suffix for native toolchains.
func runTask(project Project, target Target) Task {
	binDir := target.BinDir
	if binDir == "" {
		binDir = project.BinDir
	}
	binary := "${workspaceFolder}/" + binDir + "/" + target.OutputName
	dependsOn := BuildTaskLabel
	if member, _, qualified := strings.Cut(target.Name, ":"); qualified && len(project.Members) > 0 {
		dependsOn = "Trestle: Build " + member
	}
	task := Task{
		Label:          "Trestle: Run " + target.Name,
		Type:           "process",
		Command:        binary,
		DependsOn:      dependsOn,
		ProblemMatcher: []string{},
		Presentation:   &Presentation{Reveal: "always", Panel: "dedicated", Focus: true},
		Options:        &TaskOptions{Cwd: "${workspaceFolder}"},
	}
	if project.Native {
		task.Windows = &TaskOverride{Command: binary + ".exe"}
	}
	return task
}

// targetInput builds the pickString prompt listing every target.
func targetInput(project Project) Input {
	options := make([]string, 0, len(project.Targets))
	for _, target := range project.Targets {
		options = append(options, target.Name)
	}
	value := project.DefaultTarget
	if value == "" {
		value = options[0]
	}
	return Input{
		ID:          targetInputID,
		Type:        "pickString",
		Description: "Select the Trestle target to build",
		Options:     options,
		Default:     value,
	}
}
