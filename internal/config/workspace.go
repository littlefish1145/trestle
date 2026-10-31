package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"trestle/internal/fsx"
)

// WorkspaceContext is the fully loaded view of a workspace root: the root
// configuration plus the merged configuration of every member, in declaration
// order. A nil *WorkspaceContext (from ParseWorkspace when the root has no
// [workspace] section) means the project is a standalone single project.
type WorkspaceContext struct {
	RootConfig  Config
	Members     map[string]*Config
	MemberOrder []string
}

// MemberNames returns the member names in their declared order.
func (wc *WorkspaceContext) MemberNames() []string {
	if wc == nil {
		return nil
	}
	return append([]string(nil), wc.MemberOrder...)
}

// Member returns the merged configuration of one member, or nil.
func (wc *WorkspaceContext) Member(name string) *Config {
	if wc == nil {
		return nil
	}
	return wc.Members[name]
}

// FindTarget locates a target by ID across the root and every member and
// reports which configuration owns it. Cross-member dependency references look
// targets up this way.
func (wc *WorkspaceContext) FindTarget(id string) (Config, bool) {
	if wc == nil {
		return Config{}, false
	}
	if _, ok := wc.RootConfig.Targets[id]; ok {
		return wc.RootConfig, true
	}
	for _, name := range wc.MemberOrder {
		if _, ok := wc.Members[name].Targets[id]; ok {
			return *wc.Members[name], true
		}
	}
	return Config{}, false
}

// ParseWorkspace loads the configuration at path. When it declares a
// [workspace] section, every member configuration is loaded from its own
// directory and merged with the root. A standalone configuration yields a nil
// context and no members, so existing single-project callers are unaffected.
func ParseWorkspace(rootPath string) (*WorkspaceContext, error) {
	root, err := Load(rootPath)
	if err != nil {
		return nil, err
	}
	if root.Workspace == nil {
		return nil, nil
	}
	if len(root.Workspace.Members) == 0 {
		return nil, fmt.Errorf("E_WORKSPACE_INVALID: %s: [workspace] must declare at least one member", rootPath)
	}
	rootDir, _ := filepath.Abs(filepath.Dir(rootPath))
	context := &WorkspaceContext{
		RootConfig: root,
		Members:    map[string]*Config{},
	}
	seen := map[string]bool{}
	for _, member := range root.Workspace.Members {
		name, dir, err := MemberDir(rootDir, member)
		if err != nil {
			return nil, err
		}
		canonical, err := fsx.Canonical(dir)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" {
			canonical = strings.ToLower(canonical)
		}
		if seen[canonical] {
			return nil, fmt.Errorf("E_WORKSPACE_INVALID: member %q is listed twice in [workspace] of %s", name, rootPath)
		}
		seen[canonical] = true
		merged, err := MergeMemberConfig(root, filepath.Join(rootDir, DefaultFileName), filepath.Join(dir, DefaultFileName))
		if err != nil {
			return nil, err
		}
		context.Members[name] = &merged
		context.MemberOrder = append(context.MemberOrder, name)
	}
	if err := ValidateWorkspaceTargets(context); err != nil {
		return nil, err
	}
	return context, nil
}

// MemberDir validates one [workspace] members entry and returns its name (a
// slash-normalized relative path) and its absolute directory. Absolute paths,
// empty entries and escaping paths are rejected so a workspace cannot reach
// outside its checkout.
func MemberDir(rootDir, member string) (string, string, error) {
	clean := filepath.Clean(filepath.ToSlash(strings.TrimSpace(member)))
	if clean == "" || clean == "." {
		return "", "", fmt.Errorf("E_WORKSPACE_INVALID: [workspace] members entries must name a directory, got %q", member)
	}
	if filepath.IsAbs(member) || filepath.VolumeName(clean) != "" {
		return "", "", fmt.Errorf("E_WORKSPACE_INVALID: member %q must be a workspace-relative directory", member)
	}
	dir := filepath.Join(rootDir, filepath.FromSlash(clean))
	if rel, err := filepath.Rel(rootDir, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("E_WORKSPACE_INVALID: member %q escapes the workspace directory", member)
	}
	canonicalRoot, err := fsx.Canonical(rootDir)
	if err != nil {
		return "", "", err
	}
	canonicalDir, err := fsx.Canonical(dir)
	if err != nil {
		return "", "", err
	}
	if rel, err := filepath.Rel(canonicalRoot, canonicalDir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("E_WORKSPACE_INVALID: member %q resolves outside the workspace directory", member)
	}
	return filepath.ToSlash(clean), dir, nil
}

