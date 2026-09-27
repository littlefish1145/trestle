package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"trestle/internal/condition"
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
		if err := condition.Validate(target.When); err != nil {
			return fmt.Errorf("target %q when: %w", name, err)
		}
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
	for i, rule := range cfg.Rules {
		if strings.TrimSpace(rule.When) == "" {
			return fmt.Errorf("rule %d requires when", i+1)
		}
		if err := condition.Validate(rule.When); err != nil {
			return fmt.Errorf("rule %d: %w", i+1, err)
		}
		if rule.Mode != "" && rule.Mode != "native" && rule.Mode != "wsl" {
			return fmt.Errorf("rule %d has invalid mode %q", i+1, rule.Mode)
		}
		if rule.Target != "" {
			if _, ok := cfg.Targets[rule.Target]; !ok {
				return fmt.Errorf("rule %d references unknown target %q", i+1, rule.Target)
			}
		}
		if len(rule.Packages) > 0 && rule.Target == "" {
			return fmt.Errorf("rule %d packages require target", i+1)
		}
		if len(rule.DependsOn) > 0 && rule.Target == "" {
			return fmt.Errorf("rule %d depends_on requires target", i+1)
		}
		for _, pkg := range rule.Packages {
			if _, ok := cfg.Packages[pkg]; !ok {
				return fmt.Errorf("rule %d references undeclared package %q", i+1, pkg)
			}
		}
		for _, dependency := range rule.DependsOn {
			if _, ok := cfg.Targets[dependency]; !ok {
				return fmt.Errorf("rule %d references unknown dependency target %q", i+1, dependency)
			}
		}
		for _, target := range rule.Targets {
			if _, ok := cfg.Targets[target]; !ok {
				return fmt.Errorf("rule %d references unknown default target %q", i+1, target)
			}
		}
	}
	for name, task := range cfg.Tasks {
		if len(task.Command) > 0 && strings.TrimSpace(task.Command[0]) == "" {
			return fmt.Errorf("task %q has an empty command", name)
		}
		if task.Timeout != "" {
			duration, err := time.ParseDuration(task.Timeout)
			if err != nil || duration <= 0 {
				return fmt.Errorf("task %q timeout must be a positive duration such as 30s or 5m", name)
			}
		}
		for key := range task.Set {
			if !taskSettingAllowed(key) {
				return fmt.Errorf("task %q cannot set %q", name, key)
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

func taskSettingAllowed(key string) bool {
	switch key {
	case "build.profile", "toolchain.c", "toolchain.cxx", "toolchain.mode", "toolchain.wsl_distribution", "vcpkg.root", "vcpkg.triplet":
		return true
	}
	return false
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
