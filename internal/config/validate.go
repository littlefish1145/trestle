package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func Validate(cfg Config) error {
	if cfg.SchemaVersion != CurrentSchemaVersion {
		if cfg.SchemaVersion > CurrentSchemaVersion {
			return fmt.Errorf("E_CONFIG_NEWER_SCHEMA: project requires schema %d; this executable supports up to %d", cfg.SchemaVersion, CurrentSchemaVersion)
		}
		return fmt.Errorf("E_CONFIG_OLD_SCHEMA: schema %d is not supported; migrate to schema %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	if strings.TrimSpace(cfg.Project.Name) == "" {
		return fmt.Errorf("E_CONFIG_MISSING_REQUIRED: project.name is required")
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("E_CONFIG_MISSING_REQUIRED: at least one target is required")
	}
	if cfg.Toolchain.Mode != "" && cfg.Toolchain.Mode != "native" && cfg.Toolchain.Mode != "wsl" {
		return fmt.Errorf("toolchain.mode %q is unsupported; use native or wsl", cfg.Toolchain.Mode)
	}
	for name, execution := range map[string]string{"cuda_execution": cfg.Toolchain.CUDAExecution, "vulkan_execution": cfg.Toolchain.VulkanExecution} {
		if execution != "" && execution != "native" && execution != "wsl" {
			return fmt.Errorf("toolchain.%s %q is unsupported; use native or wsl", name, execution)
		}
	}
	for _, name := range cfg.Build.DefaultTargets {
		if _, ok := cfg.Targets[name]; !ok {
			return fmt.Errorf("build.default_targets references unknown target %q", name)
		}
	}
	for name, preset := range cfg.CompilerPresets {
		if preset.Mode != "" && preset.Mode != "native" && preset.Mode != "wsl" {
			return fmt.Errorf("compiler preset %q has unsupported mode %q", name, preset.Mode)
		}
	}
	for name, target := range cfg.Targets {
		switch target.Type {
		case "static", "shared", "executable", "test", "shader":
		default:
			return fmt.Errorf("target %q has unsupported type %q", name, target.Type)
		}
		if len(target.Sources) == 0 {
			return fmt.Errorf("target %q must contain at least one source", name)
		}
		if target.Type == "shader" && target.ShaderStage == "" {
			return fmt.Errorf("target %q shader_stage is required", name)
		}
		for _, source := range target.Sources {
			if _, err := filepath.Abs(source); err != nil {
				return fmt.Errorf("target %q source %q: %w", name, source, err)
			}
		}
		for _, dep := range target.Dependencies {
			if (dep.Target == "") == (dep.Package == "") {
				return fmt.Errorf("target %q dependency must set exactly one of target or package", name)
			}
			if dep.Scope != "private" && dep.Scope != "public" && dep.Scope != "interface" {
				return fmt.Errorf("target %q dependency has invalid scope %q", name, dep.Scope)
			}
			if dep.Target != "" {
				if _, ok := cfg.Targets[dep.Target]; !ok {
					return fmt.Errorf("target %q depends on unknown target %q", name, dep.Target)
				}
			} else if _, ok := cfg.Packages[dep.Package]; !ok {
				return fmt.Errorf("target %q depends on undeclared package %q", name, dep.Package)
			}
		}
	}
	if err := graphHasCycle(cfg.Targets); err != nil {
		return err
	}
	if cfg.Package.Format != "zip" {
		return fmt.Errorf("package.format %q is unsupported; use zip", cfg.Package.Format)
	}
	for _, name := range cfg.Package.Targets {
		if _, ok := cfg.Targets[name]; !ok {
			return fmt.Errorf("package.targets references unknown target %q", name)
		}
	}
	if value := cfg.Package.Optimization; value != "" && value != "balanced" && value != "speed" && value != "size" {
		if !strings.HasPrefix(value, "custom:") {
			return fmt.Errorf("package.optimization %q is unsupported", value)
		}
		name := strings.TrimPrefix(value, "custom:")
		if _, ok := cfg.CompilerPresets[name]; !ok {
			return fmt.Errorf("package.optimization references missing compiler preset %q", name)
		}
	}
	return nil
}

func graphHasCycle(targets map[string]Target) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := make(map[string]int, len(targets))
	var visit func(string, []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case gray:
			return fmt.Errorf("E_DEP_CYCLE: target dependency cycle: %s", strings.Join(append(path, name), " -> "))
		case black:
			return nil
		}
		state[name] = gray
		path = append(path, name)
		for _, dep := range targets[name].Dependencies {
			if dep.Target != "" {
				if err := visit(dep.Target, path); err != nil {
					return err
				}
			}
		}
		state[name] = black
		return nil
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	for _, name := range names {
		if err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}
