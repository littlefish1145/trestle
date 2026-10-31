package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"trestle/internal/config"
	"trestle/internal/graph"
	"trestle/internal/model"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
)

func TestVulkanPlanTracksIncludesDefinesAndDependencies(t *testing.T) {
	for _, compiler := range []string{"glslc", "glslangValidator.exe"} {
		t.Run(compiler, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "main.comp")
			if err := os.WriteFile(source, nil, 0644); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default("shader")
			cfg.Targets = map[string]config.Target{"shader": {Type: "shader", Sources: []string{source}, ShaderStage: "comp", IncludeDirs: []string{filepath.Join(root, "include")}, Defines: []string{"LOCAL_SIZE=4"}, ShaderTool: compiler}}
			cfg.Build.DefaultTargets = []string{"shader"}
			project, err := graph.Resolve(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sdk := vulkan.SDK{Glslc: "glslc", Glslang: "glslangValidator.exe"}
			build, err := Build(cfg, project, Options{Root: root, BuildDir: filepath.Join(root, "build"), Vulkan: &sdk})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, action := range build.Actions {
				if action.ID != ActionID("shader", "shader") {
					continue
				}
				found = true
				args := strings.Join(action.Command.Args, " ")
				if action.Depfile == nil || !strings.Contains(args, action.Depfile.Path) || !strings.Contains(args, "-I../../include") || !strings.Contains(args, "-DLOCAL_SIZE=4") {
					t.Fatalf("shader dependency or usage lost: %+v", action)
				}
				if compiler != "glslc" && !strings.Contains(args, "-V ") {
					t.Fatal("glslang omitted Vulkan SPIR-V generation")
				}
			}
			if !found {
				t.Fatal("missing shader action")
			}
		})
	}
}
func TestMixedShaderProjectStillRequiresCompiler(t *testing.T) {
	for _, targets := range [][]model.ResolvedTarget{{{Target: model.Target{ID: "shader", Type: model.Shader}}, {Target: model.Target{ID: "app", Type: model.Executable}}}, {{Target: model.Target{ID: "app", Type: model.Executable}}, {Target: model.Target{ID: "shader", Type: model.Shader}}}} {
		if err := checkToolchain(model.ResolvedProject{Targets: targets}, Options{}); err == nil {
			t.Fatal("shader bypassed C++ toolchain validation")
		}
	}
	if err := checkToolchain(model.ResolvedProject{Targets: []model.ResolvedTarget{{Target: model.Target{Type: model.Shader}}}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := checkToolchain(model.ResolvedProject{Targets: []model.ResolvedTarget{{Target: model.Target{Type: model.Executable}}}}, Options{Toolchain: toolchain.Toolchain{CXX: "c++"}}); err != nil {
		t.Fatal(err)
	}
}
func TestShaderRejectsMultipleResolvedSources(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("shader")
	target := model.ResolvedTarget{Target: model.Target{ID: "shader", Type: model.Shader, Sources: []string{"a.vert", "b.vert"}, ShaderStage: "vert"}}
	s, err := newBuildState(cfg, model.ResolvedProject{Targets: []model.ResolvedTarget{target}}, Options{Root: root, BuildDir: root, Vulkan: &vulkan.SDK{Glslc: "glslc"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.emitShader(target); err == nil || !strings.Contains(err.Error(), "E_VULKAN_SHADER_SOURCE") {
		t.Fatal(err)
	}
}
