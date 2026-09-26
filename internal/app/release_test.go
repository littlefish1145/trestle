package app

import (
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestBuiltInReleaseOptimizations(t *testing.T) {
	cfg := config.Default("demo")
	cfg.Toolchain.CXX = `C:\VS\cl.exe`
	cfg.Package.Optimization = "speed"
	if err := applyReleaseOptimization(&cfg); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(append(cfg.Build.CompileFlags, cfg.Build.LinkFlags...), " ")
	if cfg.Build.Profile != "release" || !strings.Contains(joined, "/GL") || !strings.Contains(joined, "/LTCG") {
		t.Fatalf("speed optimization was not applied: %s", joined)
	}
}

func TestCustomReleaseOptimizationUsesCompilerPreset(t *testing.T) {
	cfg := config.Default("demo")
	cfg.CompilerPresets["native"] = config.CompilerPreset{CXX: "clang++", CXXFlags: []string{"-march=native"}, LinkFlags: []string{"-flto"}}
	cfg.Package.Optimization = "custom:native"
	if err := applyReleaseOptimization(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.CXX != "clang++" || len(cfg.Build.CXXFlags) != 1 || cfg.Build.CXXFlags[0] != "-march=native" {
		t.Fatalf("custom optimization was not applied: %#v", cfg)
	}
}

func TestProjectSettingEditsTargetMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectSetting(path, "target:app:include_dirs", "include,third_party/include"); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectSetting(path, "target:app:libraries", "fmt,ws2_32"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets["app"].IncludeDirs) != 2 || len(cfg.Targets["app"].Libraries) != 2 {
		t.Fatalf("target settings were not saved: %#v", cfg.Targets["app"])
	}
}

func TestConfigureReleaseRejectsUnknownOptimization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureRelease(path, "imaginary", nil, ""); err == nil {
		t.Fatal("expected unknown optimization error")
	}
}

func TestInvalidProjectSettingIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectSetting(path, "target:app:type", "imaginary"); err == nil {
		t.Fatal("expected target validation error")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Targets["app"].Type != "executable" {
		t.Fatalf("invalid setting was persisted: %#v", cfg.Targets["app"])
	}
}
