package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/graph"
	cmake "trestle/internal/importer/cmake"
	"trestle/internal/model"
	"trestle/internal/toolchain"
)

// WorkspaceInitOptions selects a CMake source tree and persistent cache settings.
type WorkspaceInitOptions struct {
	CMake    bool
	Source   string
	Cache    map[string]string
	Jobs     int
	Profile  string
	BuildDir string
}

// InitWorkspace discovers the project's members or attaches its CMake build graph.
func InitWorkspace(ctx context.Context, path string, options WorkspaceInitOptions, progress func(string)) error {
	return runOperation(ctx, path, "workspace init", progress, func(ctx context.Context, emit func(string)) error {
		expected, err := config.Snapshot(path)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(filepath.Dir(path))
		if err != nil {
			return err
		}
		cfg, err := config.Load(path)
		switch {
		case errors.Is(err, config.ErrNotFound):
			cfg = config.Default(filepath.Base(root))
			cfg.Targets = nil
			cfg.Build.DefaultTargets = nil
			cfg.Build.BuildDir = filepath.Join(root, cfg.Build.BuildDir)
			cfg.Build.CompileCommands = filepath.Join(root, cfg.Build.CompileCommands)
		case err != nil:
			return err
		}
		if options.BuildDir != "" {
			cfg.Build.BuildDir = options.BuildDir
			if !filepath.IsAbs(cfg.Build.BuildDir) {
				cfg.Build.BuildDir = filepath.Join(root, cfg.Build.BuildDir)
			}
		}

		changed := cfg.Workspace == nil || options.Source != "" || len(options.Cache) > 0 || options.Jobs != 0 || options.Profile != "" || options.BuildDir != ""
		wantsCMake := options.CMake || options.Source != "" || len(options.Cache) > 0 || options.Jobs != 0
		if _, err := cmake.WorkspaceCacheArgs(options.Cache); err != nil {
			return err
		}
		if cfg.Workspace == nil && !wantsCMake {
			members, err := config.DiscoverWorkspaceMembers(root, cfg.Build.BuildDir)
			if err != nil {
				return err
			}
			wantsCMake = len(members) == 0 && findCMakeSource(root) != ""
		}
		if wantsCMake || cfg.Workspace != nil && cfg.Workspace.CMake != nil {
			if len(cfg.Targets) > 0 || cfg.Workspace != nil && cfg.Workspace.CMake == nil {
				return fmt.Errorf("E_WORKSPACE_INVALID: %s already defines native targets or members", path)
			}
			if cfg.Workspace == nil {
				cfg.Workspace = &config.Workspace{CMake: &config.CMakeWorkspace{}}
			}
			settings := cfg.Workspace.CMake
			if options.Source != "" {
				settings.Source = options.Source
			}
			if settings.Source == "" {
				settings.Source = findCMakeSource(root)
			}
			if settings.Cache == nil {
				settings.Cache = make(map[string]string)
			}
			for name, value := range options.Cache {
				base, _, _ := strings.Cut(name, ":")
				for existing := range settings.Cache {
					if key, _, _ := strings.Cut(existing, ":"); key == base {
						delete(settings.Cache, existing)
					}
				}
				settings.Cache[name] = value
			}
			if options.Jobs != 0 {
				settings.Jobs = options.Jobs
			}
		} else if cfg.Workspace == nil {
			cfg.Workspace = &config.Workspace{Discover: true}
		}
		if options.Profile != "" {
			cfg.Build.Profile = options.Profile
		}
		if err := config.Validate(cfg); err != nil {
			return err
		}
		wc, err := config.ResolveWorkspaceMembers(path, cfg)
		if err != nil {
			return err
		}
		return withWorkspacePlan(ctx, path, wc, "", emit, func(_ context.Context, plan model.WorkspacePlan, _ []string) error {
			if !changed {
				return nil
			}
			if expected == "missing" && plan.Backend == "cmake" {
				cfg.Project.Name = plan.Project
			}
			return config.SaveExpected(path, cfg, expected)
		})
	})
}

func findCMakeSource(root string) string {
	for _, source := range []string{".", "llvm"} {
		info, err := os.Stat(filepath.Join(root, source, "CMakeLists.txt"))
		if err == nil && info.Mode().IsRegular() {
			return source
		}
	}
	return ""
}

// GenerateWorkspacePlan refreshes the orchestration for the requested profile.
func GenerateWorkspacePlan(ctx context.Context, path, profile string, progress func(string)) error {
	return runOperation(ctx, path, "workspace plan", progress, func(ctx context.Context, emit func(string)) error {
		wc, err := LoadWorkspace(ctx, path)
		if err != nil {
			return err
		}
		return withWorkspacePlan(ctx, path, wc, profile, emit, nil)
	})
}

func workspaceProfileDir(cfg config.Config, profile string) (string, string, error) {
	if profile == "" {
		profile = cfg.Build.Profile
	}
	if !filepath.IsLocal(profile) || profile == "." || strings.ContainsAny(profile, `/\:`) {
		return "", "", fmt.Errorf("invalid workspace build profile %q", profile)
	}
	return filepath.Join(cfg.Build.BuildDir, profile), profile, nil
}

