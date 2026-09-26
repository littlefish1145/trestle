package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	if err := Save(path, Default("demo")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src")); !os.IsNotExist(err) {
		t.Fatal("config Save must not create source files")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Name != "demo" || cfg.Build.Profile != "debug" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	data := "schema_version = 3\n[project]\nname = \"demo\"\n[build]\nprofile = \"debug\"\nunknown = true\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestNormalizeRepairsWSLModeWithWindowsCompiler(t *testing.T) {
	cfg := Default("demo")
	cfg.Toolchain.Mode = "wsl"
	cfg.Toolchain.WSLDistribution = "Ubuntu"
	cfg.Toolchain.CXX = `D:\vs2022\VC\bin\cl.exe`
	if err := cfg.normalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.Mode != "native" || cfg.Toolchain.WSLDistribution != "" {
		t.Fatalf("invalid mixed toolchain was not repaired: %#v", cfg.Toolchain)
	}
}

func TestNormalizeSelectsReleaseTargetsForLegacyConfig(t *testing.T) {
	cfg := Default("demo")
	cfg.Build.DefaultTargets = nil
	cfg.Package.Targets = nil
	if err := cfg.normalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Build.DefaultTargets) != 1 || cfg.Build.DefaultTargets[0] != "app" || len(cfg.Package.Targets) != 1 || cfg.Package.Targets[0] != "app" {
		t.Fatalf("legacy target defaults were not repaired: %#v %#v", cfg.Build.DefaultTargets, cfg.Package.Targets)
	}
}
