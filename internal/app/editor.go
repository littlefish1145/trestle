package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"trestle/internal/config"
	"trestle/internal/vscode"
)

// EditorConfigOptions selects how EditorConfig treats an existing
// .vscode/tasks.json.
type EditorConfigOptions struct {
	Force bool
	Merge bool
}

// EditorConfig generates the VS Code tasks for the project at configPath. An
// existing .vscode/tasks.json is never overwritten silently: without options
// the call fails, Merge updates only the generated tasks, and Force replaces
// the file.
func EditorConfig(configPath string, options EditorConfigOptions) (vscode.WriteResult, error) {
	if options.Force && options.Merge {
		return vscode.WriteResult{}, fmt.Errorf("E_EDITOR_CONFIG_FLAGS: -force and -merge cannot be used together")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return vscode.WriteResult{}, err
	}
	mode := vscode.ModeRefuse
	switch {
	case options.Force:
		mode = vscode.ModeReplace
	case options.Merge:
		mode = vscode.ModeMerge
	}
	root := cfg.Root()
	document := vscode.Generate(vscode.FromConfig(root, cfg))
	if cfg.Workspace != nil {
		workspace, err := config.ParseWorkspace(configPath)
		if err != nil {
			return vscode.WriteResult{}, err
		}
		document = vscode.Generate(vscode.FromWorkspace(root, workspace))
	}
	result, err := vscode.Write(filepath.Join(root, ".vscode", "tasks.json"), document, mode)
	if err != nil {
		if errors.Is(err, vscode.ErrExists) {
			return result, fmt.Errorf("%w; re-run with -merge to update the generated tasks or -force to overwrite", err)
		}
		return vscode.WriteResult{}, err
	}
	return result, nil
}
