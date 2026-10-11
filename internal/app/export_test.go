package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestExportCMakeRefusesOverwriteUnlessForced(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "src", "main.cpp")
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.DefaultFileName)
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	cmakePath := filepath.Join(dir, "CMakeLists.txt")
	if err := os.WriteFile(cmakePath, []byte("# handwritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ExportCMake(context.Background(), path, "", false, nil); err == nil {
		t.Fatal("expected an overwrite error for an existing CMakeLists.txt")
	}
	remaining, err := os.ReadFile(cmakePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "# handwritten\n" {
		t.Fatalf("a refused export must not touch the file, got %q", remaining)
	}

	result, err := ExportCMake(context.Background(), path, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Targets != 1 {
		t.Fatalf("expected 1 exported target, got %d", result.Targets)
	}
	data, err := os.ReadFile(cmakePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "add_executable(app") || !strings.Contains(string(data), "src/main.cpp") {
		t.Fatalf("unexpected exported CMakeLists.txt:\n%s", data)
	}
}
