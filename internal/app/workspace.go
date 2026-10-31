package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/graph"
	"trestle/internal/model"
	"trestle/internal/plan"
	"trestle/internal/policy"
	"trestle/internal/toolchain"
)

// workspaceMemberKey marks the context with the member an operation runs for,
// and workspaceProjectKey carries the resolved workspace graph so member builds
// reuse the cross-member resolution instead of re-resolving each member alone.
type (
	workspaceMemberKey  struct{}
	workspaceProjectKey struct{}
)

// WorkspaceBuildOptions selects which members and targets a workspace build
// covers: an empty Member builds every member, otherwise only that member.
type WorkspaceBuildOptions struct {
	Member  string
	Targets []string
	All     bool
	Force   bool
	Profile string
}

// LoadWorkspace parses the workspace at path. A standalone project (no
// [workspace] section) yields a nil context, so callers can branch on nil.
func LoadWorkspace(ctx context.Context, path string) (*config.WorkspaceContext, error) {
	return config.ParseWorkspace(path)
}

// IsWorkspace reports whether the configuration at path declares a
// [workspace] section, letting commands branch without disturbing standalone
// projects.
func IsWorkspace(path string) bool {
	cfg, err := config.Load(path)
	if err != nil {
		return false
	}
	return cfg.Workspace != nil
}

// SplitWorkspaceTargets parses "member:target" shorthand arguments into the
// named member and the remaining plain targets. Plain arguments are workspace
// targets resolved against the selected member.
func SplitWorkspaceTargets(args []string) (member string, targets []string, err error) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			return "", nil, fmt.Errorf("E_WORKSPACE_INVALID: target must not be empty")
		}
		name, target, prefixed := strings.Cut(arg, ":")
		if !prefixed {
			targets = append(targets, arg)
			continue
		}
		name, target = strings.TrimSpace(name), strings.TrimSpace(target)
		if name == "" || target == "" || strings.Contains(target, ":") {
			return "", nil, fmt.Errorf("E_WORKSPACE_INVALID: %q must use member:target syntax", arg)
		}
		if member != "" && member != name {
			return "", nil, fmt.Errorf("E_WORKSPACE_INVALID: cannot mix members %q and %q in one build", member, name)
		}
		member = name
		targets = append(targets, target)
	}
	return member, targets, nil
}

// BuildWorkspace builds the workspace at path. With no Member selection every
// member is built in dependency order; a Member builds just that member. Each
// member's merged configuration is injected into the shared operation context
// so the existing single-project pipeline handles the actual work.
func BuildWorkspace(ctx context.Context, path string, options WorkspaceBuildOptions, progress func(string)) error {
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if wc == nil {
		return fmt.Errorf("%s does not declare a [workspace] section", path)
	}
	_, err = graph.OrderWorkspaceMembers(wc)
	if err != nil {
		return err
	}
	if options.Member != "" {
		if !workspaceHasMember(wc, options.Member) {
			return fmt.Errorf("E_WORKSPACE_INVALID: unknown member %q; members: %s", options.Member, strings.Join(wc.MemberNames(), ", "))
		}
	}
	// "member:target" arguments select their own member; plain targets apply to
	// the -p selection (or every member when nothing is selected).
	prefixedMember, targets, err := SplitWorkspaceTargets(options.Targets)
	if err != nil {
		return err
	}
	options.Targets = targets
	if prefixedMember != "" {
		if options.Member != "" && options.Member != prefixedMember {
			return fmt.Errorf("E_WORKSPACE_INVALID: %q names member %q but -p selected %q", strings.Join(options.Targets, " "), prefixedMember, options.Member)
		}
		if !workspaceHasMember(wc, prefixedMember) {
			return fmt.Errorf("E_WORKSPACE_INVALID: unknown member %q; members: %s", prefixedMember, strings.Join(wc.MemberNames(), ", "))
		}
		options.Member = prefixedMember
		options.Targets = targets
	}
	if options.Profile != "" {
		wc.RootConfig.Build.Profile = options.Profile
		for _, cfg := range wc.Members {
			cfg.Build.Profile = options.Profile
		}
	}
	buildRoot, err := filepath.Abs(wc.RootConfig.Build.BuildDir)
	if err != nil {
		return err
	}
	workspace, err := graph.ResolveWorkspace(ctx, wc, buildRoot)
	if err != nil {
		return err
	}
	prefix := func(member string) func(string) {
		if progress == nil {
			return nil
		}
		return func(line string) { progress("[" + member + "] " + line) }
	}
	selected := workspaceBuildMembers(workspace, options.Member)
	for _, name := range selected {
		memberPath := workspaceMemberPath(wc, name)
		memberCtx, err := workspaceMemberContext(ctx, wc, name, options.Profile, workspace)
		if err != nil {
			return err
		}
		targets, all := options.Targets, options.All
		if options.Member != "" && name != options.Member {
			targets, all = nil, true
		}
		if err := BuildTargetsWithOptions(memberCtx, memberPath, targets, all, options.Force, prefix(name)); err != nil {
			return err
		}
	}
	return publishWorkspaceCompileCommands(ctx, wc, buildRoot)
}

