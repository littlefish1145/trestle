package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
)

// workspaceFixture writes a workspace root and members to disk and parses it.
func workspaceFixture(t *testing.T, root string, members map[string]string) (*config.WorkspaceContext, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.DefaultFileName), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range members {
		memberDir := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Join(memberDir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(memberDir, config.DefaultFileName), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(memberDir, "src", "main.cpp"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wc, err := config.ParseWorkspace(filepath.Join(dir, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	return wc, dir
}

const wsRoot = `
schema_version = 7
[project]
name = "ws"
[workspace]
members = ["core", "app"]
`

func TestResolveWorkspaceSingleMemberMatchesSingleProject(t *testing.T) {
	dir := t.TempDir()
	memberDir := filepath.Join(dir, "core")
	if err := os.MkdirAll(filepath.Join(memberDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memberDir, "src", "main.cpp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memberDir, config.DefaultFileName), []byte(`
schema_version = 7
[project]
name = "core"
[targets.lib]
type = "static"
sources = ["src/main.cpp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.DefaultFileName), []byte(`
schema_version = 7
[project]
name = "ws"
[workspace]
members = ["core"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	wc, err := config.ParseWorkspace(filepath.Join(dir, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	single, err := ResolveAt(context.Background(), *wc.Member("core"), memberDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Members) != 1 || workspace.Members[0].Project.Name != single.Name {
		t.Fatalf("member mismatch: %#v", workspace.Members)
	}
	if len(workspace.Targets) != len(single.Targets) || len(workspace.ByID) != len(single.ByID) {
		t.Fatalf("flat targets differ: %d vs %d", len(workspace.Targets), len(single.Targets))
	}
	member := workspace.Members[0]
	wantBuildDir := filepath.Join(dir, "build", "debug", "core")
	if member.BuildDir != wantBuildDir {
		t.Fatalf("member build dir = %q, want %q", member.BuildDir, wantBuildDir)
	}
	if len(workspace.Order) != 1 || workspace.Order[0] != "core" {
		t.Fatalf("unexpected order: %#v", workspace.Order)
	}
}

func TestResolveWorkspaceCrossMemberDependencyOrdersMembers(t *testing.T) {
	wc, dir := workspaceFixture(t, wsRoot, map[string]string{
		"core": `
schema_version = 7
[project]
name = "core"
[targets.util]
type = "static"
sources = ["src/main.cpp"]
include_dirs = ["include"]
`,
		"app": `
schema_version = 7
[project]
name = "app"
[targets.lib]
type = "static"
sources = ["src/main.cpp"]
[targets.cli]
type = "executable"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:util", scope = "public" }]
`,
	})
	workspace, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Order) != 2 || workspace.Order[0] != "core" || workspace.Order[1] != "app" {
		t.Fatalf("unexpected member build order: %#v", workspace.Order)
	}
	if member, _ := workspace.MemberForTarget("cli"); member != "app" {
		t.Fatalf("cli owner = %q", member)
	}
	cli, ok := workspace.ByID["cli"]
	if !ok {
		t.Fatal("cli missing from flat target index")
	}
	// The provider's public include directory must reach the consumer resolved
	// against the provider's own directory, not the process working directory.
	wantInclude := filepath.Join(dir, "core", "include")
	if !containsString(cli.CompileSelf.IncludeDirs, wantInclude) {
		t.Fatalf("cross-member include usage not propagated: %#v", cli.CompileSelf.IncludeDirs)
	}
	provider := workspace.ByID["util"]
	if !containsString(provider.CompileSelf.IncludeDirs, wantInclude) {
		t.Fatalf("member include dir not rebased onto its root: %#v", provider.CompileSelf.IncludeDirs)
	}
	// The consumer must link the provider's artifact from the provider's build
	// directory.
	providerBin := filepath.Join(dir, "build", "debug", "core", "bin")
	if !containsString(cli.LinkSelf.LibraryDirs, providerBin) {
		t.Fatalf("cross-member library dir missing: %#v", cli.LinkSelf.LibraryDirs)
	}
	if !containsLinkItem(cli.LinkSelf.Items, "util") {
		t.Fatalf("cross-member library item missing: %#v", cli.LinkSelf.Items)
	}
	// The provider depends on nothing, the consumer on the provider.
	if members := workspace.Members; len(members) != 2 || len(members[1].DependsOn) != 1 || members[1].DependsOn[0] != "core" {
		t.Fatalf("member dependencies not recorded: %#v", members)
	}
	if len(cli.Implementation) == 0 || cli.Implementation[0].Ref.Target != "util" {
		t.Fatalf("dependency ref not rewritten: %#v", cli.Implementation)
	}
}

func TestResolveWorkspaceTransitiveCrossMemberUsage(t *testing.T) {
	wc, dir := workspaceFixture(t, `
schema_version = 7
[project]
name = "ws"
[workspace]
members = ["a", "b", "c"]
`, map[string]string{
		"a": `
schema_version = 7
[project]
name = "a"
[targets.top]
type = "static"
sources = ["src/main.cpp"]
dependencies = [{ target = "b:mid", scope = "public" }]
`,
		"b": `
schema_version = 7
[project]
name = "b"
[targets.mid]
type = "static"
sources = ["src/main.cpp"]
dependencies = [{ target = "c:base", scope = "public" }]
`,
		"c": `
schema_version = 7
[project]
name = "c"
[targets.base]
type = "static"
sources = ["src/main.cpp"]
include_dirs = ["include"]
`})
	workspace, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "b", "a"}
	if len(workspace.Order) != len(want) {
		t.Fatalf("order = %#v", workspace.Order)
	}
	for i, name := range want {
		if workspace.Order[i] != name {
			t.Fatalf("order = %#v, want %#v", workspace.Order, want)
		}
	}
	top := workspace.ByID["top"]
	if !containsString(top.CompileSelf.IncludeDirs, filepath.Join(dir, "c", "include")) {
		t.Fatalf("transitive include usage missing: %#v", top.CompileSelf.IncludeDirs)
	}
	// The provider chain must also be linkable from the consumer.
	for _, id := range []model.TargetID{"mid", "base"} {
		if !containsLinkItem(top.LinkSelf.Items, string(id)) {
			t.Fatalf("missing transitive link item %q: %#v", id, top.LinkSelf.Items)
		}
	}
}

func TestExampleWorkspaceResolves(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "hello-workspace")
	wc, err := config.ParseWorkspace(filepath.Join(root, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	if wc == nil {
		t.Fatal("example root has no [workspace] section")
	}
	workspace, err := ResolveWorkspace(context.Background(), wc, filepath.Join(t.TempDir(), "build"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Order) != 2 || workspace.Order[0] != "core" || workspace.Order[1] != "cli" {
		t.Fatalf("example build order = %#v", workspace.Order)
	}
	hello, ok := workspace.ByID["hello"]
	if !ok {
		t.Fatal("cli target hello missing")
	}
	if !containsLinkItem(hello.LinkSelf.Items, "greeter") {
		t.Fatalf("hello does not link greeter: %#v", hello.LinkSelf.Items)
	}
	if _, ok := workspace.ByID["greeter_test"]; !ok {
		t.Fatal("core test target missing")
	}
	if owner, _ := workspace.MemberForTarget("greeter"); owner != "core" {
		t.Fatalf("greeter owner = %q", owner)
	}
}

func TestResolveWorkspaceRejectsUnknownCrossMemberTarget(t *testing.T) {
	wc, dir := workspaceFixture(t, wsRoot, map[string]string{
		"core": `
schema_version = 7
[project]
name = "core"
[targets.util]
type = "static"
sources = ["src/main.cpp"]
`,
		"app": `
schema_version = 7
[project]
name = "app"
[targets.cli]
type = "executable"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:missing" }]
`,
	})
	_, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("expected unknown target error, got %v", err)
	}
}

func TestResolveWorkspaceRejectsMemberCycles(t *testing.T) {
	wc, dir := workspaceFixture(t, wsRoot, map[string]string{
		"core": `
schema_version = 7
[project]
name = "core"
[targets.util]
type = "static"
sources = ["src/main.cpp"]
dependencies = [{ target = "app:cli" }]
`,
		"app": `
schema_version = 7
[project]
name = "app"
[targets.cli]
type = "static"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:util" }]
`,
	})
	_, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err == nil || !strings.Contains(err.Error(), "E_WORKSPACE_CYCLE") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestResolveWorkspaceRootTargetsDependOnMembers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "core", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("core", "src", "util.cpp"), "")
	write(filepath.Join("src", "aggregate.cpp"), "")
	write(config.DefaultFileName, `
schema_version = 7
[project]
name = "ws"
[workspace]
members = ["core"]
[targets.aggregate]
type = "executable"
sources = ["src/aggregate.cpp"]
dependencies = [{ target = "core:util" }]
`)
	write(filepath.Join("core", config.DefaultFileName), `
schema_version = 7
[project]
name = "core"
[targets.util]
type = "static"
sources = ["src/util.cpp"]
`)
	wc, err := config.ParseWorkspace(filepath.Join(dir, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := ResolveWorkspace(context.Background(), wc, filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Order) != 2 || workspace.Order[0] != "core" || workspace.Order[1] != RootMemberName {
		t.Fatalf("root targets must build after members: %#v", workspace.Order)
	}
	if owner, _ := workspace.MemberForTarget("aggregate"); owner != RootMemberName {
		t.Fatalf("root target owner = %q", owner)
	}
	rootMember, ok := workspace.Member(RootMemberName)
	if !ok {
		t.Fatal("root member missing")
	}
	if rootMember.BuildDir != filepath.Join(dir, "build", "debug") {
		t.Fatalf("root build dir = %q", rootMember.BuildDir)
	}
	aggregate := workspace.ByID["aggregate"]
	if !containsLinkItem(aggregate.LinkSelf.Items, "util") {
		t.Fatalf("root target did not link the member target: %#v", aggregate.LinkSelf.Items)
	}
	if len(rootMember.DependsOn) != 1 || rootMember.DependsOn[0] != "core" {
		t.Fatalf("root member dependencies = %#v", rootMember.DependsOn)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if filepath.Clean(value) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

func containsLinkItem(items []model.LinkItem, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
