package vcpkg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledDependenciesParsesTransitivePorts(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "installed", "vcpkg")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	data := "Package: demo\nArchitecture: x64-windows\nDepends: mbedtls (>= 3), d2d1[core]:x64-windows\n\n"
	if err := os.WriteFile(filepath.Join(directory, "status"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := InstalledDependencies(Layout{Root: root, Triplet: "x64-windows"}, "demo")
	if len(deps) != 2 || deps[0] != "mbedtls" || deps[1] != "d2d1" {
		t.Fatalf("unexpected dependencies: %#v", deps)
	}
}