// RunWorkspaceTests runs the test jobs of each selected member in dependency
// order, reusing the same per-member configuration injection as builds.
func RunWorkspaceTests(ctx context.Context, path string, member string, selection TestSelection, progress func(string)) error {
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if wc == nil {
		return fmt.Errorf("%s does not declare a [workspace] section", path)
	}
	order, err := graph.OrderWorkspaceMembers(wc)
	if err != nil {
		return err
	}
	if member != "" && !workspaceHasMember(wc, member) {
		return fmt.Errorf("E_WORKSPACE_INVALID: unknown member %q; members: %s", member, strings.Join(wc.MemberNames(), ", "))
	}
	buildRoot, err := filepath.Abs(wc.RootConfig.Build.BuildDir)
	if err != nil {
		return err
	}
	workspace, err := graph.ResolveWorkspace(ctx, wc, buildRoot)
	if err != nil {
		return err
	}
	for _, name := range order {
		if member != "" && member != name {
			continue
		}
		memberCfg := wc.Member(name)
		if name == graph.RootMemberName {
			memberCfg = &wc.RootConfig
		}
		if memberCfg == nil {
			continue
		}
		// A member without test targets has nothing to run; skip it in the
		// all-member sweep instead of failing the whole workspace.
		if len(listTestsConfig(*memberCfg)) == 0 {
			if member != "" {
				return fmt.Errorf("member %s declares no test jobs", name)
			}
			if progress != nil {
				progress("[" + name + "] no test jobs; skipped")
			}
			continue
		}
		memberPath := workspaceMemberPath(wc, name)
		memberCtx, err := workspaceMemberContext(ctx, wc, name, "", workspace)
		if err != nil {
			return err
		}
		prefixed := progress
		if progress != nil {
			prefixed = func(line string) { progress("[" + name + "] " + line) }
		}
		// Build provider members before the selected member's test binaries.
		for _, provider := range workspaceBuildMembers(workspace, name) {
			if provider == name {
				continue
			}
			providerCtx, err := workspaceMemberContext(ctx, wc, provider, "", workspace)
			if err != nil {
				return err
			}
			if err := BuildTargetsWithProgress(providerCtx, workspaceMemberPath(wc, provider), nil, true, prefixed); err != nil {
				return err
			}
		}
		if err := RunTests(memberCtx, memberPath, selection, prefixed); err != nil {
			return err
		}
	}
	return nil
}

