package desktoprpc

import (
	"fmt"
	"path/filepath"

	"trestle/internal/model"
	"trestle/internal/plan"
)

type generatedCommand struct {
	Action     plan.ID  `json:"action"`
	Executable string   `json:"executable"`
	Args       []string `json:"arguments"`
	Directory  string   `json:"directory"`
	Outputs    []string `json:"outputs"`
}

type generatedPage struct {
	Commands  []generatedCommand `json:"commands"`
	Available bool               `json:"available"`
	Truncated bool               `json:"truncated"`
	Next      *int               `json:"next_offset,omitempty"`
}

// Retain only presentation data, without exposing the Core's toolchain
// environment. Bounds apply to the whole build, even across workspace members.
func (j *job) captureCommands(manifest string, actions []plan.Action) {
	if j.generated == nil {
		j.generated = map[model.TargetID][]generatedCommand{}
	}
	for _, action := range actions {
		if action.Target == "" || action.Rule == "phony" {
			continue
		}
		size := len(action.ID) + len(action.Command.Exe) + len(action.Command.Dir) + len(manifest) + 120
		for _, values := range [][]string{action.Command.Args, action.Outputs} {
			for _, value := range values {
				size += len(value) + 8
			}
		}
		const perCommand = 64 << 10
		const perBuild = 2 << 20
		if size > perCommand || j.generatedBytes+size > perBuild {
			j.commandsCut = true
			continue
		}
		dir := action.Command.Dir
		if dir == "" {
			dir = filepath.Dir(manifest)
		}
		j.generatedBytes += size
		j.generated[action.Target] = append(j.generated[action.Target], generatedCommand{
			Action: action.ID, Executable: action.Command.Exe, Args: action.Command.Args,
			Directory: dir, Outputs: action.Outputs,
		})
	}
}

func (s *session) commands(buildID, target string, offset int64) (generatedPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.lookup(buildID)
	if err != nil {
		return generatedPage{}, err
	}
	if target == "" {
		return generatedPage{}, fmt.Errorf("target is required")
	}
	items := j.generated[model.TargetID(target)]
	if offset < 0 || offset > int64(len(items)) {
		return generatedPage{}, fmt.Errorf("command offset is outside the retained plan")
	}
	end := min(int(offset)+16, len(items))
	page := generatedPage{
		Commands:  append([]generatedCommand{}, items[int(offset):end]...),
		Available: len(items) != 0, Truncated: j.commandsCut,
	}
	if end < len(items) {
		page.Next = &end
	}
	return page, nil
}
