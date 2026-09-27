package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestBuildClangModuleAndOrdinaryImporter(t *testing.T) {
	compiler, compilerErr := exec.LookPath("clang++")
	scanner, scannerErr := exec.LookPath("clang-scan-deps")
	if compilerErr != nil || scannerErr != nil {
		t.Skip("clang++ and clang-scan-deps are required")
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("ninja is required")
	}
	root := t.TempDir()
	provider := filepath.Join(root, "math.cppm")
	consumer := filepath.Join(root, "main.cpp")
	header := filepath.Join(root, "value.hpp")
	if err := os.WriteFile(header, []byte("inline int value = 42;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, source := range map[string]string{
		provider: "module;\n#include \"value.hpp\"\nexport module math;\nexport int answer() { return value; }\n",
		consumer: "import math;\nint main() { return answer() == 42 ? 0 : 1; }\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default("module-demo")
	cfg.Build.Modules = true
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Toolchain.CXX = compiler
	cfg.Toolchain.ModuleScanner = scanner
	target := cfg.Targets["app"]
	target.Sources = []string{provider, consumer}
	cfg.Targets["app"] = target
	path := filepath.Join(root, "trestle.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	var output []string
	if err := BuildWithProgress(context.Background(), path, func(line string) { output = append(output, line) }); err != nil {
		t.Fatalf("build module and importer: %v\n%s", err, strings.Join(output, "\n"))
	}
	result, err := GenerateWithContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(cfg.Build.BuildDir, cfg.Build.Profile, filepath.FromSlash(result.Outputs["app"]))
	if err := exec.Command(program).Run(); err != nil {
		t.Fatalf("initial module result: %v", err)
	}
	if err := os.WriteFile(header, []byte("inline int value = 43;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output = nil
	if err := BuildWithProgress(context.Background(), path, func(line string) { output = append(output, line) }); err != nil {
		t.Fatalf("rebuild after header edit: %v\n%s", err, strings.Join(output, "\n"))
	}
	var exit *exec.ExitError
	if err := exec.Command(program).Run(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("module header change was not rebuilt: %v", err)
	}
}
