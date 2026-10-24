package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/config"
	"trestle/internal/model"
)

// RootMemberName is the member name used for targets declared by the workspace
// root itself.
const RootMemberName = "root"

// ResolveWorkspace resolves every member of a workspace into one workspace-wide
// project. Each member is resolved with the existing single-project resolver
// against its own directory, and receives its own build directory at
// buildDir/<profile>/<member> so artifacts from different members never collide.
//
// Cross-member dependencies use the extended dependency syntax: a target field of
// the form "member:target" refers to a target declared by another member. The
// referenced target's exported usage is folded into the consumer exactly as a
// local dependency would be, the consumer's dependency reference is rewritten to
// the (globally unique) target ID, and a member-level edge is recorded so the
// workspace build order puts the provider first.
func ResolveWorkspace(ctx context.Context, wc *config.WorkspaceContext, buildDir string) (model.WorkspaceProject, error) {
	if wc == nil {
		return model.WorkspaceProject{}, fmt.Errorf("E_WORKSPACE_INVALID: no workspace context to resolve")
	}
	configs, order, err := workspaceConfigs(wc)
	if err != nil {
		return model.WorkspaceProject{}, err
	}
	// Usage paths are resolved against each member's directory below. The
	// resolver clean()s relative paths against the process working directory,
	// which is the workspace root rather than the member, so rebase them first.
	for name, cfg := range configs {
		rebaseConfigPaths(&cfg, cfg.Root())
		configs[name] = cfg
	}
	owner, err := workspaceOwners(configs, order)
	if err != nil {
		return model.WorkspaceProject{}, err
	}

	resolved := make(map[string]model.ResolvedProject, len(order))
	for _, name := range order {
		if err := ctx.Err(); err != nil {
			return model.WorkspaceProject{}, err
		}
		cfg := configs[name]
		project, err := ResolveAt(ctx, cfg, cfg.Root())
		if err != nil {
			return model.WorkspaceProject{}, fmt.Errorf("workspace member %s: %w", name, err)
		}
		resolved[name] = project
	}

	edges, err := linkMembers(configs, order, owner, resolved, buildDir)
	if err != nil {
		return model.WorkspaceProject{}, err
	}
	buildOrder, err := orderMembers(order, edges)
	if err != nil {
		return model.WorkspaceProject{}, err
	}

	result := model.WorkspaceProject{
		Order:    buildOrder,
		ByID:     map[model.TargetID]model.ResolvedTarget{},
		MemberOf: map[model.TargetID]string{},
	}
	for _, name := range order {
		cfg := configs[name]
		memberProject := resolved[name]
		result.Members = append(result.Members, model.MemberProject{
			Name:      name,
			Root:      cfg.Root(),
			BuildDir:  memberBuildDir(cfg, buildDir, name),
			Project:   memberProject,
			DependsOn: sortedKeys(edges[name]),
		})
		for _, target := range memberProject.Targets {
			result.Targets = append(result.Targets, target)
			result.ByID[target.ID] = target
			result.MemberOf[target.ID] = name
		}
	}
	return result, nil
}

// OrderWorkspaceMembers returns the member names ordered so that providers come
// before their consumers. It only reads the configurations, so it is cheap
// enough for build orchestration that does not need a resolved graph.
func OrderWorkspaceMembers(wc *config.WorkspaceContext) ([]string, error) {
	if wc == nil {
		return nil, fmt.Errorf("E_WORKSPACE_INVALID: no workspace context")
	}
	configs, order, err := workspaceConfigs(wc)
	if err != nil {
		return nil, err
	}
	owners, err := workspaceOwners(configs, order)
	if err != nil {
		return nil, err
	}
	edges, err := workspaceEdges(configs, order, owners)
	if err != nil {
		return nil, err
	}
	return orderMembers(order, edges)
}

// rebaseConfigPaths rewrites the member-relative path lists in cfg onto root,
// so they resolve against the member's directory instead of the process working
// directory (which is the workspace root when a workspace builds).
func rebaseConfigPaths(cfg *config.Config, root string) {
	rebase := func(paths []string) []string {
		if len(paths) == 0 {
			return paths
		}
		result := make([]string, len(paths))
		for i, path := range paths {
			if path == "" || filepath.IsAbs(path) {
				result[i] = path
				continue
			}
			result[i] = filepath.Join(root, path)
		}
		return result
	}
	for name, target := range cfg.Targets {
		target.IncludeDirs = rebase(target.IncludeDirs)
		target.PrivateIncludeDirs = rebase(target.PrivateIncludeDirs)
		target.LibraryDirs = rebase(target.LibraryDirs)
		if target.TestWorkingDir != "" && !filepath.IsAbs(target.TestWorkingDir) {
			target.TestWorkingDir = filepath.Join(root, target.TestWorkingDir)
		}
		cfg.Targets[name] = target
	}
	for name, pkg := range cfg.Packages {
		pkg.IncludeDirs = rebase(pkg.IncludeDirs)
		pkg.LibraryDirs = rebase(pkg.LibraryDirs)
		pkg.RuntimeFiles = rebase(pkg.RuntimeFiles)
		cfg.Packages[name] = pkg
	}
}

