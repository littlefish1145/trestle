package cmake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/config"
)

type Result struct {
	Project          string
	Targets          int
	Warnings         []string
	artifacts        map[string]string
	generatedTargets []string
}

type Options struct {
	CCompiler   string
	CXXCompiler string
	Environment map[string]string
}

type replyIndex struct {
	Objects []struct {
		Kind     string `json:"kind"`
		JSONFile string `json:"jsonFile"`
	} `json:"objects"`
}

type codeModel struct {
	Paths struct {
		Source string `json:"source"`
	} `json:"paths"`
	Configurations []struct {
		Name     string `json:"name"`
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
		Targets []struct {
			Name     string `json:"name"`
			ID       string `json:"id"`
			JSONFile string `json:"jsonFile"`
		} `json:"targets"`
	} `json:"configurations"`
}

type targetObject struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Sources []struct {
		Path      string `json:"path"`
		Generated bool   `json:"isGenerated"`
	} `json:"sources"`
	CompileGroups []struct {
		Language         string `json:"language"`
		LanguageStandard *struct {
			Standard string `json:"standard"`
		} `json:"languageStandard"`
		Includes []struct {
			Path string `json:"path"`
		} `json:"includes"`
		Defines []struct {
			Define string `json:"define"`
		} `json:"defines"`
		Fragments []struct {
			Fragment string `json:"fragment"`
		} `json:"compileCommandFragments"`
	} `json:"compileGroups"`
	Dependencies []struct {
		ID string `json:"id"`
	} `json:"dependencies"`
	Artifacts []struct {
		Path string `json:"path"`
	} `json:"artifacts"`
	Link *struct {
		CommandFragments []struct {
			Fragment string `json:"fragment"`
			Role     string `json:"role"`
		} `json:"commandFragments"`
	} `json:"link"`
}

