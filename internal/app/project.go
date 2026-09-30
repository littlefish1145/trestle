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
	units := project.CompilationUnits()
	if len(units) == 0 {
		return config.Config{}, fmt.Errorf("E_PROJECT_NO_SOURCES: no C, C++, CUDA, or Objective-C sources found in %s", root)
	}
	name := filepath.Base(root)
	cfg = config.Default(name)
	cfg.Targets = map[string]config.Target{}
	entrySources := project.MainSources()
	if len(entrySources) == 0 {
		target := config.Target{Type: "static", Sources: relativeSources(root, units), OutputName: name}
		if hasIncludeDirectory(root) {
			target.IncludeDirs = []string{"include"}
		}
		cfg.Targets["app"] = target
	} else if len(entrySources) == 1 {
		info := project.Sources[entrySources[0]]
		outputName := name
		if len(info.EntryPoints) > 0 && info.EntryPoints[0].InferredTarget != "" {
			outputName = info.EntryPoints[0].InferredTarget
		}
		target := config.Target{Type: "executable", Sources: relativeSources(root, units), OutputName: outputName}
		if hasIncludeDirectory(root) {
			target.IncludeDirs = []string{"include"}
		}
		cfg.Targets["app"] = target
	} else {
		for _, sourcePath := range entrySources {
			info := project.Sources[sourcePath]
			outputName := filepath.Base(sourcePath)
			outputName = strings.TrimSuffix(outputName, filepath.Ext(outputName))
			if len(info.EntryPoints) > 0 && info.EntryPoints[0].InferredTarget != "" {
				outputName = info.EntryPoints[0].InferredTarget
			}
			target := config.Target{Type: "executable", Sources: relativeSources(root, []string{sourcePath}), OutputName: outputName}
			if hasIncludeDirectory(root) {
				target.IncludeDirs = []string{"include"}
			}
			cfg.Targets[targetID(outputName)] = target
		}
	}
	for _, info := range project.Sources {
		if info.Module != nil && info.Module.Provides != "" {
			cfg.Build.Modules = true
			break
		}
	}
	if _, ok := cfg.Targets["app"]; !ok {
		cfg.Build.DefaultTargets = nil
	}
	if err := config.SaveExpected(path, cfg, "missing"); err != nil {
		return config.Config{}, err
	}
	return config.Load(path)
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