func withWorkspacePlan(ctx context.Context, path string, wc *config.WorkspaceContext, profile string, progress func(string), action func(context.Context, model.WorkspacePlan, []string) error) error {
	if wc == nil {
		return fmt.Errorf("%s does not declare a [workspace] section", path)
	}
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	cfg := wc.RootConfig
	dir, profile, err := workspaceProfileDir(cfg, profile)
	if err != nil {
		return err
	}
	ctx, unlock, err := acquireResource(ctx, filepath.Join(dir, "workspace.json"), "workspace orchestration", false)
	if err != nil {
		return err
	}
	defer unlock()

	var plan model.WorkspacePlan
	var env []string
	if settings := cfg.Workspace.CMake; settings != nil {
		cache, err := cmake.WorkspaceCacheArgs(settings.Cache)
		if err != nil {
			return err
		}
		source := settings.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(root, source)
		}
		if setup := cfg.Toolchain.Setup; setup != "" {
			overrides := toolchain.EnvironmentForSetup(ctx, setup)
			if len(overrides) == 0 {
				return fmt.Errorf("could not load CMake toolchain environment from %s", setup)
			}
			env = append(os.Environ(), environmentList(overrides)...)
		}
		// Distinct cache arguments get distinct build trees, so removing a cache
		// variable cannot leave its old value active in a later configuration.
		key := fsx.Hash(append([]string{source, cfg.Toolchain.Setup, os.Getenv("CC"), os.Getenv("CXX")}, cache...))[:12]
		buildDir := filepath.Join(dir, "cmake-"+key)
		plan, err = cmake.ConfigureWorkspace(ctx, source, buildDir, profile, cache, env, progress)
		if err != nil {
			return err
		}
		if err := publishCMakeCompileCommands(ctx, buildDir, cfg.Build.CompileCommands); err != nil {
			return err
		}
	} else {
		plan, err = nativeWorkspacePlan(wc, root, dir, profile)
		if err != nil {
			return err
		}
	}
	if err := writeWorkspacePlan(dir, plan, progress); err != nil {
		return err
	}
	if action != nil {
		return action(ctx, plan, env)
	}
	return nil
}

func writeWorkspacePlan(dir string, plan model.WorkspacePlan, progress func(string)) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "workspace.json")
	if err := fsx.AtomicWrite(path, append(data, '\n')); err != nil {
		return err
	}
	if progress != nil {
		progress(fmt.Sprintf("Workspace plan: %s (%d members, %d targets)", path, len(plan.Members), len(plan.Targets)))
	}
	return nil
}

func nativeWorkspacePlan(wc *config.WorkspaceContext, root, dir, profile string) (model.WorkspacePlan, error) {
	order, err := graph.OrderWorkspaceMembers(wc)
	if err != nil {
		return model.WorkspacePlan{}, err
	}
	result := model.WorkspacePlan{
		Backend: "trestle", Project: wc.RootConfig.Project.Name, Profile: profile, SourceDir: root, BuildDir: dir,
		Members: make([]model.WorkspaceGroup, 0, len(order)), Targets: []model.WorkspaceTarget{},
	}
	for _, member := range order {
		cfg := wc.Member(member)
		source := member
		if member == graph.RootMemberName {
			cfg, source = &wc.RootConfig, "."
		}
		group := model.WorkspaceGroup{Name: member, Source: source}
		for _, name := range sortedTargetNames(cfg.Targets) {
			target := cfg.Targets[name]
			entry := model.WorkspaceTarget{ID: name, Name: name, Type: target.Type, Member: member}
			for _, dep := range target.Dependencies {
				if provider, targetID, qualified := strings.Cut(dep.Target, ":"); qualified {
					provider = strings.TrimSpace(provider)
					if provider != member && !slices.Contains(group.DependsOn, provider) {
						group.DependsOn = append(group.DependsOn, provider)
					}
					entry.DependsOn = append(entry.DependsOn, strings.TrimSpace(targetID))
				} else if dep.Target != "" {
					entry.DependsOn = append(entry.DependsOn, dep.Target)
				}
			}
			slices.Sort(entry.DependsOn)
			result.Targets = append(result.Targets, entry)
		}
		slices.Sort(group.DependsOn)
		result.Members = append(result.Members, group)
	}
	return result, nil
}

func publishCMakeCompileCommands(ctx context.Context, buildDir, destination string) error {
	source := filepath.Join(buildDir, "compile_commands.json")
	if destination == "" || destination == source {
		return nil
	}
	data, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		// CMake omits the database for projects without compiled sources.
		return nil
	}
	if err != nil {
		return err
	}
	lock, err := fsx.WaitLock(ctx, destination, "publish CMake compile commands")
	if err != nil {
		return err
	}
	defer lock.Close()
	return fsx.AtomicWrite(destination, data)
}
