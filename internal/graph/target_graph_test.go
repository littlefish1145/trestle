package graph

import (
	"os"
	"path/filepath"
	"testing"

	"trestle/internal/config"
)

func TestResolvePropagatesPublicUsageAndLinkClosure(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.cpp")
	write("b.cpp")
	write("main.cpp")
	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "demo"},
		Build:         config.Build{Profile: "debug", BuildDir: "build", CStandard: "c17", CXXStandard: "c++20"},
		Toolchain:     config.Toolchain{C: "auto", CXX: "auto"},
		Package:       config.PackageOutput{Format: "zip"},
		Targets: map[string]config.Target{
			"core":   {Type: "static", Sources: []string{filepath.Join(dir, "a.cpp")}, IncludeDirs: []string{"include"}},
			"engine": {Type: "static", Sources: []string{filepath.Join(dir, "b.cpp")}, Dependencies: []config.Dependency{{Target: "core", Scope: "public"}}},
			"app":    {Type: "executable", Sources: []string{filepath.Join(dir, "main.cpp")}, Dependencies: []config.Dependency{{Target: "engine", Scope: "private"}}},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	project, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app := project.ByID["app"]
	if len(app.CompileSelf.IncludeDirs) != 1 {
		t.Fatalf("public include did not propagate: %#v", app.CompileSelf)
	}
	if len(project.LinkClosure["app"]) != 2 || project.LinkClosure["app"][0] != "core" || project.LinkClosure["app"][1] != "engine" {
		t.Fatalf("unexpected link closure: %#v", project.LinkClosure["app"])
	}
}
