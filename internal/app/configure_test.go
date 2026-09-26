package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestEnsureProjectConfigDetectsSourcesAndEntryPoint(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "src", "main.cpp")
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("#include \"helper.h\"\nint main() { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "helper.h"), []byte("int helper();\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.DefaultFileName)
	cfg, err := ensureProjectConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Targets["app"].Type != "executable" || len(cfg.Targets["app"].Sources) != 1 {
		t.Fatalf("unexpected generated target: %#v", cfg.Targets["app"])
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Targets["app"].Sources[0] != filepath.ToSlash(filepath.Join("src", "main.cpp")) {
		t.Fatalf("unexpected source path: %#v", loaded.Targets["app"].Sources)
	}
}

func TestEnsureProjectConfigSplitsMultipleEntryPoints(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "peek")
	for name, contents := range map[string]string{
		"main.c": "int main() { return 0; }",
		"dump.c": "const char *name = \"peekcss\"; int main() { return 0; }",
		"gui.c":  "const char *name = \"peekg\"; int main() { return 0; }",
		"core.c": "int core(void) { return 0; }",
	} {
		path := filepath.Join(dir, "src", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := ensureProjectConfig(filepath.Join(dir, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 3 {
		t.Fatalf("expected one target per entry point: %#v", cfg.Targets)
	}
	if len(cfg.Targets["peekcss"].Sources) != 1 || len(cfg.Targets["peekg"].Sources) != 1 {
		t.Fatalf("entry targets must not consume all sources: %#v", cfg.Targets)
	}
	if _, exists := cfg.Targets["core"]; exists {
		t.Fatalf("non-entry source must not become a target: %#v", cfg.Targets)
	}
}

func TestConfigureInteractiveDetectsAndWritesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := config.Save(path, config.Default("detected")); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(dir, "input.txt")
	outputPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(inputPath, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigureInteractive(path, input, output); err != nil {
		t.Fatal(err)
	}
	output.Close()
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "C++ project detected") || !strings.Contains(string(data), "Generated") {
		t.Fatalf("unexpected wizard output: %s", data)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
}
