package app

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trestle/internal/config"
)

func TestPackagePreservesFileMetadata(t *testing.T) {
	root := t.TempDir()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	modified := time.Date(2024, time.March, 12, 10, 20, 30, 0, time.UTC)
	contents := []byte(strings.Repeat("release artifact\n", 128))
	for _, artifact := range []struct {
		name string
		mode os.FileMode
	}{{"app", 0755}, {"data.txt", 0644}} {
		source := filepath.Join(root, artifact.name)
		if err := os.WriteFile(source, contents, artifact.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, modified, modified); err != nil {
			t.Fatal(err)
		}
		if err := addFile(archive, source, filepath.Join("bin", artifact.name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("unexpected archive entries: %d", len(reader.File))
	}
	for _, entry := range reader.File {
		info, err := os.Stat(filepath.Join(root, filepath.Base(entry.Name)))
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name != "bin/"+info.Name() || entry.Mode() != info.Mode() || !entry.Modified.Equal(info.ModTime()) || entry.Method != zip.Deflate {
			t.Fatalf("metadata lost for %s: mode=%v modified=%v method=%d; source mode=%v modified=%v", entry.Name, entry.Mode(), entry.Modified, entry.Method, info.Mode(), info.ModTime())
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(data, contents) {
			t.Fatalf("invalid content for %s: read=%v close=%v", entry.Name, readErr, closeErr)
		}
	}
}

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
