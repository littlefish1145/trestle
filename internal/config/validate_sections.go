package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"trestle/internal/condition"
)

// validateSchemaVersion rejects a configuration written for a schema the
// executable cannot read, distinguishing a project that is too old from one
// that is too new so the message tells the user which way to migrate.
func validateSchemaVersion(cfg Config) error {
	if cfg.SchemaVersion != CurrentSchemaVersion {
		if cfg.SchemaVersion > CurrentSchemaVersion {
			return fmt.Errorf("E_CONFIG_NEWER_SCHEMA: project requires schema %d; this executable supports up to %d", cfg.SchemaVersion, CurrentSchemaVersion)
		}
		return fmt.Errorf("E_CONFIG_OLD_SCHEMA: schema %d is not supported; migrate to schema %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	return nil
}

// validateProject checks the required project identity and requires at least one
// target, since a project without a buildable unit is meaningless.
func validateProject(cfg Config) error {
	if strings.TrimSpace(cfg.Project.Name) == "" {
		return fmt.Errorf("E_CONFIG_MISSING_REQUIRED: project.name is required")
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("E_CONFIG_MISSING_REQUIRED: at least one target is required")
	}
	return nil
}

// validateToolchain checks the toolchain selection mode, the optional CUDA and
// Vulkan execution modes, and every compiler version selector.
func validateToolchain(cfg Config) error {
	if cfg.Toolchain.Mode != "" && cfg.Toolchain.Mode != "native" && cfg.Toolchain.Mode != "wsl" {
		return fmt.Errorf("toolchain.mode %q is unsupported; use native or wsl", cfg.Toolchain.Mode)
	}
	for name, execution := range map[string]string{"cuda_execution": cfg.Toolchain.CUDAExecution, "vulkan_execution": cfg.Toolchain.VulkanExecution} {
		if execution != "" && execution != "native" && execution != "wsl" {
			return fmt.Errorf("toolchain.%s %q is unsupported; use native or wsl", name, execution)
		}
	}
	for _, spec := range []struct{ field, value string }{
		{"msvc", cfg.Toolchain.MSVC},
		{"c", cfg.Toolchain.C},
		{"cxx", cfg.Toolchain.CXX},
		{"cuda", cfg.Toolchain.CUDA},
	} {
		if err := validateVersionSpec(spec.field, spec.value); err != nil {
			return err
		}
	}
	return nil
}

// validateBuild checks that every default target named by the build section is
// declared in the targets table.
func validateBuild(cfg Config) error {
	for _, name := range cfg.Build.DefaultTargets {
		if _, ok := cfg.Targets[name]; !ok {
			return fmt.Errorf("build.default_targets references unknown target %q", name)
		}
	}
	return nil
}

// validateCompilerPresets checks that each named preset uses a supported toolchain
// mode; an empty mode means "inherit the project toolchain".
func validateCompilerPresets(cfg Config) error {
	for name, preset := range cfg.CompilerPresets {
		if preset.Mode != "" && preset.Mode != "native" && preset.Mode != "wsl" {
			return fmt.Errorf("compiler preset %q has unsupported mode %q", name, preset.Mode)
		}
	}
	return nil
}

// validateTargets checks each target's condition, type, sources, shader stage and
// dependency declarations, and that dependencies reference declared targets or
// packages.
func validateTargets(cfg Config) error {
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
	return nil
}

// validateRules checks each rule's condition, mode and target, and that every
// referenced target, package and dependency target is declared.
func validateRules(cfg Config) error {
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
	return nil
}

// validatePackages checks that every declared package uses a supported provider.
func validatePackages(cfg Config) error {
	for name, pkg := range cfg.Packages {
		if err := validatePackageProvider(name, pkg); err != nil {
			return err
		}
	}
	return nil
}

// validateTasks checks each task's command, timeout and overridden settings.
func validateTasks(cfg Config) error {
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
	return nil
}

// validatePackage checks the package section's format, target list and
// optimization selection, including that a custom optimization names a declared
// compiler preset.
func validatePackage(cfg Config) error {
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