// workspaceConfigs flattens the workspace into ordered configurations, adding
// the root itself when it declares targets of its own.
func workspaceConfigs(wc *config.WorkspaceContext) (map[string]config.Config, []string, error) {
	configs := make(map[string]config.Config, len(wc.MemberOrder)+1)
	order := make([]string, 0, len(wc.MemberOrder)+1)
	for _, name := range wc.MemberOrder {
		member := wc.Member(name)
		if member == nil {
			return nil, nil, fmt.Errorf("E_WORKSPACE_INVALID: member %q has no configuration", name)
		}
		if name == RootMemberName {
			return nil, nil, fmt.Errorf("E_WORKSPACE_INVALID: member name %q is reserved for workspace root targets", RootMemberName)
		}
		configs[name] = *member
		order = append(order, name)
	}
	if len(wc.RootConfig.Targets) > 0 {
		configs[RootMemberName] = wc.RootConfig
		order = append(order, RootMemberName)
	}
	return configs, order, nil
}

// workspaceOwners indexes which member declares each target and rejects
// duplicate IDs that slipped past configuration validation.
func workspaceOwners(configs map[string]config.Config, order []string) (map[model.TargetID]string, error) {
	owners := map[model.TargetID]string{}
	for _, name := range order {
		for id := range configs[name].Targets {
			if existing, taken := owners[model.TargetID(id)]; taken {
				return nil, fmt.Errorf("E_WORKSPACE_INVALID: target %q is declared by both members %s and %s", id, existing, name)
			}
			owners[model.TargetID(id)] = name
		}
	}
	return owners, nil
}

// workspaceEdges scans the member configurations for cross-member dependency
// references and returns the consumer -> provider edges they create.
func workspaceEdges(configs map[string]config.Config, order []string, owners map[model.TargetID]string) (map[string]map[string]bool, error) {
	edges := map[string]map[string]bool{}
	for _, name := range order {
		edges[name] = map[string]bool{}
		for _, target := range configs[name].Targets {
			for _, dep := range target.Dependencies {
				provider, _, cross, err := parseCrossMemberRef(name, dep.Target, owners)
				if err != nil {
					return nil, err
				}
				if cross {
					edges[name][provider] = true
				}
			}
		}
	}
	return edges, nil
}

// linkMembers rewrites cross-member dependency references and folds the provider
// target's usage into its consumers. It returns the member-level dependency
// edges discovered (consumer -> provider). Members are linked in dependency
// order, so a provider's own cross-member usage is already folded in when its
// consumer borrows it.
func linkMembers(configs map[string]config.Config, order []string, owners map[model.TargetID]string, resolved map[string]model.ResolvedProject, buildDir string) (map[string]map[string]bool, error) {
	edges, err := workspaceEdges(configs, order, owners)
	if err != nil {
		return nil, err
	}
	topological, err := orderMembers(order, edges)
	if err != nil {
		return nil, err
	}
	for _, name := range topological {
		project := resolved[name]
		for targetName, targetCfg := range configs[name].Targets {
			dependsOn := false
			for _, dep := range targetCfg.Dependencies {
				provider, dependencyID, cross, err := parseCrossMemberRef(name, dep.Target, owners)
				if err != nil {
					return nil, err
				}
				if provider == "" {
					continue
				}
				if !cross {
					rewriteDependency(&project, model.TargetID(targetName), provider, dependencyID)
					dependsOn = true
					continue
				}
				dependsOn = true
				if err := injectCrossMember(name, provider, dependencyID, resolved, configs[provider].Root()); err != nil {
					return nil, err
				}
				rewriteDependency(&project, model.TargetID(targetName), provider, dependencyID)
				addCrossMemberLink(&project, model.TargetID(targetName), dependencyID, resolved, configs, buildDir)
			}
			if dependsOn {
				resolved[name] = project
			}
		}
	}
	// Usage and link closures must be recomputed after the cross-member
	// references were materialized.
	for name, project := range resolved {
		for i := range project.Targets {
			if err := resolveTarget(&project, project.Targets[i].ID); err != nil {
				return nil, fmt.Errorf("workspace member %s: %w", name, err)
			}
		}
		resolved[name] = project
	}
	return edges, nil
}

