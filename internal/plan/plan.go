// Package plan turns a resolved project into the concrete action graph Ninja
// runs: compile, archive, link, shader, module, test and runtime-copy steps,
// each with its inputs, outputs and environment.
package plan

import "trestle/internal/model"

// ID is a plan-local identity for an action or rule. Values are sanitized so
// they are safe to use as Ninja target names.
type ID string

// Command is a single process invocation: the executable, its arguments, an
// optional environment overlay, and the working directory.
type Command struct {
	Exe  string
	Args []string
	Env  map[string]string
	Dir  string
}

// DepfileSpec points at the compiler-written dependency file Ninja reads back
// after the command runs.
type DepfileSpec struct {
	Path string
}

// Action is one node of the build graph. Rule groups actions that share a
// command shape, while the remaining fields mirror Ninja's per-edge inputs,
// outputs, implicit dependencies and pool assignment.
type Action struct {
	ID           ID
	Rule         ID
	Command      Command
	Inputs       []string
	Implicit     []string
	OrderOnly    []string
	Outputs      []string
	Depfile      *DepfileSpec
	Deps         string
	Pool         string
	ResponseFile bool
}

// BuildPlan is the complete Ninja program for a project: its ordered actions,
// default targets, global variables, and the runtime files each target needs
// beside it.
type BuildPlan struct {
	Actions        []Action
	Defaults       []string
	Variables      map[string]string
	CompileCommand string
	RuntimeOutputs map[model.TargetID][]string
}

// Validate checks that every action names a rule and an executable and has at
// least one output, and that no two actions claim the same output path.
func (p BuildPlan) Validate() error {
	seen := map[string]bool{}
	for _, action := range p.Actions {
		if action.Rule == "" || len(action.Outputs) == 0 || action.Command.Exe == "" {
			return &ValidationError{Action: string(action.ID)}
		}
		for _, output := range action.Outputs {
			if seen[output] {
				return &ValidationError{Action: string(action.ID), Message: "duplicate output " + output}
			}
			seen[output] = true
		}
	}
	return nil
}

// ValidationError reports the first action that fails BuildPlan.Validate.
type ValidationError struct {
	Action  string
	Message string
}

// Error describes the invalid action, including the detail when one is set.
func (e *ValidationError) Error() string {
	if e.Message == "" {
		return "invalid action " + e.Action
	}
	return "invalid action " + e.Action + ": " + e.Message
}
