package app

import (
	"os"
	"path/filepath"
	"testing"

	"trestle/internal/config"
)

// TestApplyExamplesWritesCUDAStaticTarget checks that enabling CUDA scaffolds a
// compilable kernel and registers it as a buildable static target.
func TestApplyExamplesWritesCUDAStaticTarget(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("demo")
	choices := initChoices{CUDA: true}
	if err := applyExamples(&cfg, root, choices, os.Stdout); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "cuda", "vector_add.cu")); err != nil {
		t.Fatalf("missing CUDA example: %v", err)
	}
	target, ok := cfg.Targets["cuda_example"]
	if !ok {
		t.Fatalf("missing CUDA target: %#v", cfg.Targets)
	}
	if target.Type != "static" || len(target.Sources) != 1 || target.Sources[0] != "src/cuda/vector_add.cu" {
		t.Fatalf("unexpected CUDA target: %#v", target)
	}
	if !containsString(cfg.Build.DefaultTargets, "cuda_example") {
		t.Fatalf("CUDA example is not part of the default targets: %#v", cfg.Build.DefaultTargets)
	}
}

// TestApplyExamplesWritesVulkanShaderTargets checks that an enabled Vulkan SDK
// scaffolds a vertex/fragment pair wired to the shader stages glslc expects.
func TestApplyExamplesWritesVulkanShaderTargets(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("demo")
	cfg.Toolchain.Vulkan = "/fake/vulkan"
	choices := initChoices{Vulkan: true}
	if err := applyExamples(&cfg, root, choices, os.Stdout); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"triangle.vert", "triangle.frag"} {
		if _, err := os.Stat(filepath.Join(root, "shaders", name)); err != nil {
			t.Fatalf("missing shader %s: %v", name, err)
		}
	}
	for name, stage := range map[string]string{"triangle_vert": "vert", "triangle_frag": "frag"} {
		target, ok := cfg.Targets[name]
		if !ok {
			t.Fatalf("missing shader target %s: %#v", name, cfg.Targets)
		}
		if target.Type != "shader" || target.ShaderStage != stage {
			t.Fatalf("target %s = %#v", name, target)
		}
		if !containsString(cfg.Build.DefaultTargets, name) {
			t.Fatalf("shader target %s is not part of the default targets: %#v", name, cfg.Build.DefaultTargets)
		}
	}
}

// TestApplyExamplesSkipsShadersWithoutSDK keeps the config buildable when the
// user asked for Vulkan but no SDK was found: no shader target can fail later.
func TestApplyExamplesSkipsShadersWithoutSDK(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("demo")
	if err := applyExamples(&cfg, root, initChoices{Vulkan: true}, os.Stdout); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Targets["triangle_vert"]; ok {
		t.Fatalf("shader targets require a detected SDK: %#v", cfg.Targets)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "main.cpp")); err != nil {
		t.Fatalf("missing starter source: %v", err)
	}
}

// TestApplyExamplesKeepsExistingSources checks that scaffolding never
// overwrites a file the user already has.
func TestApplyExamplesKeepsExistingSources(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "main.cpp")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("int main() { return 7; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("demo")
	if err := applyExamples(&cfg, root, initChoices{}, os.Stdout); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "int main() { return 7; }\n" {
		t.Fatalf("starter scaffold overwrote the user's source: %q", data)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