// parseCrossMemberRef splits a "member:target" dependency reference. A plain
// target name (or package dependency) is not cross-member. When the member
// prefix names the declaring member itself the reference is treated as local.
func parseCrossMemberRef(self, ref string, owners map[model.TargetID]string) (provider, target string, cross bool, err error) {
	if ref == "" || !strings.Contains(ref, ":") {
		return "", "", false, nil
	}
	parts := strings.SplitN(ref, ":", 2)
	member, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if member == "" || name == "" {
		return "", "", false, fmt.Errorf("E_WORKSPACE_INVALID: dependency target %q must use member:target syntax", ref)
	}
	owner, ok := owners[model.TargetID(name)]
	if !ok {
		return "", "", false, fmt.Errorf("E_WORKSPACE_INVALID: dependency target %q names an unknown target", ref)
	}
	if owner != member {
		return "", "", false, fmt.Errorf("E_WORKSPACE_INVALID: dependency target %q: target %q is declared by member %q, not %q", ref, name, owner, member)
	}
	if member == self {
		return member, name, false, nil
	}
	return member, name, true, nil
}

// injectCrossMember makes the provider's resolved targets addressable from the
// consumer's graph, so the existing usage resolution walks them like local
// dependencies. Provider dependencies are injected transitively, and
// provider-relative usage paths are rebased onto the provider's root so the
// consumer's compiler sees the same directories the provider declares.
func injectCrossMember(consumer, provider string, target string, resolved map[string]model.ResolvedProject, providerRoot string) error {
	project, ok := resolved[consumer]
	if !ok {
		return fmt.Errorf("E_WORKSPACE_INVALID: member %q is not resolved", consumer)
	}
	source, ok := resolved[provider]
	if !ok {
		return fmt.Errorf("E_WORKSPACE_INVALID: dependency member %q is not resolved", provider)
	}
	var inject func(id model.TargetID)
	inject = func(id model.TargetID) {
		if _, exists := project.ByID[id]; exists {
			return
		}
		dependency, ok := source.ByID[id]
		if !ok {
			return
		}
		dependency = rebaseTargetPaths(dependency, providerRoot)
		project.ByID[id] = dependency
		for _, nested := range dependency.Implementation {
			if nested.Ref.Target != "" {
				inject(nested.Ref.Target)
			}
		}
	}
	inject(model.TargetID(target))
	resolved[consumer] = project
	return nil
}

// rebaseTargetPaths rewrites the relative path lists of a target's usage onto
// root, so usage borrowed from another member resolves against that member's
// directory instead of the consumer's.
func rebaseTargetPaths(target model.ResolvedTarget, root string) model.ResolvedTarget {
	rebase := func(paths []string) []string {
		if len(paths) == 0 {
			return paths
		}
		result := make([]string, len(paths))
		for i, path := range paths {
			if path == "" || filepath.IsAbs(path) {
				result[i] = path
				continue
			}
			result[i] = filepath.Join(root, path)
		}
		return result
	}
	rebaseLink := func(usage *model.LinkUsage) {
		usage.LibraryDirs = rebase(usage.LibraryDirs)
		usage.RuntimeFiles = rebase(usage.RuntimeFiles)
		for i := range usage.Items {
			if usage.Items[i].Path != "" && !filepath.IsAbs(usage.Items[i].Path) {
				usage.Items[i].Path = filepath.Join(root, usage.Items[i].Path)
			}
		}
	}
	for _, usage := range []*model.Usage{&target.Private, &target.Public, &target.Interface} {
		usage.Compile.IncludeDirs = rebase(usage.Compile.IncludeDirs)
		rebaseLink(&usage.Link)
	}
	return target
}

// rewriteDependency points one target's dependency reference at the provider's
// globally unique target ID when it still carries the member prefix.
func rewriteDependency(project *model.ResolvedProject, consumer model.TargetID, provider, dependency string) {
	prefixed := provider + ":" + dependency
	local := model.TargetID(dependency)
	rewrite := func(dependencies []model.Dependency) {
		for i := range dependencies {
			if string(dependencies[i].Ref.Target) == prefixed {
				dependencies[i].Ref.Target = local
			}
		}
	}
	for i := range project.Targets {
		if project.Targets[i].ID == consumer {
			rewrite(project.Targets[i].Implementation)
		}
	}
	if target, ok := project.ByID[consumer]; ok {
		rewrite(target.Implementation)
		project.ByID[consumer] = target
	}
}

