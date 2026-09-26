package vcpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/model"
)

func TestResolveInstalledManifestUsesOnlyOwnedFiles(t *testing.T) {
	root := t.TempDir()
	triplet := "x64-windows"
	installed := filepath.Join(root, "installed", triplet)
	info := filepath.Join(root, "installed", "vcpkg", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installed, "include"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "x64-windows/include/demo/demo.h\n" +
		"x64-windows/lib/demo.lib\n" +
		"x64-windows/lib/pkgconfig/demo.pc\n" +
		"x64-windows/bin/demo.dll\n"
	if err := os.WriteFile(filepath.Join(info, "demo_1.0_x64-windows.list"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := DetectLayout(root, triplet)
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := ResolveInstalledManifest(layout, "demo")
	if !ok || len(usage.Compile.IncludeDirs) != 1 || len(usage.Link.Items) != 1 || usage.Link.Items[0].Name != "demo" {
		t.Fatalf("unexpected usage: %#v, ok=%v", usage, ok)
	}
	if len(usage.Link.RuntimeFiles) != 1 {
		t.Fatalf("runtime files were not discovered: %#v", usage.Link.RuntimeFiles)
	}
}

func TestFinalizeUsageSelectsOneRuntimeForProfile(t *testing.T) {
	root := t.TempDir()
	layout := Layout{
		ReleaseBinDir: filepath.Join(root, "bin"),
		DebugBinDir:   filepath.Join(root, "debug", "bin"),
		ReleaseLibDir: filepath.Join(root, "lib"),
		DebugLibDir:   filepath.Join(root, "debug", "lib"),
	}
	for _, dir := range []string{layout.ReleaseBinDir, layout.DebugBinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo.dll"), []byte("dll"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	usage := model.Usage{Link: model.LinkUsage{RuntimeFiles: []string{
		filepath.Join(layout.ReleaseBinDir, "demo.dll"),
		filepath.Join(layout.DebugBinDir, "demo.dll"),
	}}}
	got := (Resolver{Profile: "debug"}).finalizeUsage(layout, usage)
	if len(got.Link.RuntimeFiles) != 1 || !strings.EqualFold(got.Link.RuntimeFiles[0], filepath.Join(layout.DebugBinDir, "demo.dll")) {
		t.Fatalf("debug runtime selection = %#v", got.Link.RuntimeFiles)
	}
}