type ctestModel struct {
	Tests []struct {
		Name       string   `json:"name"`
		Command    []string `json:"command"`
		Properties []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"properties"`
	} `json:"tests"`
}

// Import configures a CMake project in an isolated directory, reads CMake's
// supported File API, and writes an equivalent Trestle project model.
func Import(ctx context.Context, sourceRoot, configPath string, progress func(string)) (Result, error) {
	return ImportWithOptions(ctx, sourceRoot, configPath, progress, Options{})
}

func ImportWithOptions(ctx context.Context, sourceRoot, configPath string, progress func(string), options Options) (Result, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(filepath.Join(root, "CMakeLists.txt")); err != nil {
		return Result{}, fmt.Errorf("CMakeLists.txt was not found in %s", root)
	}
	cmakePath, err := exec.LookPath("cmake")
	if err != nil {
		return Result{}, fmt.Errorf("cmake is required for import: %w", err)
	}
	buildName := "cmake"
	if options.CXXCompiler != "" {
		compilerName := strings.TrimSuffix(strings.ToLower(filepath.Base(options.CXXCompiler)), filepath.Ext(options.CXXCompiler))
		buildName += "-" + compilerName
	}
	buildDir := filepath.Join(root, ".trestle", "import", buildName)
	queryDir := filepath.Join(buildDir, ".cmake", "api", "v1", "query", "client-trestle")
	if err := os.MkdirAll(queryDir, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(queryDir, "codemodel-v2"), nil, 0o644); err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress("Configuring CMake project in " + filepath.ToSlash(relativeOrAbsolute(root, buildDir)))
	}
	configureArgs := []string{"-S", root, "-B", buildDir, "-G", "Ninja", "-DCMAKE_TRY_COMPILE_TARGET_TYPE=STATIC_LIBRARY"}
	if options.CCompiler != "" {
		configureArgs = append(configureArgs, "-DCMAKE_C_COMPILER:FILEPATH="+options.CCompiler)
	}
	if options.CXXCompiler != "" {
		configureArgs = append(configureArgs, "-DCMAKE_CXX_COMPILER:FILEPATH="+options.CXXCompiler)
	}
	command := exec.CommandContext(ctx, cmakePath, configureArgs...)
	command.Dir = root
	command.Env = mergedEnvironment(options.Environment)
	output, err := command.CombinedOutput()
	if text := strings.TrimSpace(string(output)); text != "" && progress != nil {
		for _, line := range strings.Split(text, "\n") {
			progress(strings.TrimSpace(line))
		}
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return Result{}, fmt.Errorf("CMake configure failed: %w\n%s", err, detail)
		}
		return Result{}, fmt.Errorf("CMake configure failed: %w", err)
	}
	result, cfg, err := ParseReply(filepath.Join(buildDir, ".cmake", "api", "v1", "reply"), root)
	if err != nil {
		return Result{}, err
	}
	for _, target := range result.generatedTargets {
		if progress != nil {
			progress("Materializing CMake generated sources for " + target)
		}
		generate := exec.CommandContext(ctx, cmakePath, "--build", buildDir, "--target", target)
		generate.Dir = root
		generate.Env = mergedEnvironment(options.Environment)
		output, generateErr := generate.CombinedOutput()
		if generateErr != nil {
			return Result{}, fmt.Errorf("CMake could not generate sources for %s: %w\n%s", target, generateErr, strings.TrimSpace(string(output)))
		}
	}
	applyCTest(ctx, cmakePath, buildDir, &cfg, &result, progress)
	if existing, loadErr := config.Load(configPath); loadErr == nil {
		cfg.Toolchain = existing.Toolchain
		cfg.Vcpkg = existing.Vcpkg
		cfg.Build = existing.Build
		cfg.Packages = existing.Packages
		cfg.Package = existing.Package
		cfg.CompilerPresets = existing.CompilerPresets
	}
	validDefaults := []string{}
	for _, name := range cfg.Build.DefaultTargets {
		if _, ok := cfg.Targets[name]; ok {
			validDefaults = append(validDefaults, name)
		}
	}
	if len(validDefaults) == 0 {
		names := make([]string, 0, len(cfg.Targets))
		for name, target := range cfg.Targets {
			if target.Type == "executable" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			for name := range cfg.Targets {
				names = append(names, name)
			}
			sort.Strings(names)
		}
		if len(names) > 0 {
			validDefaults = []string{names[0]}
		}
	}
	cfg.Build.DefaultTargets = validDefaults
	validPackageTargets := []string{}
	for _, name := range cfg.Package.Targets {
		if _, ok := cfg.Targets[name]; ok {
			validPackageTargets = append(validPackageTargets, name)
		}
	}
	if len(validPackageTargets) == 0 {
		validPackageTargets = append([]string{}, validDefaults...)
	}
	cfg.Package.Targets = validPackageTargets
	if err := config.Save(configPath, cfg); err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(fmt.Sprintf("Imported %d targets into %s", result.Targets, filepath.Base(configPath)))
	}
	return result, nil
}

func mergedEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		if split := strings.IndexByte(entry, '='); split > 0 {
			values[strings.ToUpper(entry[:split])] = entry
		}
	}
	for key, value := range overrides {
		values[strings.ToUpper(key)] = key + "=" + value
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

// ParseReply converts a CMake File API reply directory into a Trestle config.
// It is separate from Import so fixtures can verify the mapping without CMake.
func ParseReply(replyDir, sourceRoot string) (Result, config.Config, error) {
	indexes, err := filepath.Glob(filepath.Join(replyDir, "index-*.json"))
	if err != nil || len(indexes) == 0 {
		return Result{}, config.Config{}, fmt.Errorf("CMake File API produced no reply index")
	}
	sort.Strings(indexes)
	var index replyIndex
	if err := readJSON(indexes[len(indexes)-1], &index); err != nil {
		return Result{}, config.Config{}, err
	}
	codemodelFile := ""
	for _, object := range index.Objects {
		if object.Kind == "codemodel" {
			codemodelFile = object.JSONFile
			break
		}
	}
	if codemodelFile == "" {
		return Result{}, config.Config{}, fmt.Errorf("CMake File API reply has no codemodel-v2 object")
	}
	var model codeModel
	if err := readJSON(filepath.Join(replyDir, codemodelFile), &model); err != nil {
		return Result{}, config.Config{}, err
	}
	if len(model.Configurations) == 0 {
		return Result{}, config.Config{}, fmt.Errorf("CMake codemodel has no configurations")
	}
	configuration := model.Configurations[0]
	projectName := filepath.Base(sourceRoot)
	if len(configuration.Projects) > 0 && configuration.Projects[0].Name != "" {
		projectName = configuration.Projects[0].Name
	}
	cfg := config.Default(projectName)
	cfg.Targets = map[string]config.Target{}
	result := Result{Project: projectName, artifacts: map[string]string{}}
	idToName := make(map[string]string, len(configuration.Targets))
	for _, reference := range configuration.Targets {
		idToName[reference.ID] = reference.Name
	}
	for _, reference := range configuration.Targets {
		var object targetObject
		if err := readJSON(filepath.Join(replyDir, reference.JSONFile), &object); err != nil {
			return Result{}, config.Config{}, err
		}
		targetType, supported := mapTargetType(object.Type)
		if !supported {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Skipped %s target %s", object.Type, reference.Name))
			continue
		}
		outputName := reference.Name
		if len(object.Artifacts) > 0 && object.Type != "OBJECT_LIBRARY" {
			artifactName := filepath.Base(filepath.FromSlash(object.Artifacts[0].Path))
			result.artifacts[reference.Name] = filepath.FromSlash(object.Artifacts[0].Path)
			outputName = strings.TrimSuffix(artifactName, filepath.Ext(artifactName))
			if targetType == "static" || targetType == "shared" {
				outputName = strings.TrimPrefix(outputName, "lib")
			}
		}
		target := config.Target{Type: targetType, OutputName: outputName}
		hasGeneratedSource := false
		for _, source := range object.Sources {
			path := source.Path
			if !filepath.IsAbs(path) {
				// File API source paths are relative to the top-level source
				// directory, not to the directory that defines the target.
				path = filepath.Join(sourceRoot, filepath.FromSlash(path))
			}
			if !compilable(path) {
				continue
			}
			if source.Generated {
				hasGeneratedSource = true
			}
			target.Sources = appendUnique(target.Sources, relativeOrAbsolute(sourceRoot, path))
		}
		if len(target.Sources) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Skipped target %s because it has no compilable sources", reference.Name))
			continue
		}
		for _, group := range object.CompileGroups {
			if group.LanguageStandard != nil {
				switch group.Language {
				case "C":
					target.CStandard = normalizeCStandard(group.LanguageStandard.Standard)
				case "CXX":
					target.CXXStandard = normalizeCXXStandard(group.LanguageStandard.Standard)
				}
			}
			for _, include := range group.Includes {
				target.PrivateIncludeDirs = appendUnique(target.PrivateIncludeDirs, relativeOrAbsolute(sourceRoot, include.Path))
			}
			for _, define := range group.Defines {
				target.PrivateDefines = appendUnique(target.PrivateDefines, define.Define)
			}
		}
		for _, dependency := range object.Dependencies {
			if name := idToName[dependency.ID]; name != "" {
				target.Dependencies = append(target.Dependencies, config.Dependency{Target: name, Scope: "private"})
			}
		}
		if object.Link != nil {
			internal := make(map[string]bool, len(target.Dependencies))
			for _, dependency := range target.Dependencies {
				internal[strings.ToLower(dependency.Target)] = true
			}
			for _, fragment := range object.Link.CommandFragments {
				if fragment.Role == "flags" && strings.Contains(strings.ToUpper(fragment.Fragment), "/DEF:") {
					target.ExportAllSymbols = true
				}
				importLinkFragment(&target, fragment.Role, fragment.Fragment, internal, sourceRoot)
			}
		}
		if hasGeneratedSource {
			result.generatedTargets = appendStringUnique(result.generatedTargets, reference.Name)
		}
		cfg.Targets[reference.Name] = target
	}
	// Remove dependency edges that point to targets we deliberately skipped.
	for name, target := range cfg.Targets {
		filtered := target.Dependencies[:0]
		for _, dependency := range target.Dependencies {
			if _, ok := cfg.Targets[dependency.Target]; ok {
				filtered = append(filtered, dependency)
			}
		}
		target.Dependencies = filtered
		cfg.Targets[name] = target
	}
	result.Targets = len(cfg.Targets)
	if result.Targets == 0 {
		return Result{}, config.Config{}, fmt.Errorf("CMake project has no executable or library targets with compilable sources")
	}
	return result, cfg, nil
}

func applyCTest(ctx context.Context, cmakePath, buildDir string, cfg *config.Config, result *Result, progress func(string)) {
	ctestPath := filepath.Join(filepath.Dir(cmakePath), "ctest")
	if _, err := os.Stat(ctestPath + ".exe"); err == nil {
		ctestPath += ".exe"
	} else if path, lookErr := exec.LookPath("ctest"); lookErr == nil {
		ctestPath = path
	} else {
		return
	}
	var placeholders []string
	for name, artifact := range result.artifacts {
		if target, ok := cfg.Targets[name]; !ok || target.Type != "executable" {
			continue
		}
		if !filepath.IsAbs(artifact) {
			artifact = filepath.Join(buildDir, artifact)
		}
		if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
			continue
		}
		file, err := os.OpenFile(artifact, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err == nil {
			_ = file.Close()
			placeholders = append(placeholders, artifact)
		}
	}
	defer func() {
		for _, placeholder := range placeholders {
			_ = os.Remove(placeholder)
		}
	}()
	output, err := exec.CommandContext(ctx, ctestPath, "--test-dir", buildDir, "--show-only=json-v1").Output()
	if err != nil {
		result.Warnings = append(result.Warnings, "CTest metadata could not be read")
		return
	}
	var suite ctestModel
	if json.Unmarshal(output, &suite) != nil {
		result.Warnings = append(result.Warnings, "CTest returned invalid JSON metadata")
		return
	}
	if progress != nil {
		progress(fmt.Sprintf("CTest reported %d job(s)", len(suite.Tests)))
	}
	claimed := map[string]string{}
	for _, test := range suite.Tests {
		if len(test.Command) == 0 {
			if target, ok := cfg.Targets[test.Name]; ok && target.Type == "executable" {
				target.Type = "test"
				target.TestGroup = "cmake"
				cfg.Targets[test.Name] = target
				claimed[test.Name] = test.Name
				if progress != nil {
					progress("Discovered CTest job " + test.Name + " → " + test.Name)
				}
			} else if progress != nil {
				progress("CTest job " + test.Name + " has no resolved command; leave it as an executable target")
			}
			continue
		}
		executable := strings.TrimSuffix(filepath.Base(test.Command[0]), filepath.Ext(test.Command[0]))
		matched := false
		for name, target := range cfg.Targets {
			if target.Type != "executable" || !strings.EqualFold(target.OutputName, executable) {
				continue
			}
			if previous := claimed[name]; previous != "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("CTest jobs %s and %s share target %s; imported the first job", previous, test.Name, name))
				break
			}
			target.Type = "test"
			target.TestGroup = "cmake"
			target.TestArgs = append([]string{}, test.Command[1:]...)
			for _, property := range test.Properties {
				if property.Name == "WORKING_DIRECTORY" {
					if value, ok := property.Value.(string); ok {
						target.TestWorkingDir = value
					}
				}
			}
			cfg.Targets[name] = target
			claimed[name] = test.Name
			matched = true
			if progress != nil {
				progress("Discovered CTest job " + test.Name + " → " + name)
			}
			break
		}
		if !matched && progress != nil {
			progress("CTest job " + test.Name + " uses an external command: " + test.Command[0])
		}
	}
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func mapTargetType(value string) (string, bool) {
	switch value {
	case "EXECUTABLE":
		return "executable", true
	case "STATIC_LIBRARY":
		return "static", true
	case "OBJECT_LIBRARY":
		// Trestle has no separate object-library surface. A static archive keeps
		// the same reusable object-code semantics for downstream targets.
		return "static", true
	case "SHARED_LIBRARY", "MODULE_LIBRARY":
		return "shared", true
	default:
		return "", false
	}
}

func compilable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".c++", ".m", ".mm", ".cu", ".ixx", ".cppm":
		return true
	default:
		return false
	}
}

func normalizeCStandard(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "c") {
		return value
	}
	if value == "90" {
		return "c89"
	}
	return "c" + value
}

func normalizeCXXStandard(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "c++") || strings.HasPrefix(value, "gnu++") {
		return value
	}
	return "c++" + value
}

func importLinkFragment(target *config.Target, role, fragment string, internal map[string]bool, sourceRoot string) {
	for _, token := range strings.Fields(fragment) {
		token = strings.Trim(token, "\"'")
		switch role {
		case "libraryPath":
			path := strings.TrimPrefix(token, "-L")
			path = strings.TrimPrefix(path, "/LIBPATH:")
			if path != "" {
				target.LibraryDirs = appendUnique(target.LibraryDirs, relativeOrAbsolute(sourceRoot, filepath.FromSlash(path)))
			}
		case "libraries":
			name := importedLibraryName(token)
			if name == "" || internal[strings.ToLower(name)] {
				continue
			}
			target.Libraries = appendStringUnique(target.Libraries, name)
		}
	}
}

func importedLibraryName(token string) string {
	if strings.HasPrefix(token, "-l") && len(token) > 2 {
		token = strings.TrimPrefix(token, "-l")
	}
	base := filepath.Base(filepath.FromSlash(token))
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		return base
	}
	if ext != ".lib" && ext != ".a" && ext != ".so" && ext != ".dylib" {
		return ""
	}
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return strings.TrimPrefix(name, "lib")
}

func relativeOrAbsolute(root, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Clean(relative)
	}
	return filepath.Clean(path)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if filepath.Clean(existing) == filepath.Clean(value) {
			return values
		}
	}
	return append(values, value)
}

func appendStringUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
