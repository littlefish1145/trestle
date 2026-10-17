package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/config"
	"trestle/internal/source"
)

func ensureProjectConfig(path string) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != config.ErrNotFound {
		return cfg, err
	}
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config.Config{}, err
	}
	project, err := source.Analyze(root)
	if err != nil {
		return config.Config{}, err
	}
	if len(project.CompilationUnits()) == 0 {
		return config.Config{}, fmt.Errorf("E_PROJECT_NO_SOURCES: no C, C++, CUDA, or Objective-C sources found in %s", root)
	}
	name := filepath.Base(root)
	cfg = config.Default(name)
	cfg.Targets = projectTargets(root, name, project)
	if projectProvidesModules(project) {
		cfg.Build.Modules = true
	}
	if _, ok := cfg.Targets["app"]; !ok {
		cfg.Build.DefaultTargets = nil
	}
	if err := config.SaveExpected(path, cfg, "missing"); err != nil {
		return config.Config{}, err
	}
	return config.Load(path)
}

// projectTargets builds the initial target set for a freshly detected project: a
// static library when there are no entry points, one executable per entry point
// when there are several, and a single "app" executable for a lone entry point.
func projectTargets(root, name string, project source.Project) map[string]config.Target {
	entrySources := project.MainSources()
	targets := map[string]config.Target{}
	switch {
	case len(entrySources) == 0:
		targets["app"] = detectedTarget(root, name, "static", project.CompilationUnits())
	case len(entrySources) > 1:
		for _, sourcePath := range entrySources {
			info := project.Sources[sourcePath]
			outputName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
			if len(info.EntryPoints) > 0 && info.EntryPoints[0].InferredTarget != "" {
				outputName = info.EntryPoints[0].InferredTarget
			}
			targets[targetID(outputName)] = detectedTarget(root, outputName, "executable", []string{sourcePath})
		}
	default:
		info := project.Sources[entrySources[0]]
		outputName := name
		if len(info.EntryPoints) > 0 && info.EntryPoints[0].InferredTarget != "" {
			outputName = info.EntryPoints[0].InferredTarget
		}
		targets["app"] = detectedTarget(root, outputName, "executable", project.CompilationUnits())
	}
	return targets
}

// detectedTarget assembles a target from relative sources, adding the project's
// include directory when one exists.
func detectedTarget(root, outputName, targetType string, sources []string) config.Target {
	target := config.Target{Type: targetType, Sources: relativeSources(root, sources), OutputName: outputName}
	if hasIncludeDirectory(root) {
		target.IncludeDirs = []string{"include"}
	}
	return target
}

// projectProvidesModules reports whether any analyzed source exports a C++20
// module, which enables module support in the generated configuration.
func projectProvidesModules(project source.Project) bool {
	for _, info := range project.Sources {
		if info.Module != nil && info.Module.Provides != "" {
			return true
		}
	}
	return false
}

func hasIncludeDirectory(root string) bool {
	_, err := os.Stat(filepath.Join(root, "include"))
	return err == nil
}

func targetID(name string) string {
	var result strings.Builder
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' {
			result.WriteRune(char)
		} else {
			result.WriteByte('_')
		}
	}
	if result.Len() == 0 {
		return "app"
	}
	return result.String()
}

func relativeSources(root string, sources []string) []string {
	result := make([]string, 0, len(sources))
	for _, sourcePath := range sources {
		relative, err := filepath.Rel(root, sourcePath)
		if err != nil {
			relative = sourcePath
		}
		result = append(result, filepath.ToSlash(strings.TrimSpace(relative)))
	}
	return result
}