// MergeMemberConfig loads the member trestle.toml at memberPath and inherits
// workspace-level settings from the root configuration at rootPath. Member
// values win; the root only fills omissions. Table merging:
//
//   - toolchain, vcpkg, conan: inherited key by key.
//   - compiler_presets: union, member winning on name conflicts.
//   - build (profile, flags, standards and friends): the member's values only.
//   - build_dir and compile_commands: inherited from the root, because a
//     workspace builds into one shared build tree and one aggregate
//     compile_commands.json (members are given a per-member subdirectory later
//     in the graph layer).
//   - packages: root packages the member omits are copied, then the
//     workspace's [workspace.dependencies] entries are added by port name,
//     so shared dependency providers are declared once.
func MergeMemberConfig(root Config, rootPath, memberPath string) (Config, error) {
	rootTree, err := decodeTreePath(rootPath)
	if err != nil {
		return Config{}, err
	}
	memberData, err := os.ReadFile(memberPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("E_WORKSPACE_INVALID: member %s: %w", memberPath, ErrNotFound)
		}
		return Config{}, err
	}
	memberTree, err := decodeTree(memberData)
	if err != nil {
		return Config{}, fmt.Errorf("E_WORKSPACE_INVALID: member %s: %w", memberPath, err)
	}
	if _, ok := memberTree["schema_version"]; !ok {
		if version, ok := rootTree["schema_version"]; ok {
			memberTree["schema_version"] = version
		}
	}
	inherited := []string{"toolchain", "vcpkg", "conan"}
	for _, section := range inherited {
		mergeTables(memberTree, rootTree, section)
	}
	mergePresets(memberTree, rootTree)
	packagesTable, ok := memberTree["packages"].(map[string]any)
	if !ok || packagesTable == nil {
		packagesTable = map[string]any{}
		memberTree["packages"] = packagesTable
	}
	mergePackagesTable(packagesTable, rootTree)
	if err := mergeWorkspacePackages(memberTree, root); err != nil {
		return Config{}, err
	}
	var mergedData bytes.Buffer
	encoder := toml.NewEncoder(&mergedData)
	encoder.Indent = "  "
	if err := encoder.Encode(memberTree); err != nil {
		return Config{}, err
	}
	merged, err := Decode(memberPath, mergedData.Bytes())
	if err != nil {
		return Config{}, err
	}
	// A workspace builds into one shared build tree described by the root, so
	// the member's build_dir / compile_commands (which normalize against its
	// own directory) are overridden by the root's. The graph layer gives each
	// member a per-member subdirectory inside it.
	merged.Build.BuildDir = root.Build.BuildDir
	merged.Build.CompileCommands = root.Build.CompileCommands
	return merged, nil
}

// mergeTables copies keys from the parent's <section> table into the child's
// when the child does not define them. When the child omits the whole section,
// the parent's table is copied wholesale.
func mergeTables(child, parent map[string]any, section string) {
	parentTable, ok := parent[section].(map[string]any)
	if !ok {
		return
	}
	childTable, ok := child[section].(map[string]any)
	if !ok || childTable == nil {
		child[section] = cloneTable(parentTable)
		return
	}
	for key, value := range parentTable {
		if _, present := childTable[key]; !present {
			childTable[key] = cloneValue(value)
		}
	}
}

