package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/fsx"
	"trestle/internal/model"
)

func Resolve(cfg config.Config) (model.ResolvedProject, error) {
	return ResolveAt(context.Background(), cfg, ".")
}

func ResolveAt(ctx context.Context, cfg config.Config, root string) (model.ResolvedProject, error) {
	names := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	project := model.ResolvedProject{
		Name:        cfg.Project.Name,
		ByID:        make(map[model.TargetID]model.ResolvedTarget, len(names)),
		LinkClosure: make(map[model.TargetID][]model.TargetID, len(names)),
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return model.ResolvedProject{}, err
		}
		target := cfg.Targets[name]
		patterns := make([]string, len(target.Sources))
		for i, pattern := range target.Sources {
			patterns[i] = pattern
			if !filepath.IsAbs(pattern) {
				patterns[i] = filepath.Join(root, pattern)
			}
		}
		source, err := fsx.ResolveSources(patterns)
		if err != nil {
			return model.ResolvedProject{}, fmt.Errorf("target %q source glob: %w", name, err)
		}
		if len(source) == 0 {
			return model.ResolvedProject{}, fmt.Errorf("E_CONFIG_MISSING_REQUIRED: target %q source pattern matched no files", name)
		}
		resolved := model.ResolvedTarget{
			Target: model.Target{
				ID:               model.TargetID(name),
				Type:             model.TargetType(target.Type),
				Sources:          source,
				OutputName:       target.OutputName,
				CStandard:        target.CStandard,
				CXXStandard:      target.CXXStandard,
				CFlags:           target.CFlags,
				CXXFlags:         target.CXXFlags,
				ExportAllSymbols: target.ExportAllSymbols,
				ShaderStage:      target.ShaderStage,
				ShaderEntry:      target.ShaderEntry,
				ShaderTool:       target.ShaderTool,
				ShaderTargetEnv:  target.ShaderTargetEnv,
				Private: model.Usage{Compile: model.CompileUsage{
					IncludeDirs: target.PrivateIncludeDirs,
					Defines:     target.PrivateDefines,
					Options:     target.CompileOptions,
				}, Link: model.LinkUsage{
					LibraryDirs: target.LibraryDirs,
					Items:       linkItems(target.Libraries),
					Options:     target.LinkOptions,
				}},
				Public: model.Usage{Compile: model.CompileUsage{
					IncludeDirs: target.IncludeDirs,
					Defines:     target.Defines,
				}},
				Interface: model.Usage{
					Compile: model.CompileUsage{Options: target.InterfaceCompile},
					Link:    model.LinkUsage{Options: target.InterfaceLink},
				},
			},
		}
		for _, dep := range target.Dependencies {
			ref := model.DependencyRef{Package: dep.Package}
			if dep.Target != "" {
				ref.Target = model.TargetID(dep.Target)
			}
			if dep.Package != "" {
				usage, err := resolvePackage(ctx, cfg, cfg.Packages[dep.Package])
				if err != nil {
					return model.ResolvedProject{}, err
				}
				switch model.DependencyScope(dep.Scope) {
				case model.Private:
					resolved.Private = mergeUsage(resolved.Private, usage)
				case model.Public:
					resolved.Public = mergeUsage(resolved.Public, usage)
				case model.Interface:
					resolved.Interface = mergeUsage(resolved.Interface, usage)
				}
			}
			resolved.Implementation = append(resolved.Implementation, model.Dependency{Ref: ref, Scope: model.DependencyScope(dep.Scope)})
		}
		project.Targets = append(project.Targets, resolved)
		project.ByID[resolved.ID] = resolved
	}
	for i := range project.Targets {
		if err := resolveTarget(&project, project.Targets[i].ID); err != nil {
			return model.ResolvedProject{}, err
		}
	}
	return project, nil
}

func linkItems(names []string) []model.LinkItem {
	items := make([]model.LinkItem, 0, len(names))
	for _, name := range names {
		items = append(items, model.LinkItem{Kind: "library", Name: name})
	}
	return items
}