// addCrossMemberLink marks the provider's library artifacts as link inputs of
// the consumer, walking the provider's own dependencies so transitively borrowed
// libraries are linked too. Member graphs are emitted separately, so the
// concrete artifact paths are filled in during generation
// (Options.WorkspaceOutputs) once the toolchain's artifact extension is known;
// the provider's bin directories stay as the name-based fallback.
func addCrossMemberLink(project *model.ResolvedProject, consumer model.TargetID, dependency string, resolved map[string]model.ResolvedProject, configs map[string]config.Config, buildDir string) {
	owner := dependencyOwner(model.TargetID(dependency), resolved)
	source, ok := resolved[owner]
	if !ok {
		return
	}
	target, ok := source.ByID[model.TargetID(dependency)]
	if !ok {
		return
	}
	seen := map[model.TargetID]bool{}
	var collect func(current model.ResolvedTarget)
	collect = func(current model.ResolvedTarget) {
		if seen[current.ID] {
			return
		}
		seen[current.ID] = true
		if current.Type == model.StaticLibrary || current.Type == model.SharedLibrary {
			addLinkItem(project, consumer, string(current.ID))
			if owner := dependencyOwner(current.ID, resolved); owner != "" {
				if cfg, ok := configs[owner]; ok {
					addLibraryDir(project, consumer, filepath.Join(memberBuildDir(cfg, buildDir, owner), "bin"))
				}
			}
		}
		for _, nested := range current.Implementation {
			if nested.Ref.Target == "" || nested.Scope == model.Interface {
				continue
			}
			if next, ok := source.ByID[nested.Ref.Target]; ok {
				collect(next)
			}
		}
	}
	collect(target)
}

// dependencyOwner finds which member's resolved project declares id.
func dependencyOwner(id model.TargetID, resolved map[string]model.ResolvedProject) string {
	for name, project := range resolved {
		for _, target := range project.Targets {
			if target.ID == id {
				return name
			}
		}
	}
	return ""
}

// addLinkItem appends one workspace link item to consumer's private usage.
func addLinkItem(project *model.ResolvedProject, consumer model.TargetID, dependency string) {
	update := func(target *model.ResolvedTarget) {
		target.Private.Link.Items = append(target.Private.Link.Items, model.LinkItem{
			Kind: "workspace",
			Name: dependency,
		})
	}
	for i := range project.Targets {
		if project.Targets[i].ID == consumer {
			update(&project.Targets[i])
		}
	}
	if target, ok := project.ByID[consumer]; ok {
		update(&target)
		project.ByID[consumer] = target
	}
}

// addLibraryDir records a provider's bin directory as the name-based link
// fallback for the consumer.
func addLibraryDir(project *model.ResolvedProject, consumer model.TargetID, dir string) {
	update := func(target *model.ResolvedTarget) {
		target.Private.Link.LibraryDirs = append(target.Private.Link.LibraryDirs, dir)
	}
	for i := range project.Targets {
		if project.Targets[i].ID == consumer {
			update(&project.Targets[i])
		}
	}
	if target, ok := project.ByID[consumer]; ok {
		update(&target)
		project.ByID[consumer] = target
	}
}

// memberBuildDir returns the per-member build directory
// <buildDir>/<profile>/<member>; root targets build directly under
// <buildDir>/<profile>.
func memberBuildDir(cfg config.Config, buildDir, name string) string {
	profile := cfg.Build.Profile
	if profile == "" {
		profile = "debug"
	}
	if name == RootMemberName {
		return filepath.Join(buildDir, profile)
	}
	return filepath.Join(buildDir, profile, name)
}

// orderMembers returns the member names with providers before consumers. Ties
// keep declaration order so single-member workspaces are unchanged.
func orderMembers(order []string, edges map[string]map[string]bool) ([]string, error) {
	index := make(map[string]int, len(order))
	for position, name := range order {
		index[name] = position
	}
	remaining := make(map[string]map[string]bool, len(order))
	for _, name := range order {
		deps := map[string]bool{}
		for dependency := range edges[name] {
			if dependency != name {
				deps[dependency] = true
			}
		}
		remaining[name] = deps
	}
	result := make([]string, 0, len(order))
	for len(result) < len(order) {
		var ready []string
		for _, name := range order {
			if _, pending := remaining[name]; !pending {
				continue
			}
			if len(remaining[name]) == 0 {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			cyclic := make([]string, 0, len(remaining))
			for name := range remaining {
				cyclic = append(cyclic, name)
			}
			sort.Strings(cyclic)
			return nil, fmt.Errorf("E_WORKSPACE_CYCLE: members depend on each other: %s", strings.Join(cyclic, ", "))
		}
		sort.Slice(ready, func(i, j int) bool { return index[ready[i]] < index[ready[j]] })
		next := ready[0]
		result = append(result, next)
		delete(remaining, next)
		for name := range remaining {
			delete(remaining[name], next)
		}
	}
	return result, nil
}

func sortedKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
