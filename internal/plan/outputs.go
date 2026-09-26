package plan

import (
	"path/filepath"

	"trestle/internal/config"
	"trestle/internal/model"
)

func Outputs(cfg config.Config, project model.ResolvedProject, options Options) (map[model.TargetID]string, error) {
	result := make(map[model.TargetID]string, len(project.Targets))
	for _, target := range project.Targets {
		output, err := targetOutput(target, filepath.Join(options.BuildDir, cfg.Build.Profile), options.Toolchain)
		if err != nil {
			return nil, err
		}
		result[target.ID] = relative(options.Root, filepath.Join(options.BuildDir, cfg.Build.Profile), output)
	}
	return result, nil
}
