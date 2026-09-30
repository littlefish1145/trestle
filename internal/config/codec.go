package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"trestle/internal/diag"
	"trestle/internal/fsx"
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
	return Decode(path, data)
}

// Decode validates one immutable snapshot, including migration in memory.
func Decode(path string, data []byte) (Config, error) {
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
	cfg.sourcePath, _ = filepath.Abs(path)
	cfg.sourceFingerprint = Fingerprint(data)
	return cfg, nil
}

func Fingerprint(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Snapshot also represents absence so import/init can detect concurrent creation.
func Snapshot(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	return Fingerprint(data), nil
}

func (c *Config) normalize(root string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absoluteRoot
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
	expected := ""
	absolute, _ := filepath.Abs(path)
	if absolute == cfg.sourcePath {
		expected = cfg.sourceFingerprint
	} else if cfg.sourcePath != "" {
		destination, destErr := fsx.Canonical(path)
		source, sourceErr := fsx.Canonical(cfg.sourcePath)
		if destErr == nil && sourceErr == nil && (destination == source || runtime.GOOS == "windows" && strings.EqualFold(destination, source)) {
			expected = cfg.sourceFingerprint
		}
	}
	return SaveExpected(path, cfg, expected)
}

func SaveExpected(path string, cfg Config, expected string) error {
	root := filepath.Dir(path)
	canonical, err := fsx.Canonical(path)
	if err != nil {
		return err
	}
	path = canonical
	lock, err := fsx.TryLock(context.Background(), path, "save configuration")
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := Snapshot(path)
	if err != nil {
		return err
	}
	if expected != "" && current != expected {
		e := diag.New("E_CONFIG_CONFLICT", diag.StageConfig, "configuration changed before saving", nil)
		e.Detail = path
		e.Hints = []string{"Refresh the project, review the latest settings, and apply your change again. The current file was preserved."}
		return e
	}
	copyCfg := cfg
	if err := copyCfg.normalize(root); err != nil {
		return err
	}
	if err := Validate(copyCfg); err != nil {
		return err
	}
	// Store project-local paths relative to the file, so init -C relative-dir
	// and moving a checkout do not introduce duplicated directory prefixes.
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	local := func(value string) string {
		if value == "" {
			return value
		}
		rel, err := filepath.Rel(absoluteRoot, value)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
		return value
	}
	copyCfg.Build.BuildDir = local(copyCfg.Build.BuildDir)
	copyCfg.Build.CompileCommands = local(copyCfg.Build.CompileCommands)
	copyCfg.Vcpkg.Root = local(copyCfg.Vcpkg.Root)
	copyCfg.Package.Output = local(copyCfg.Package.Output)
	var buffer bytes.Buffer
	encoder := toml.NewEncoder(&buffer)
	encoder.Indent = "  "
	if err := encoder.Encode(copyCfg); err != nil {
		return err
	}
	if current != "missing" {
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var version struct {
			Version int `toml:"schema_version"`
		}
		if _, err := toml.Decode(string(original), &version); err != nil {
			return err
		}
		if version.Version < CurrentSchemaVersion {
			backup := fmt.Sprintf("%s.schema-v%d.%s.bak", path, version.Version, Fingerprint(original)[:12])
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err := fsx.AtomicWrite(backup, original); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				data, err := os.ReadFile(backup)
				if err != nil || !bytes.Equal(data, original) {
					return fmt.Errorf("E_CONFIG_BACKUP: cannot preserve original configuration at %s: %v", backup, err)
				}
			}
		}
		// Editors do not take our lock: detect edits made during encoding/backup.
		latest, err := Snapshot(path)
		if err != nil {
			return err
		}
		if latest != current {
			return fmt.Errorf("E_CONFIG_CONFLICT: %s changed while saving; refresh and retry", path)
		}
	}
	return fsx.AtomicWrite(path, buffer.Bytes())
}