// mergePresets unions the root's compiler presets with the member's own; a
// preset name declared by both uses the member's definition.
func mergePresets(child, parent map[string]any) {
	parentTable, ok := parent["compiler_presets"].(map[string]any)
	if !ok || parentTable == nil {
		return
	}
	childTable, ok := child["compiler_presets"].(map[string]any)
	if !ok || childTable == nil {
		child["compiler_presets"] = cloneTable(parentTable)
		return
	}
	for name, value := range parentTable {
		if _, present := childTable[name]; !present {
			childTable[name] = cloneValue(value)
		}
	}
}

// mergePackagesTable copies root [packages] entries the member does not
// declare, then appends the workspace's shared [workspace.dependencies] by
// port name.
func mergePackagesTable(childTable map[string]any, parent map[string]any) {
	if parentTable, ok := parent["packages"].(map[string]any); ok {
		for name, value := range parentTable {
			if _, present := childTable[name]; !present {
				childTable[name] = cloneValue(value)
			}
		}
	}
}

// mergeWorkspacePackages turns the root's [workspace.dependencies] array into
// named [packages] entries so every member resolves the same shared
// dependencies. Each entry must set port; a package already declared by the
// member (including inherited root packages, merged earlier) wins.
func mergeWorkspacePackages(child map[string]any, root Config) error {
	if root.Workspace == nil || len(root.Workspace.Dependencies) == 0 {
		return nil
	}
	packagesTable, ok := child["packages"].(map[string]any)
	if !ok || packagesTable == nil {
		packagesTable = map[string]any{}
		child["packages"] = packagesTable
	}
	for _, shared := range root.Workspace.Dependencies {
		name := shared.Port
		if name == "" {
			return fmt.Errorf("E_WORKSPACE_INVALID: [workspace] dependencies entries must set port so members can reference them")
		}
		if _, present := packagesTable[name]; present {
			continue
		}
		table := map[string]any{}
		if data, err := toml.Marshal(shared); err != nil {
			return err
		} else if _, err := toml.Decode(string(data), &table); err != nil {
			return err
		}
		packagesTable[name] = table
	}
	return nil
}

func decodeTreePath(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		return nil, err
	}
	return decodeTree(data)
}

func decodeTree(data []byte) (map[string]any, error) {
	var tree map[string]any
	if _, err := toml.Decode(string(data), &tree); err != nil {
		return nil, fmt.Errorf("parse TOML: %w", err)
	}
	return tree, nil
}

func cloneTable(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = cloneValue(value)
	}
	return clone
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneTable(typed)
	case []any:
		clone := make([]any, len(typed))
		for index, item := range typed {
			clone[index] = cloneValue(item)
		}
		return clone
	case []map[string]any:
		clone := make([]map[string]any, len(typed))
		for index, item := range typed {
			clone[index] = cloneTable(item)
		}
		return clone
	default:
		return value
	}
}

// ValidateWorkspaceTargets enforces the workspace naming rule: target IDs are
// globally unique, so two members (or a member and the root) may not declare
// the same target name.
func ValidateWorkspaceTargets(wc *WorkspaceContext) error {
	owners := map[string]string{}
	names := append([]string{"."}, wc.MemberOrder...)
	sort.Strings(names)
	for _, name := range names {
		cfg := wc.RootConfig
		if name != "." {
			cfg = *wc.Members[name]
		}
		inWorkspace := ""
		if name == "." {
			inWorkspace = "root"
		} else {
			inWorkspace = "member " + name
		}
		for id := range cfg.Targets {
			if owner, taken := owners[id]; taken {
				return fmt.Errorf("E_WORKSPACE_INVALID: target %q is declared by both %s and %s; targets must be unique within a workspace", id, owner, inWorkspace)
			}
			owners[id] = inWorkspace
		}
	}
	return nil
}
