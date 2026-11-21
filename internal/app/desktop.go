package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/graph"
	"trestle/internal/model"
	"trestle/internal/policy"
)

type ProjectSnapshot struct {
	ID       string          `json:"id"`
	Path     string          `json:"path"`
	Name     string          `json:"name"`
	Profile  string          `json:"profile"`
	Compiler string          `json:"compiler"`
	Projects []ProjectMember `json:"projects"`
	Targets  []ProjectTarget `json:"targets"`
}

type ProjectMember struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Profile  string `json:"profile"`
	Compiler string `json:"compiler"`
}

type ProjectTarget struct {
	ID           model.TargetID   `json:"id"`
	Member       string           `json:"member"`
	Type         model.TargetType `json:"type"`
	Sources      []string         `json:"sources,omitempty"`
	Dependencies []model.TargetID `json:"dependencies,omitempty"`
	Packages     []string         `json:"packages,omitempty"`
	Compiler     string           `json:"compiler"`
	CompileFlags []string         `json:"compile_flags,omitempty"`
	LinkFlags    []string         `json:"link_flags,omitempty"`
	OutputName   string           `json:"output_name,omitempty"`
}

type ProjectOperation struct {
	Member  string   `json:"member,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Profile string   `json:"profile,omitempty"`
	All     bool     `json:"all,omitempty"`
	Rebuild bool     `json:"rebuild,omitempty"`
}

// ProjectPath validates an existing project. Opening a directory never creates
// or modifies its configuration.
func ProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("project path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		path = filepath.Join(path, config.DefaultFileName)
	}
	return fsx.Canonical(path)
}

func InspectProject(ctx context.Context, path string) (ProjectSnapshot, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return ProjectSnapshot{}, err
	}
	result := ProjectSnapshot{
		ID: path, Path: cfg.Root(), Name: cfg.Project.Name, Profile: cfg.Build.Profile,
		Compiler: cfg.Toolchain.CXX, Projects: []ProjectMember{}, Targets: []ProjectTarget{},
	}
	appendProject := func(name string, cfg config.Config, targets []model.ResolvedTarget) {
		result.Projects = append(result.Projects, ProjectMember{
			Name: name, Path: cfg.Root(), Profile: cfg.Build.Profile, Compiler: cfg.Toolchain.CXX,
		})
		for _, target := range targets {
			details := ProjectTarget{
				ID: target.ID, Member: name, Type: target.Type, Sources: target.Sources,
				Compiler: cfg.Toolchain.CXX, OutputName: target.OutputName,
				CompileFlags: append(append([]string{}, cfg.Build.CompileFlags...), target.CompileSelf.Options...),
				LinkFlags:    append(append([]string{}, cfg.Build.LinkFlags...), target.LinkSelf.Options...),
			}
			for _, dep := range target.Implementation {
				if dep.Ref.Target != "" {
					details.Dependencies = append(details.Dependencies, dep.Ref.Target)
				}
				if dep.Ref.Package != "" {
					details.Packages = append(details.Packages, dep.Ref.Package)
				}
			}
			result.Targets = append(result.Targets, details)
		}
	}
	if cfg.Workspace == nil {
		cfg, err = policy.ApplyAt(ctx, cfg, cfg.Root())
		if err != nil {
			return ProjectSnapshot{}, err
		}
		project, err := graph.ResolveAt(ctx, cfg, cfg.Root())
		if err != nil {
			return ProjectSnapshot{}, err
		}
		appendProject("", cfg, project.Targets)
		return result, nil
	}
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return ProjectSnapshot{}, err
	}
	ws, err := graph.ResolveWorkspace(ctx, wc, wc.RootConfig.Build.BuildDir)
	if err != nil {
		return ProjectSnapshot{}, err
	}
	for _, member := range ws.Members {
		memberConfig := wc.Member(member.Name)
		if member.Name == graph.RootMemberName {
			memberConfig = &wc.RootConfig
		}
		appendProject(member.Name, *memberConfig, member.Project.Targets)
	}
	return result, nil
}

// RunProjectOperation calls the same Ninja and manifest generation paths as the
// terminal frontends. Selecting a desktop profile applies to this run only.
type rebuildProjectKey struct{}

func RunProjectOperation(ctx context.Context, path string, options ProjectOperation, configure bool, emit func(string)) error {
	if options.Rebuild && !configure {
		ctx = context.WithValue(ctx, rebuildProjectKey{}, true)
	}
	if options.Profile != "" && options.Profile != "debug" && options.Profile != "release" {
		return fmt.Errorf("profile must be debug or release")
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if cfg.Workspace == nil {
		if options.Member != "" {
			return fmt.Errorf("standalone project has no member %q", options.Member)
		}
		if options.Profile != "" {
			cfg.Build.Profile = options.Profile
		}
		cfg, err = policy.ApplyAt(ctx, cfg, cfg.Root())
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, configurationKey{}, cfg)
		if configure {
			return configureManifest(ctx, path, emit)
		}
		return BuildTargetsWithOptions(ctx, path, options.Targets, options.All, false, emit)
	}
	if !configure {
		return BuildWorkspace(ctx, path, WorkspaceBuildOptions{
			Member: options.Member, Targets: options.Targets, Profile: options.Profile, All: options.All,
		}, emit)
	}
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if options.Profile != "" {
		wc.RootConfig.Build.Profile = options.Profile
		for _, member := range wc.Members {
			member.Build.Profile = options.Profile
		}
	}
	buildRoot := wc.RootConfig.Build.BuildDir
	ws, err := graph.ResolveWorkspace(ctx, wc, buildRoot)
	if err != nil {
		return err
	}
	if options.Member != "" {
		if _, ok := ws.Member(options.Member); !ok {
			return fmt.Errorf("unknown workspace member %q", options.Member)
		}
	}
	for _, name := range workspaceBuildMembers(ws, options.Member) {
		memberCtx, err := workspaceMemberContext(ctx, wc, name, options.Profile, ws)
		if err != nil {
			return err
		}
		if err := configureManifest(memberCtx, workspaceMemberPath(wc, name), emit); err != nil {
			return err
		}
	}
	return publishWorkspaceCompileCommands(ctx, wc, buildRoot)
}

func configureManifest(ctx context.Context, path string, emit func(string)) error {
	return runOperation(ctx, path, "generate", emit, func(ctx context.Context, emit func(string)) error {
		result, err := GenerateWithContext(ctx, path)
		if err == nil {
			emit("Generated " + result.Manifest)
		}
		return err
	})
}

type ProjectToolchain struct {
	Kind    string `json:"kind"`
	CC      string `json:"cc"`
	CXX     string `json:"cxx"`
	Version string `json:"version"`
	Target  string `json:"target"`
}

func InspectToolchain(ctx context.Context, path, member string) (ProjectToolchain, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return ProjectToolchain{}, err
	}
	if member != "" && member != graph.RootMemberName {
		wc, err := LoadWorkspace(ctx, path)
		if err != nil {
			return ProjectToolchain{}, err
		}
		cfgAtMember := wc.Member(member)
		if cfgAtMember == nil {
			return ProjectToolchain{}, fmt.Errorf("unknown workspace member %q", member)
		}
		cfg = *cfgAtMember
	}
	tc, err := detectConfigured(ctx, cfg)
	return ProjectToolchain{Kind: string(tc.Kind), CC: tc.CC, CXX: tc.CXX, Version: tc.Version, Target: tc.Target}, err
}
