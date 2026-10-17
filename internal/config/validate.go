package config

import (
	"fmt"
	"strings"
	"trestle/internal/toolchain"
)

// Validate reports the first problem that would make cfg unusable, checking the
// schema version, required fields, cross-references between targets, rules,
// packages and tasks, and finally that the target graph has no cycle. Each
// section is validated in turn by a dedicated helper so the first failure
// short-circuits in the same order as the configuration is laid out.
func Validate(cfg Config) error {
	if err := validateSchemaVersion(cfg); err != nil {
		return err
	}
	if err := validateProject(cfg); err != nil {
		return err
	}
	if err := validateToolchain(cfg); err != nil {
		return err
	}
	if err := validateBuild(cfg); err != nil {
		return err
	}
	if err := validateCompilerPresets(cfg); err != nil {
		return err
	}
	if err := validateTargets(cfg); err != nil {
		return err
	}
	if err := validateRules(cfg); err != nil {
		return err
	}
	if err := validatePackages(cfg); err != nil {
		return err
	}
	if err := validateTasks(cfg); err != nil {
		return err
	}
	if err := graphHasCycle(cfg.Targets); err != nil {
		return err
	}
	return validatePackage(cfg)
}

// validatePackageProvider accepts the built-in providers. The shared Packages
// page renders whichever provider is configured, a Conan package must name a
// conan recipe reference (name/version[@user/channel]), and "manual" declares
// hand-written usage paths instead of a package-manager lookup.
func validatePackageProvider(name string, pkg Package) error {
	switch pkg.Provider {
	case "", "vcpkg", "manual":
		return nil
	case "conan":
		if pkg.Version != "" && strings.Contains(pkg.Version, "[") {
			return fmt.Errorf("package %q: conan features belong on the reference name, not the version", name)
		}
		return nil
	default:
		return fmt.Errorf("package %q uses unsupported provider %q; use vcpkg or conan", name, pkg.Provider)
	}
}

func taskSettingAllowed(key string) bool {
	switch key {
	case "build.profile", "toolchain.c", "toolchain.cxx", "toolchain.msvc", "toolchain.mode", "toolchain.wsl_distribution", "vcpkg.root", "vcpkg.triplet", "conan.root", "conan.profile":
		return true
	}
	return false
}

// validateVersionSpec accepts "auto", a bare executable name, a machine-local
// path used as an escape hatch, or a version range such as 12.0~12.9.
func validateVersionSpec(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "auto") || toolchain.IsLocalPath(value) || !toolchain.LooksLikeVersion(value) {
		return nil
	}
	if _, err := toolchain.ParseRange(value); err != nil {
		return fmt.Errorf("E_CONFIG_INVALID_VERSION: [toolchain].%s: %w", field, err)
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