func resolveTarget(project *model.ResolvedProject, id model.TargetID) error {
	visited := map[model.TargetID]bool{}
	var resolve func(model.TargetID) (model.Usage, error)
	resolve = func(current model.TargetID) (model.Usage, error) {
		if visited[current] {
			return model.Usage{}, nil
		}
		visited[current] = true
		defer delete(visited, current)
		target := project.ByID[current]
		usage := model.Usage{
			Compile: mergeCompile(target.Public.Compile, target.Interface.Compile),
			Link:    mergeLink(target.Public.Link, target.Interface.Link),
		}
		for _, dep := range target.Implementation {
			include := dep.Scope == model.Private || dep.Scope == model.Public
			export := dep.Scope == model.Public || dep.Scope == model.Interface
			if dep.Ref.Package != "" {
				if include {
					usage.Compile = mergeCompile(usage.Compile, project.ByID[current].Private.Compile)
				}
				continue
			}
			dependency := project.ByID[dep.Ref.Target]
			if include {
				dependencyUsage, err := resolve(dep.Ref.Target)
				if err != nil {
					return model.Usage{}, err
				}
				usage.Compile = mergeCompile(usage.Compile, dependencyUsage.Compile)
				usage.Link = mergeLink(usage.Link, dependencyUsage.Link)
			}
			if export {
				usage.Compile = mergeCompile(usage.Compile, dependency.Interface.Compile)
				usage.Link = mergeLink(usage.Link, dependency.Interface.Link)
			}
		}
		return usage, nil
	}
	interfaceUsage, err := resolve(id)
	if err != nil {
		return err
	}
	target := project.ByID[id]
	target.CompileSelf = mergeCompile(target.Private.Compile, interfaceUsage.Compile)
	target.CompileInterface = interfaceUsage.Compile
	target.LinkSelf = mergeLink(target.Private.Link, target.Interface.Link)
	target.LinkInterface = interfaceUsage.Link
	project.ByID[id] = target
	for i := range project.Targets {
		if project.Targets[i].ID == id {
			project.Targets[i] = target
		}
	}
	var closure func(model.TargetID) []model.TargetID
	closure = func(current model.TargetID) []model.TargetID {
		var result []model.TargetID
		seen := map[model.TargetID]bool{}
		var walk func(model.TargetID)
		walk = func(node model.TargetID) {
			if seen[node] {
				return
			}
			seen[node] = true
			for _, dep := range project.ByID[node].Implementation {
				if dep.Ref.Target != "" && dep.Scope != model.Interface {
					walk(dep.Ref.Target)
				}
			}
			if node != current {
				result = append(result, node)
			}
		}
		walk(current)
		return result
	}
	project.LinkClosure[id] = closure(id)
	return nil
}

func resolvePackage(ctx context.Context, cfg config.Config, pkg config.Package) (model.Usage, error) {
	if pkg.Triplet == "" || pkg.Triplet == "auto" {
		pkg.Triplet = cfg.Vcpkg.Triplet
	}
	resolved, err := (vcpkg.Resolver{Root: cfg.Vcpkg.Root, Profile: cfg.Build.Profile, CRTLinkage: cfg.Vcpkg.CRTLinkage, LibraryLinkage: cfg.Vcpkg.LibraryLinkage}).Resolve(ctx, pkg)
	if err != nil {
		return model.Usage{}, fmt.Errorf("package %s: %w", pkg.Port, err)
	}
	return resolved.Usage, nil
}

func mergeUsage(left, right model.Usage) model.Usage {
	left.Compile = mergeCompile(left.Compile, right.Compile)
	left.Link = mergeLink(left.Link, right.Link)
	return left
}

func mergeCompile(values ...model.CompileUsage) model.CompileUsage {
	var result model.CompileUsage
	includeSeen := map[string]bool{}
	defineSeen := map[string]bool{}
	optionSeen := map[string]bool{}
	for _, value := range values {
		for _, item := range value.IncludeDirs {
			if key := clean(item); !includeSeen[key] {
				includeSeen[key] = true
				result.IncludeDirs = append(result.IncludeDirs, clean(item))
			}
		}
		for _, item := range value.Defines {
			if !defineSeen[item] {
				defineSeen[item] = true
				result.Defines = append(result.Defines, item)
			}
		}
		for _, item := range value.Options {
			if !optionSeen[item] {
				optionSeen[item] = true
				result.Options = append(result.Options, item)
			}
		}
	}
	return result
}

func mergeLink(values ...model.LinkUsage) model.LinkUsage {
	var result model.LinkUsage
	dirSeen := map[string]bool{}
	optionSeen := map[string]bool{}
	runtimeSeen := map[string]bool{}
	for _, value := range values {
		for _, item := range value.LibraryDirs {
			if !dirSeen[clean(item)] {
				dirSeen[clean(item)] = true
				result.LibraryDirs = append(result.LibraryDirs, clean(item))
			}
		}
		for _, item := range value.Items {
			result.Items = append(result.Items, item)
		}
		for _, item := range value.Options {
			if !optionSeen[item] {
				optionSeen[item] = true
				result.Options = append(result.Options, item)
			}
		}
		for _, item := range value.RuntimeFiles {
			if !runtimeSeen[item] {
				runtimeSeen[item] = true
				result.RuntimeFiles = append(result.RuntimeFiles, item)
			}
		}
	}
	return result
}

func clean(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		return abs
	}
	return filepath.Clean(strings.TrimSpace(path))
}
