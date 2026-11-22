package cmake

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"trestle/internal/model"
	"trestle/internal/processx"
)

var cacheKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*(:BOOL|:FILEPATH|:PATH|:STRING|:INTERNAL)?$`)

// WorkspaceCacheArgs translates stored cache entries into deterministic CMake arguments.
func WorkspaceCacheArgs(cache map[string]string) ([]string, error) {
	keys := make([]string, 0, len(cache))
	for key := range cache {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	args := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		name, _, _ := strings.Cut(key, ":")
		switch {
		case !cacheKey.MatchString(key) || strings.ContainsRune(cache[key], 0):
			return nil, fmt.Errorf("invalid CMake cache entry %q", key)
		case name == "CMAKE_BUILD_TYPE" || name == "CMAKE_EXPORT_COMPILE_COMMANDS":
			return nil, fmt.Errorf("CMake cache entry %q is managed by the workspace", key)
		case seen[name]:
			return nil, fmt.Errorf("duplicate CMake cache entry %q", name)
		}
		seen[name] = true
		args = append(args, "-D"+key+"="+cache[key])
	}
	return args, nil
}

// ConfigureWorkspace generates the original CMake build graph and parses its metadata.
func ConfigureWorkspace(ctx context.Context, source, buildDir, profile string, cacheArgs, env []string, progress func(string)) (model.WorkspacePlan, error) {
	root, err := resolveImportRoot(source)
	if err != nil {
		return model.WorkspacePlan{}, err
	}
	cmakePath, err := locateCMake()
	if err != nil {
		return model.WorkspacePlan{}, err
	}
	if err := prepareFileAPIQuery(buildDir); err != nil {
		return model.WorkspacePlan{}, err
	}
	buildType := profile
	switch strings.ToLower(profile) {
	case "debug":
		buildType = "Debug"
	case "release":
		buildType = "Release"
	case "relwithdebinfo":
		buildType = "RelWithDebInfo"
	case "minsizerel":
		buildType = "MinSizeRel"
	}
	args := []string{"-S", root, "-B", buildDir, "-G", "Ninja", "-DCMAKE_BUILD_TYPE=" + buildType, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"}
	args = append(args, cacheArgs...)
	if err := runWorkspaceCommand(ctx, cmakePath, root, env, args, progress); err != nil {
		return model.WorkspacePlan{}, fmt.Errorf("configure CMake workspace: %w", err)
	}
	return readWorkspacePlan(ctx, filepath.Join(buildDir, ".cmake", "api", "v1", "reply"), root, buildDir, profile)
}

// BuildWorkspace asks CMake to build the chosen goals in its generated Ninja graph.
func BuildWorkspace(ctx context.Context, root, buildDir string, jobs int, goals, env []string, progress func(string)) error {
	cmakePath, err := locateCMake()
	if err != nil {
		return err
	}
	args := []string{"--build", buildDir}
	if jobs > 0 {
		args = append(args, "--parallel", strconv.Itoa(jobs))
	}
	if len(goals) > 0 {
		args = append(args, "--target")
		args = append(args, goals...)
	}
	if err := runWorkspaceCommand(ctx, cmakePath, root, env, args, progress); err != nil {
		return fmt.Errorf("build CMake workspace: %w", err)
	}
	return nil
}

// TestWorkspace runs CTest against the generated build directory.
func TestWorkspace(ctx context.Context, root, buildDir, pattern string, env []string, progress func(string)) error {
	cmakePath, err := locateCMake()
	if err != nil {
		return err
	}
	ctestPath, found := locateCTest(cmakePath)
	if !found {
		return fmt.Errorf("ctest is required for CMake workspace tests")
	}
	args := []string{"--test-dir", buildDir, "--output-on-failure", "--no-tests=error"}
	if pattern != "" {
		args = append(args, "-R", pattern)
	}
	return runWorkspaceCommand(ctx, ctestPath, root, env, args, progress)
}

func runWorkspaceCommand(ctx context.Context, exe, root string, env, args []string, progress func(string)) error {
	command := workspaceCommand(ctx, exe, env, args)
	command.Dir = root
	return processx.Stream(ctx, command, progress)
}

// CheckWorkspaceTools reports the executables needed to configure a workspace.
func CheckWorkspaceTools() error {
	if _, err := locateCMake(); err != nil {
		return err
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		return fmt.Errorf("ninja is required for CMake workspaces: %w", err)
	}
	return nil
}

func readWorkspacePlan(ctx context.Context, replyDir, source, buildDir, profile string) (model.WorkspacePlan, error) {
	code, err := loadCodeModel(replyDir)
	if err != nil {
		return model.WorkspacePlan{}, err
	}
	if len(code.Configurations) != 1 {
		return model.WorkspacePlan{}, fmt.Errorf("expected one CMake configuration, got %d", len(code.Configurations))
	}
	cfg := code.Configurations[0]
	result := model.WorkspacePlan{
		Backend: "cmake", Project: filepath.Base(source), Profile: profile, SourceDir: source, BuildDir: buildDir,
		Members: make([]model.WorkspaceGroup, 0, len(cfg.Directories)),
		Targets: make([]model.WorkspaceTarget, 0, len(cfg.Targets)),
	}
	if len(cfg.Projects) > 0 {
		result.Project = cfg.Projects[0].Name
	}
	for _, dir := range cfg.Directories {
		name := dir.Build
		goal := ""
		if !filepath.IsAbs(filepath.FromSlash(name)) {
			goal = strings.TrimPrefix(name+"/all", "./")
		}
		result.Members = append(result.Members, model.WorkspaceGroup{Name: name, Source: dir.Source, Goal: goal})
	}
	owners := make(map[string]int, len(cfg.Targets))
	for _, ref := range cfg.Targets {
		if ref.Directory < 0 || ref.Directory >= len(result.Members) {
			return model.WorkspacePlan{}, fmt.Errorf("invalid CMake directory index for target %q", ref.Name)
		}
		owners[ref.ID] = ref.Directory
	}
	memberDeps := make(map[int]map[string]bool)
	for _, ref := range cfg.Targets {
		if err := ctx.Err(); err != nil {
			return model.WorkspacePlan{}, err
		}
		var object struct {
			Type      string `json:"type"`
			Artifacts []struct {
				Path string `json:"path"`
			} `json:"artifacts"`
			Dependencies []struct {
				ID string `json:"id"`
			} `json:"dependencies"`
		}
		if err := readJSON(filepath.Join(replyDir, ref.JSONFile), &object); err != nil {
			return model.WorkspacePlan{}, err
		}
		target := model.WorkspaceTarget{ID: ref.ID, Name: ref.Name, Type: object.Type, Member: result.Members[ref.Directory].Name}
		for _, artifact := range object.Artifacts {
			target.Artifacts = append(target.Artifacts, artifact.Path)
		}
		for _, dep := range object.Dependencies {
			member, ok := owners[dep.ID]
			if !ok {
				continue
			}
			target.DependsOn = append(target.DependsOn, dep.ID)
			if member != ref.Directory {
				if memberDeps[ref.Directory] == nil {
					memberDeps[ref.Directory] = make(map[string]bool)
				}
				memberDeps[ref.Directory][result.Members[member].Name] = true
			}
		}
		slices.Sort(target.DependsOn)
		result.Targets = append(result.Targets, target)
	}
	for idx, deps := range memberDeps {
		for dep := range deps {
			result.Members[idx].DependsOn = append(result.Members[idx].DependsOn, dep)
		}
		slices.Sort(result.Members[idx].DependsOn)
	}
	slices.SortFunc(result.Members, func(a, b model.WorkspaceGroup) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(result.Targets, func(a, b model.WorkspaceTarget) int { return strings.Compare(a.ID, b.ID) })
	return result, nil
}
