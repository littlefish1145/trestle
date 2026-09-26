package app

import (
	"path/filepath"
	"testing"

	"trestle/internal/config"
)

func TestAddPackageChoosesExecutableAndPersistsFeatures(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, config.DefaultFileName)
	cfg := config.Default("sample")
	delete(cfg.Targets, "app")
	cfg.Build.DefaultTargets = []string{"game"}
	cfg.Targets["library"] = config.Target{Type: "static", Sources: []string{"src/library.cpp"}}
	cfg.Targets["game"] = config.Target{Type: "executable", Sources: []string{"src/main.cpp"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := AddPackage(path, "sdl2[image,ttf]", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pkg := loaded.Packages["sdl2"]
	if len(pkg.Features) != 2 || pkg.Features[0] != "image" || pkg.Features[1] != "ttf" {
		t.Fatalf("features were not persisted: %#v", pkg)
	}
	if len(loaded.Targets["game"].Dependencies) != 1 || loaded.Targets["game"].Dependencies[0].Package != "sdl2" {
		t.Fatalf("package should attach to executable target: %#v", loaded.Targets)
	}
}

func TestAddPackagePrefersTargetAlreadyLinkingMatchingLibrary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, config.DefaultFileName)
	cfg := config.Default("sample")
	delete(cfg.Targets, "app")
	cfg.Build.DefaultTargets = []string{"voxel"}
	cfg.Targets["test_color"] = config.Target{Type: "executable", Sources: []string{"tests/color.c"}}
	cfg.Targets["voxel"] = config.Target{Type: "executable", Sources: []string{"game/voxel.c"}, Libraries: []string{"glfw"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := AddPackage(path, "glfw3", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Targets["voxel"].Dependencies) != 1 || loaded.Targets["voxel"].Dependencies[0].Package != "glfw3" {
		t.Fatalf("glfw should attach to voxel: %#v", loaded.Targets["voxel"])
	}
	if len(loaded.Targets["test_color"].Dependencies) != 0 {
		t.Fatalf("glfw should not attach to unrelated default target: %#v", loaded.Targets["test_color"])
	}
}
