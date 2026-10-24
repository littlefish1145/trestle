package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
)

func writeWorkspace(t *testing.T, dir string, members map[string]string, extra string) string {
	t.Helper()
	root := "schema_version = 7\n[project]\nname = \"ws\"\n[workspace]\nmembers = [" + `"` + strings.Join(keysOf(members), `","`) + `"` + "]\n" + extra
	rootPath := filepath.Join(dir, config.DefaultFileName)
	if err := os.WriteFile(rootPath, []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range members {
		memberDir := filepath.Join(dir, name)
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
	return rootPath
}

func keysOf(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

const simpleMember = `
schema_version = 7
[project]
name = "member"
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
`

func TestSplitWorkspaceTargets(t *testing.T) {
	member, targets, err := SplitWorkspaceTargets([]string{"core:app", "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if member != "core" || len(targets) != 2 || targets[0] != "app" || targets[1] != "tool" {
		t.Fatalf("unexpected split: %q %#v", member, targets)
	}
	member, targets, err = SplitWorkspaceTargets([]string{"app"})
	if err != nil || member != "" || len(targets) != 1 {
		t.Fatalf("plain targets: %q %#v %v", member, targets, err)
	}
	if _, _, err := SplitWorkspaceTargets([]string{"core:app", "cli:tool"}); err == nil {
		t.Fatal("expected mixed member error")
	}
	if _, _, err := SplitWorkspaceTargets([]string{":app"}); err == nil {
		t.Fatal("expected malformed reference error")
	}
}

func TestIsWorkspaceDistinguishesRootAndMember(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember}, "")
	if !IsWorkspace(root) {
		t.Fatal("root must be detected as workspace")
	}
	if IsWorkspace(filepath.Join(dir, "core", config.DefaultFileName)) {
		t.Fatal("member must not be detected as workspace")
	}
	if IsWorkspace(filepath.Join(dir, "missing", config.DefaultFileName)) {
		t.Fatal("missing file must not be detected as workspace")
	}
}

func TestBuildWorkspaceRejectsUnknownMember(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember}, "")
	err := BuildWorkspace(context.Background(), root, WorkspaceBuildOptions{Member: "nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("expected unknown member error, got %v", err)
	}
}

func TestBuildWorkspaceRejectsNonWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	standalone := "schema_version = 7\n[project]\nname = \"solo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	if err := os.WriteFile(path, []byte(standalone), 0o644); err != nil {
		t.Fatal(err)
	}
	err := BuildWorkspace(context.Background(), path, WorkspaceBuildOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "[workspace]") {
		t.Fatalf("expected workspace error, got %v", err)
	}
}

func TestDoctorWorkspaceRejectsUnknownMember(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember}, "")
	err := DoctorWorkspace(context.Background(), root, "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("expected unknown member error, got %v", err)
	}
}

func TestRunWorkspaceTestsRejectsUnknownMember(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember}, "")
	err := RunWorkspaceTests(context.Background(), root, "nope", TestSelection{All: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("expected unknown member error, got %v", err)
	}
}

func TestEditorConfigWritesWorkspaceTasks(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{
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
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:util" }]
`,
	}, "")
	if _, err := EditorConfig(root, EditorConfigOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".vscode", "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"Trestle: Build core", "Trestle: Build app", "core:util", "Trestle: Run app:app"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("workspace tasks missing %q:\n%s", expected, text)
		}
	}
}

func TestWorkspaceMemberContextCarriesMergedConfig(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember},
		"[toolchain]\ncxx = \"c++23\"\n")
	wc, err := config.ParseWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := workspaceMemberContext(context.Background(), wc, "core", "release", model.WorkspaceProject{})
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := ctx.Value(configurationKey{}).(config.Config)
	if !ok {
		t.Fatal("member configuration missing from context")
	}
	if cfg.Toolchain.CXX != "c++23" {
		t.Fatalf("root toolchain not inherited: %+v", cfg.Toolchain)
	}
	if cfg.Build.Profile != "release" {
		t.Fatalf("profile override lost: %q", cfg.Build.Profile)
	}
	if name, _ := ctx.Value(workspaceMemberKey{}).(string); name != "core" {
		t.Fatalf("member key = %q", name)
	}
}
