package plan

import (
	"os"
	"path/filepath"
	"testing"

	"trestle/internal/config"
	"trestle/internal/graph"
	"trestle/internal/toolchain"
)

// TestBuildEmitsStagesInOrder locks in the order of the orchestrator's stages:
// per-target compile and link actions, then the "all" phony, then each test
// stamp action, and finally the "test" phony.
func TestBuildEmitsStagesInOrder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"app.cpp", "check.cpp"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("int main() { return 0; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default("order")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{
		"app":   {Type: "executable", Sources: []string{filepath.Join(root, "app.cpp")}},
		"check": {Type: "test", Sources: []string{filepath.Join(root, "check.cpp")}},
	}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.GCC, CC: "cc", CXX: "c++", Linker: "c++", Archiver: "ar"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]ID, 0, len(build.Actions))
	for _, action := range build.Actions {
		ids = append(ids, action.ID)
	}
	want := []ID{
		ActionID("compile", "app", sourceID(filepath.Join(root, "app.cpp"))),
		ActionID("link", "app"),
		ActionID("compile", "check", sourceID(filepath.Join(root, "check.cpp"))),
		ActionID("link", "check"),
		"all",
		ActionID("test", "check"),
		"test",
	}
	if len(ids) != len(want) {
		t.Fatalf("action count = %d, want %d: %#v", len(ids), len(want), ids)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("action %d = %q, want %q (full order: %#v)", index, ids[index], want[index], ids)
		}
	}
}
