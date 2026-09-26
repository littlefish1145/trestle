package plan

import "trestle/internal/model"

type ID string

type Command struct {
	Exe  string
	Args []string
	Env  map[string]string
	Dir  string
}

type DepfileSpec struct {
	Path string
}

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

type BuildPlan struct {
	Actions        []Action
	Defaults       []string
	Variables      map[string]string
	CompileCommand string
	RuntimeOutputs map[model.TargetID][]string
}

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

type ValidationError struct {
	Action  string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Message == "" {
		return "invalid action " + e.Action
	}
	return "invalid action " + e.Action + ": " + e.Message
}
