package app

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"trestle/internal/buildanalyzer"
	"trestle/internal/config"
	cmake "trestle/internal/importer/cmake"
	"trestle/internal/model"
)

func buildCMakeWorkspace(ctx context.Context, path string, wc *config.WorkspaceContext, options WorkspaceBuildOptions, progress func(string)) error {
	return runOperation(ctx, path, "build cmake workspace", progress, func(ctx context.Context, emit func(string)) error {
		return withWorkspacePlan(ctx, path, wc, options.Profile, emit, func(ctx context.Context, plan model.WorkspacePlan, env []string) error {
			goals, err := cmakeWorkspaceGoals(plan, options)
			if err != nil {
				return err
			}
			jobs := wc.RootConfig.Workspace.CMake.Jobs
			if override := buildanalyzer.Jobs(ctx); override > 0 {
				jobs = override
			}
			return cmake.BuildWorkspace(ctx, filepath.Dir(path), plan.BuildDir, jobs, goals, env, emit)
		})
	})
}

func cmakeWorkspaceGoals(plan model.WorkspacePlan, options WorkspaceBuildOptions) ([]string, error) {
	member, goals, err := SplitWorkspaceTargets(options.Targets)
	if err != nil {
		return nil, err
	}
	if options.Member != "" {
		if member != "" && member != options.Member {
			return nil, fmt.Errorf("E_WORKSPACE_INVALID: target selects member %q but -p selects %q", member, options.Member)
		}
		member = options.Member
	}
	if options.All && len(goals) > 0 {
		return nil, fmt.Errorf("use --all or explicit CMake targets")
	}
	if member != "" {
		idx := slices.IndexFunc(plan.Members, func(group model.WorkspaceGroup) bool { return group.Name == member })
		if idx < 0 {
			return nil, fmt.Errorf("E_WORKSPACE_INVALID: unknown CMake member %q; run trestle workspace plan to list members", member)
		}
		if len(goals) == 0 {
			if goal := plan.Members[idx].Goal; goal != "" {
				return []string{goal}, nil
			}
			return nil, fmt.Errorf("CMake member %q requires an explicit target", member)
		}
	}
	for _, goal := range goals {
		if strings.HasPrefix(goal, "-") {
			return nil, fmt.Errorf("invalid CMake build target %q", goal)
		}
		if member != "" && !slices.ContainsFunc(plan.Targets, func(target model.WorkspaceTarget) bool {
			return target.Name == goal && (member == "." || target.Member == member || strings.HasPrefix(target.Member, member+"/"))
		}) {
			return nil, fmt.Errorf("E_WORKSPACE_INVALID: member %q has no CMake target %q", member, goal)
		}
	}
	return goals, nil
}

func testCMakeWorkspace(ctx context.Context, path string, wc *config.WorkspaceContext, member string, selection TestSelection, progress func(string)) error {
	if member != "" || selection.Group != "" || selection.File != "" {
		return fmt.Errorf("CMake workspace tests accept -all or -job NAME")
	}
	pattern := ""
	if selection.Job != "" {
		pattern = "^" + regexp.QuoteMeta(selection.Job) + "$"
	}
	return runOperation(ctx, path, "test cmake workspace", progress, func(ctx context.Context, emit func(string)) error {
		return withWorkspacePlan(ctx, path, wc, "", emit, func(ctx context.Context, plan model.WorkspacePlan, env []string) error {
			root := filepath.Dir(path)
			if err := cmake.BuildWorkspace(ctx, root, plan.BuildDir, wc.RootConfig.Workspace.CMake.Jobs, nil, env, emit); err != nil {
				return err
			}
			return cmake.TestWorkspace(ctx, root, plan.BuildDir, pattern, env, emit)
		})
	})
}
