package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

var ErrNotFound = errors.New("trestle.toml not found")

func Default(projectName string) Config {
	if projectName == "" {
		projectName = "trestle-project"
	}
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		Project:       Project{Name: projectName},
		Build: Build{
			Profile:            "debug",
			BuildDir:           "build",
			CStandard:          "c17",
			CXXStandard:        "c++20",
			CompileFlags:       []string{},
			CFlags:             []string{},
			CXXFlags:           []string{},
			LinkFlags:          []string{},
			DefaultTargets:     []string{"app"},
			CompileCommands:    "compile_commands.json",
			AutoCompileShaders: true,
		},
		Toolchain:       Toolchain{C: "auto", CXX: "auto", Mode: "native", CUDAExecution: "native", VulkanExecution: "native"},
		CompilerPresets: map[string]CompilerPreset{},
		Targets: map[string]Target{
			"app": {
				Type:         "executable",
				Sources:      []string{"src/main.cpp"},
				OutputName:   projectName,
				Dependencies: []Dependency{},
			},
		},
		Packages: map[string]Package{},
		Package:  PackageOutput{Format: "zip", Output: "dist/" + projectName + ".zip", Optimization: "balanced"},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrNotFound
		}
		return Config{}, err
	}
	migrated, err := Migrate(data)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	meta, err := toml.Decode(string(migrated), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse TOML: %w", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Config{}, fmt.Errorf("unknown configuration fields: %s", strings.Join(keys, ", "))
	}
	if err := cfg.normalize(filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) normalize(root string) error {
	if c.Build.Profile == "" {
		c.Build.Profile = "debug"
	}
	if c.Build.BuildDir == "" {
		c.Build.BuildDir = "build"
	}
	if c.Vcpkg.Triplet == "" {
		c.Vcpkg.Triplet = "auto"
	}
	if c.Build.CStandard == "" {
		c.Build.CStandard = "c17"
	}
	if c.Build.CXXStandard == "" {
		c.Build.CXXStandard = "c++20"
	}
	if c.Build.CompileCommands == "" {
		c.Build.CompileCommands = "compile_commands.json"
	}
	if c.Toolchain.Mode == "" {
		c.Toolchain.Mode = "native"
	}
	// Heal configurations created by the old TUI mode toggle. A Windows
	// compiler cannot be executed inside WSL; WSL compilers use Linux paths.
	if c.Toolchain.Mode == "wsl" && (filepath.VolumeName(c.Toolchain.CXX) != "" || strings.Contains(c.Toolchain.CXX, `\`)) {
		c.Toolchain.Mode = "native"
		c.Toolchain.WSLDistribution = ""
	}
	if c.Toolchain.C == "" {
		c.Toolchain.C = "auto"
	}
	if c.Toolchain.CXX == "" {
		c.Toolchain.CXX = "auto"
	}
	if c.Toolchain.CUDAMode == "" {
		c.Toolchain.CUDAMode = "whole"
	}
	if c.Toolchain.CUDAExecution == "" {
		c.Toolchain.CUDAExecution = "native"
	}
	if c.Toolchain.VulkanExecution == "" {
		c.Toolchain.VulkanExecution = "native"
	}
	if len(c.Toolchain.CUDAArchitectures) == 0 {
		c.Toolchain.CUDAArchitectures = []string{"sm_75"}
	}
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	if c.Packages == nil {
		c.Packages = map[string]Package{}
	}
	if c.CompilerPresets == nil {
		c.CompilerPresets = map[string]CompilerPreset{}
	}
	normalize := func(value string) string {
		if value == "" || filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Clean(filepath.Join(root, value))
	}
	c.Build.BuildDir = normalize(c.Build.BuildDir)
	if c.Build.CompileCommands != "" {
		c.Build.CompileCommands = normalize(c.Build.CompileCommands)
	}
	if c.Vcpkg.Root != "" {
		c.Vcpkg.Root = normalize(c.Vcpkg.Root)
	}
	for name, target := range c.Targets {
		target.Sources = cleanPaths(target.Sources)
		target.IncludeDirs = cleanPaths(target.IncludeDirs)
		target.PrivateIncludeDirs = cleanPaths(target.PrivateIncludeDirs)
		target.LibraryDirs = cleanPaths(target.LibraryDirs)
		if target.TestWorkingDir != "" {
			target.TestWorkingDir = normalize(target.TestWorkingDir)
		}
		for i, dep := range target.Dependencies {
			if dep.Scope == "" {
				dep.Scope = "private"
				target.Dependencies[i] = dep
			}
		}
		if target.OutputName == "" {
			target.OutputName = name
		}
		c.Targets[name] = target
	}
	for name, pkg := range c.Packages {
		pkg.IncludeDirs = cleanPaths(pkg.IncludeDirs)
		pkg.LibraryDirs = cleanPaths(pkg.LibraryDirs)
		pkg.RuntimeFiles = cleanPaths(pkg.RuntimeFiles)
		if pkg.Port == "" {
			pkg.Port = name
		}
		c.Packages[name] = pkg
	}
	if len(c.Build.DefaultTargets) == 0 && len(c.Targets) > 0 {
		for _, candidate := range []string{c.Project.Name, "app"} {
			if _, ok := c.Targets[candidate]; ok {
				c.Build.DefaultTargets = []string{candidate}
				break
			}
		}
		if len(c.Build.DefaultTargets) == 0 {
			names := make([]string, 0, len(c.Targets))
			for name, target := range c.Targets {
				if target.Type == "executable" {
					names = append(names, name)
				}
			}
			if len(names) == 0 {
				for name := range c.Targets {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			c.Build.DefaultTargets = []string{names[0]}
		}
	}
	if c.Package.Format == "" {
		c.Package.Format = "zip"
	}
	if c.Package.Output == "" {
		c.Package.Output = filepath.Join("dist", c.Project.Name+"."+c.Package.Format)
	}
	if c.Package.Optimization == "" {
		c.Package.Optimization = "balanced"
	}
	if len(c.Package.Targets) == 0 {
		c.Package.Targets = append([]string{}, c.Build.DefaultTargets...)
	}
	c.Package.Output = normalize(c.Package.Output)
	return nil
}

func cleanPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if filepath.Clean(path) != "." {
			result = append(result, path)
		}
	}
	return result
}

func Save(path string, cfg Config) error {
	root := filepath.Dir(path)
	copyCfg := cfg
	copyCfg.normalize(root)
	var buffer bytes.Buffer
	encoder := toml.NewEncoder(&buffer)
	encoder.Indent = "  "
	if err := encoder.Encode(copyCfg); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}