// DoctorWorkspace checks members of the workspace, reporting which member a
// failure belongs to and returning the joined failures. An empty member checks
// every member.
func DoctorWorkspace(ctx context.Context, path, member string, progress func(string)) error {
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if wc == nil {
		return fmt.Errorf("%s does not declare a [workspace] section", path)
	}
	order, err := graph.OrderWorkspaceMembers(wc)
	if err != nil {
		return err
	}
	if member != "" && wc.Member(member) == nil {
		return fmt.Errorf("E_WORKSPACE_INVALID: unknown member %q; members: %s", member, strings.Join(wc.MemberNames(), ", "))
	}
	var failures []error
	for _, name := range order {
		if member != "" && member != name {
			continue
		}
		if progress != nil {
			progress("[" + name + "] " + "checking member")
		}
		memberPath := workspaceMemberPath(wc, name)
		memberCtx, err := workspaceMemberContext(ctx, wc, name, "", model.WorkspaceProject{})
		if err != nil {
			failures = append(failures, fmt.Errorf("member %s: %w", name, err))
			continue
		}
		prefixed := progress
		if progress != nil {
			prefixed = func(line string) { progress("[" + name + "] " + line) }
		}
		if err := DoctorWithContext(memberCtx, memberPath, prefixed); err != nil {
			failures = append(failures, fmt.Errorf("member %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

// workspaceOutputPaths computes every member artifact path for the given
// toolchain, so a consumer member's manifest can link the provider's built
// library directly instead of guessing its extension.
func workspaceOutputPaths(ctx context.Context, tc toolchain.Toolchain) map[string]string {
	workspace, ok := ctx.Value(workspaceProjectKey{}).(model.WorkspaceProject)
	if !ok {
		return nil
	}
	outputs := make(map[string]string, len(workspace.Targets))
	for _, member := range workspace.Members {
		for _, target := range member.Project.Targets {
			if output, err := plan.WorkspaceOutput(target, member.BuildDir, tc); err == nil {
				outputs[string(target.ID)] = output
			}
		}
	}
	return outputs
}

// ReleaseWorkspace builds and packages every member in dependency order. Each
// member uses its own [package] settings (output path, optimization, targets)
// and its merged configuration, so cross-member dependencies still resolve; the
// computed release settings are not written back to the member files.
func ReleaseWorkspace(ctx context.Context, path string, progress func(string)) error {
	wc, err := LoadWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if wc == nil {
		return fmt.Errorf("%s does not declare a [workspace] section", path)
	}
	order, err := graph.OrderWorkspaceMembers(wc)
	if err != nil {
		return err
	}
	buildRoot, err := filepath.Abs(wc.RootConfig.Build.BuildDir)
	if err != nil {
		return err
	}
	workspace, err := graph.ResolveWorkspace(ctx, wc, buildRoot)
	if err != nil {
		return err
	}
	for _, name := range order {
		member := wc.Member(name)
		if member == nil {
			continue
		}
		memberPath := workspaceMemberPath(wc, name)
		cfg := *member
		if err := applyReleaseOptimization(&cfg); err != nil {
			return err
		}
		cfg, err = policy.ApplyAt(ctx, cfg, cfg.Root())
		if err != nil {
			return err
		}
		memberCtx := context.WithValue(ctx, configurationKey{}, cfg)
		memberCtx = context.WithValue(memberCtx, workspaceProjectKey{}, workspace)
		memberCtx = context.WithValue(memberCtx, workspaceMemberKey{}, name)
		targets := cfg.Package.Targets
		if len(targets) == 0 {
			targets = cfg.Build.DefaultTargets
		}
		prefixed := progress
		if progress != nil {
			prefixed = func(line string) { progress("[" + name + "] " + line) }
		}
		if prefixed != nil {
			prefixed("Release optimization: " + cfg.Package.Optimization)
		}
		if err := BuildTargetsWithProgress(memberCtx, memberPath, targets, false, prefixed); err != nil {
			return err
		}
		if err := packageSelectedWithContext(memberCtx, memberPath, targets, prefixed); err != nil {
			return err
		}
	}
	return nil
}

// workspaceMemberPath returns the member's trestle.toml path, or the root path
// for the pseudo member that owns the root's own targets.
func workspaceMemberPath(wc *config.WorkspaceContext, name string) string {
	if name == graph.RootMemberName {
		return filepath.Join(wc.RootConfig.Root(), config.DefaultFileName)
	}
	return filepath.Join(wc.RootConfig.Root(), filepath.FromSlash(name), config.DefaultFileName)
}

// workspaceMemberContext carries the member's merged, policy-applied
// configuration plus the resolved workspace graph into the shared operation
// pipeline.
func workspaceMemberContext(ctx context.Context, wc *config.WorkspaceContext, name, profile string, workspace model.WorkspaceProject) (context.Context, error) {
	var cfg config.Config
	if name == graph.RootMemberName {
		cfg = wc.RootConfig
	} else {
		member := wc.Member(name)
		if member == nil {
			return ctx, fmt.Errorf("E_WORKSPACE_INVALID: unknown member %q", name)
		}
		cfg = *member
	}
	if profile != "" {
		cfg.Build.Profile = profile
	}
	cfg, err := policy.ApplyAt(ctx, cfg, cfg.Root())
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, configurationKey{}, cfg)
	ctx = context.WithValue(ctx, workspaceMemberKey{}, name)
	if len(workspace.Members) > 0 {
		ctx = context.WithValue(ctx, workspaceProjectKey{}, workspace)
	}
	return ctx, nil
}

// publishWorkspaceCompileCommands merges every member's compile_commands.json
// into the workspace build profile directory and the root's configured
// compile_commands path, so one editor configuration covers all members.
func publishWorkspaceCompileCommands(ctx context.Context, wc *config.WorkspaceContext, buildRoot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	profile := wc.RootConfig.Build.Profile
	if profile == "" {
		profile = "debug"
	}
	dirs := make([]string, 0, len(wc.MemberOrder)+1)
	for _, name := range wc.MemberOrder {
		dirs = append(dirs, filepath.Join(buildRoot, profile, name))
	}
	if len(wc.RootConfig.Targets) > 0 {
		dirs = append(dirs, filepath.Join(buildRoot, profile))
	}
	var entries []compileCommandEntry
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, "compile_commands.json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		var member []compileCommandEntry
		if err := json.Unmarshal(data, &member); err != nil {
			return err
		}
		for _, entry := range member {
			// A root database may be the previous aggregate. Only retain root
			// commands here; member databases below are the current authority.
			if filepath.Clean(dir) == filepath.Clean(filepath.Join(buildRoot, profile)) && filepath.Clean(entry.Directory) != filepath.Clean(dir) {
				continue
			}
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	unique := make(map[string]compileCommandEntry)
	for _, entry := range entries {
		unique[entry.Directory+"\x00"+entry.File+"\x00"+entry.Output] = entry
	}
	entries = entries[:0]
	for _, entry := range unique {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Directory != b.Directory {
			return a.Directory < b.Directory
		}
		return a.Output < b.Output
	})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	aggregateDir := filepath.Join(buildRoot, profile)
	if err := fsx.AtomicWrite(filepath.Join(aggregateDir, "compile_commands.json"), data); err != nil {
		return err
	}
	destination := wc.RootConfig.Build.CompileCommands
	if destination != "" && filepath.Clean(destination) != filepath.Clean(filepath.Join(aggregateDir, "compile_commands.json")) {
		lock, err := fsx.WaitLock(ctx, destination, "publish workspace compile commands")
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := fsx.AtomicWrite(destination, data); err != nil {
			return err
		}
	}
	return nil
}

func workspaceHasMember(wc *config.WorkspaceContext, name string) bool {
	return wc.Member(name) != nil || (name == graph.RootMemberName && len(wc.RootConfig.Targets) > 0)
}

// workspaceBuildMembers includes the selected member's transitive providers in
// topological order, so a clean checkout can build a consumer directly.
func workspaceBuildMembers(workspace model.WorkspaceProject, selected string) []string {
	if selected == "" {
		return append([]string(nil), workspace.Order...)
	}
	needed := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		member, ok := workspace.Member(name)
		if ok {
			for _, provider := range member.DependsOn {
				visit(provider)
			}
		}
	}
	visit(selected)
	result := []string{}
	for _, name := range workspace.Order {
		if needed[name] {
			result = append(result, name)
		}
	}
	return result
}
