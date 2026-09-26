package vcpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientEnvironmentOverridesExistingValues(t *testing.T) {
	t.Setenv("TRESTLE_VCPKG_TEST_PATH", "original")
	client := Client{Environment: map[string]string{
		"PATH":                    "C:\\Visual Studio\\Tools",
		"TRESTLE_VCPKG_TEST_PATH": "overridden",
	}}
	values := make(map[string]string)
	for _, entry := range client.environment() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			values[strings.ToUpper(parts[0])] = parts[1]
		}
	}
	if values["PATH"] != "C:\\Visual Studio\\Tools" || values["TRESTLE_VCPKG_TEST_PATH"] != "overridden" {
		t.Fatalf("unexpected environment: %#v", values)
	}
	if os.Getenv("TRESTLE_VCPKG_TEST_PATH") != "original" {
		t.Fatal("client environment mutated the parent process")
	}
}

func TestIsInstalledUsesVcpkgInfoManifest(t *testing.T) {
	root := t.TempDir()
	info := filepath.Join(root, "installed", "vcpkg", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info, "glfw3_3.4_x64-windows.list"), []byte("x64-windows/share/glfw3/copyright\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	if !client.IsInstalled("glfw3") {
		t.Fatal("glfw3 info manifest should mark the port installed")
	}
	if client.IsInstalled("missing") {
		t.Fatal("missing port was reported installed")
	}
}
