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
	"trestle/internal/toolchain"
)

// ErrNotFound reports that no trestle.toml exists at the requested path, so
// callers such as init can distinguish an absent file from an unreadable one.
var ErrNotFound = errors.New("trestle.toml not found")

// DefaultToolchainCacheDir stores the discovered toolchain inventory inside the
// project's ignored .trestle directory.
const DefaultToolchainCacheDir = ".trestle/toolchain"

// Default returns a minimal, valid configuration for a new project, with
// sensible language standards, a single executable target and an auto-detected
// toolchain.
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
		Toolchain:       Toolchain{MSVC: "auto", C: "auto", CXX: "auto", Mode: "native", CacheDir: DefaultToolchainCacheDir, CUDAExecution: "native", VulkanExecution: "native"},
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

// Load reads trestle.toml from path and decodes it. It returns ErrNotFound when
// the file does not exist.
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

// Fingerprint returns the SHA-256 hex digest of raw configuration bytes, used to
// detect concurrent edits between loading and saving.
func Fingerprint(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Root is the project directory holding trestle.toml, used to resolve
// project-relative settings such as the toolchain cache directory.
func (c Config) Root() string {
	if c.sourcePath == "" {
		return "."
	}
	return filepath.Dir(c.sourcePath)
}

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

// normalize fills in every omitted setting and rewrites project-local paths
// relative to root, so a decoded configuration is complete before validation.
func (c *Config) normalize(root string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	normalizePath := relativePathNormalizer(absoluteRoot)
	c.applyDefaults()
	c.repairWSLToolchain()
	c.normalizeRootPaths(normalizePath)
	c.normalizeTargets(normalizePath)
	c.normalizePackages()
	c.selectDefaultTargets()
	c.applyPackagingDefaults(normalizePath)
	return nil
}

// applyDefaults fills empty scalar settings and nil collections with the values
// a fresh configuration uses.
func (c *Config) applyDefaults() {
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
	if c.Toolchain.MSVC == "" {
		c.Toolchain.MSVC = "auto"
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
	if c.Toolchain.CacheDir == "" {
		c.Toolchain.CacheDir = DefaultToolchainCacheDir
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
	if c.Conan.Profile == "" {
		c.Conan.Profile = "default"
	}
}

// repairWSLToolchain heals configurations created by the old TUI mode toggle. A
// Windows compiler cannot be executed inside WSL; WSL compilers use Linux paths.
func (c *Config) repairWSLToolchain() {
	if c.Toolchain.Mode == "wsl" && (filepath.VolumeName(c.Toolchain.CXX) != "" || strings.Contains(c.Toolchain.CXX, `\`)) {
		c.Toolchain.Mode = "native"
		c.Toolchain.WSLDistribution = ""
	}
}

// relativePathNormalizer returns a function that cleans a path, joining
// project-local values onto root while leaving absolute and empty values
// untouched.
func relativePathNormalizer(root string) func(string) string {
	return func(value string) string {
		if value == "" || filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Clean(filepath.Join(root, value))
	}
}

// normalizeRootPaths rewrites the build-related directories that live directly
// on the configuration's top-level sections.
func (c *Config) normalizeRootPaths(normalize func(string) string) {
	c.Build.BuildDir = normalize(c.Build.BuildDir)
	if c.Build.CompileCommands != "" {
		c.Build.CompileCommands = normalize(c.Build.CompileCommands)
	}
	c.Toolchain.CacheDir = normalize(c.Toolchain.CacheDir)
	if c.Toolchain.CUDARoot != "" {
		c.Toolchain.CUDARoot = normalize(c.Toolchain.CUDARoot)
	}
	if c.Vcpkg.Root != "" {
		c.Vcpkg.Root = normalize(c.Vcpkg.Root)
	}
	if c.Conan.Root != "" {
		c.Conan.Root = normalize(c.Conan.Root)
	}
	if c.Conan.BuildRoot != "" {
		c.Conan.BuildRoot = normalize(c.Conan.BuildRoot)
	}
}

// normalizeTargets cleans each target's path lists and defaults its dependency
// scopes and output name.
func (c *Config) normalizeTargets(normalize func(string) string) {
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
}

// normalizePackages cleans each package's path lists and defaults its port name
// to its configuration key.
func (c *Config) normalizePackages() {
	for name, pkg := range c.Packages {
		pkg.IncludeDirs = cleanPaths(pkg.IncludeDirs)
		pkg.LibraryDirs = cleanPaths(pkg.LibraryDirs)
		pkg.RuntimeFiles = cleanPaths(pkg.RuntimeFiles)
		if pkg.Port == "" {
			pkg.Port = name
		}
		c.Packages[name] = pkg
	}
}

// selectDefaultTargets chooses the targets built when none are configured,
// preferring the project name, then "app", then the sorted executable targets,
// then whichever target sorts first.
func (c *Config) selectDefaultTargets() {
	if len(c.Build.DefaultTargets) != 0 || len(c.Targets) == 0 {
		return
	}
	for _, candidate := range []string{c.Project.Name, "app"} {
		if _, ok := c.Targets[candidate]; ok {
			c.Build.DefaultTargets = []string{candidate}
			return
		}
	}
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

// applyPackagingDefaults fills the package output defaults and rewrites the
// package output directory relative to the project root.
func (c *Config) applyPackagingDefaults(normalize func(string) string) {
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

// Save validates cfg and writes it to path, but only when the file on disk still
// matches the version cfg was loaded from. The staleness check is skipped when
// path does not point at cfg's own source.
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

// SaveExpected writes cfg to path after confirming the file still matches
// expected, normalizing project-local paths relative to the file's directory and
// preserving an unmodified pre-migration original as a backup. An empty expected
// disables the staleness check.
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
		return configChanged(path)
	}
	copyCfg := cfg
	if err := copyCfg.normalize(root); err != nil {
		return err
	}
	if err := Validate(copyCfg); err != nil {
		return err
	}
	if err := copyCfg.makePathsRelative(root); err != nil {
		return err
	}
	data, err := encodeTOML(copyCfg)
	if err != nil {
		return err
	}
	if current != "missing" {
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := backupLegacySchema(path, original); err != nil {
			return err
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
	return fsx.AtomicWrite(path, data)
}

// configChanged builds the diagnostic raised when the file on disk no longer
// matches the version the caller loaded.
func configChanged(path string) error {
	e := diag.New("E_CONFIG_CONFLICT", diag.StageConfig, "configuration changed before saving", nil)
	e.Detail = path
	e.Hints = []string{"Refresh the project, review the latest settings, and apply your change again. The current file was preserved."}
	return e
}

// makePathsRelative stores project-local paths relative to root, so init -C
// relative-dir and moving a checkout do not introduce duplicated directory
// prefixes.
func (c *Config) makePathsRelative(root string) error {
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
	c.Build.BuildDir = local(c.Build.BuildDir)
	c.Build.CompileCommands = local(c.Build.CompileCommands)
	c.Vcpkg.Root = local(c.Vcpkg.Root)
	c.Conan.Root = local(c.Conan.Root)
	c.Conan.BuildRoot = local(c.Conan.BuildRoot)
	c.Package.Output = local(c.Package.Output)
	c.Toolchain.CacheDir = local(c.Toolchain.CacheDir)
	if c.Toolchain.CUDARoot != "" && !toolchain.IsLocalPath(c.Toolchain.CUDARoot) {
		c.Toolchain.CUDARoot = local(c.Toolchain.CUDARoot)
	}
	return nil
}

// encodeTOML renders cfg with the canonical two-space indentation.
func encodeTOML(cfg Config) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := toml.NewEncoder(&buffer)
	encoder.Indent = "  "
	if err := encoder.Encode(cfg); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// backupLegacySchema preserves an unmodified pre-migration original beside the
// configuration, unless an identical backup already exists.
func backupLegacySchema(path string, original []byte) error {
	var version struct {
		Version int `toml:"schema_version"`
	}
	if _, err := toml.Decode(string(original), &version); err != nil {
		return err
	}
	if version.Version >= CurrentSchemaVersion {
		return nil
	}
	backup := fmt.Sprintf("%s.schema-v%d.%s.bak", path, version.Version, Fingerprint(original)[:12])
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		return fsx.AtomicWrite(backup, original)
	} else if err != nil {
		return err
	}
	data, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(data, original) {
		return fmt.Errorf("E_CONFIG_BACKUP: cannot preserve original configuration at %s: %v", backup, err)
	}
	return nil
}
