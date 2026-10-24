package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeToml(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const workspaceRootToml = `
schema_version = 7
[project]
name = "ws"
[build]
profile = "debug"
cxx_flags = ["-O2"]
[toolchain]
cxx = "c++23"
[compiler_presets.fast]
cxx = "clang++"
[packages]
sys = { provider = "manual", include_dirs = ["shared/include"] }
[workspace]
members = ["core", "cli"]
[[workspace.dependencies]]
provider = "vcpkg"
port = "fmt"
[targets.tooling]
type = "static"
sources = ["src/tooling.cpp"]
`

const memberToml = `
schema_version = 7
[project]
name = "member"
[build]
cxx_flags = ["-Os"]
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
`

const cliToml = `
schema_version = 7
[project]
name = "member"
[targets.ctl]
type = "executable"
sources = ["src/ctl.cpp"]
`

func TestParseWorkspaceStandaloneConfigReturnsNilContext(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, DefaultFileName, "schema_version = 7\n[project]\nname=\"solo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n")
	wc, err := ParseWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	if wc != nil {
		t.Fatalf("standalone config produced a workspace context: %+v", wc)
	}
}

func TestParseWorkspaceLoadsAndMergesMembers(t *testing.T) {
	dir := t.TempDir()
	rootPath := writeToml(t, dir, DefaultFileName, workspaceRootToml)
	writeToml(t, dir, "core"+string(filepath.Separator)+DefaultFileName, memberToml)
	writeToml(t, dir, "cli"+string(filepath.Separator)+DefaultFileName, cliToml)
	wc, err := ParseWorkspace(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if wc == nil {
		t.Fatal("workspace config produced no context")
	}
	if got := strings.Join(wc.MemberNames(), ","); got != "core,cli" {
		t.Fatalf("member order: %q", got)
	}
	core := wc.Member("core")
	if core == nil {
		t.Fatal("member core missing")
	}
	// Toolchain and presets are inherited; flags stay the member's.
	if core.Toolchain.CXX != "c++23" {
		t.Fatalf("toolchain not inherited: %+v", core.Toolchain)
	}
	if got := core.Build.CXXFlags; len(got) != 1 || got[0] != "-Os" {
		t.Fatalf("member build flags must win: %+v", core.Build.CXXFlags)
	}
	if _, ok := core.CompilerPresets["fast"]; !ok {
		t.Fatalf("root compiler preset not inherited: %+v", core.CompilerPresets)
	}
	// Root packages and [workspace.dependencies] are shared.
	if _, ok := core.Packages["sys"]; !ok {
		t.Fatal("root package not inherited by member")
	}
	pkg, ok := core.Packages["fmt"]
	if !ok || pkg.Provider != "vcpkg" || pkg.Port != "fmt" {
		t.Fatalf("workspace dependency missing from member: %+v", core.Packages)
	}
	// Build dir follows the workspace root.
	wantBuildDir, err := filepath.Abs(filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	if core.Build.BuildDir != wantBuildDir {
		t.Fatalf("member build dir = %q, want %q", core.Build.BuildDir, wantBuildDir)
	}
	// Root targets and member targets coexist.
	if _, ok := wc.RootConfig.Targets["tooling"]; !ok {
		t.Fatal("root target lost")
	}
	if _, ok := core.Targets["app"]; !ok {
		t.Fatal("member target lost")
	}
	if core.Root() != filepath.Join(dir, "core") {
		t.Fatalf("member root = %q", core.Root())
	}
}

func TestParseWorkspaceRejectsDuplicateTargets(t *testing.T) {
	dir := t.TempDir()
	rootPath := writeToml(t, dir, DefaultFileName, workspaceRootToml)
	writeToml(t, dir, "core"+string(filepath.Separator)+DefaultFileName, memberToml)
	writeToml(t, dir, "cli"+string(filepath.Separator)+DefaultFileName, memberToml)
	// Both members declare the target "app", which violates global uniqueness.
	_, err := ParseWorkspace(rootPath)
	if err == nil {
		t.Fatal("expected duplicate target error")
	}
	if !strings.Contains(err.Error(), "E_WORKSPACE_INVALID") || !strings.Contains(err.Error(), `"app"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseWorkspaceRejectsMissingMemberConfig(t *testing.T) {
	dir := t.TempDir()
	rootPath := writeToml(t, dir, DefaultFileName, workspaceRootToml)
	writeToml(t, dir, "core"+string(filepath.Separator)+DefaultFileName, memberToml)
	_, err := ParseWorkspace(rootPath)
	if err == nil || (!os.IsNotExist(err) && !strings.Contains(err.Error(), "trestle.toml not found")) {
		t.Fatalf("expected missing member error, got %v", err)
	}
}

func TestParseWorkspaceRejectsEmptyMembersAndEscapes(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, DefaultFileName, "schema_version = 7\n[workspace]\n")
	if _, err := ParseWorkspace(path); err == nil {
		t.Fatal("expected error for empty [workspace]")
	}
	escape := writeToml(t, dir, DefaultFileName, "schema_version = 7\n[workspace]\nmembers = [\"..\", \"/abs\"]\n")
	if _, err := ParseWorkspace(escape); err == nil {
		t.Fatal("expected error for escaping members")
	}
}

func TestParseWorkspaceMemberWithoutBuildDirStaysUnchangedWithoutWorkspace(t *testing.T) {
	dir := t.TempDir()
	data := `
schema_version = 7
[project]
name = "solo"
[build]
build_dir = "out"
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
`
	path := writeToml(t, dir, DefaultFileName, data)
	wc, err := ParseWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	if wc != nil {
		t.Fatal("unexpected workspace context")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(filepath.Join(dir, "out"))
	if cfg.Build.BuildDir != want {
		t.Fatalf("single project build dir changed: %q", cfg.Build.BuildDir)
	}
}
