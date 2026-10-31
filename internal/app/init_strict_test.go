package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"trestle/internal/config"
	"trestle/internal/graph"
	"trestle/internal/vscode"
)

func TestStarterMatrixIsValidAndIdempotent(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, cuda := range []bool{false, true} {
			for _, vulkan := range []bool{false, true} {
				t.Run(fmt.Sprintf("workspace=%t/cuda=%t/vulkan=%t", workspace, cuda, vulkan), func(t *testing.T) {
					root := t.TempDir()
					cfg := config.Default("demo")
					choices := initChoices{Workspace: workspace, CUDA: cuda, Vulkan: vulkan}
					if cuda {
						cfg.Toolchain.CUDA = "auto"
					}
					if vulkan {
						cfg.Toolchain.Vulkan = "/fake/sdk"
					}
					dir := root
					if workspace {
						dir = filepath.Join(root, StarterMember)
					}
					for i := 0; i < 2; i++ {
						if err := applyExamples(&cfg, dir, choices, os.Stdout); err != nil {
							t.Fatal(err)
						}
					}
					want := []string{"app"}
					if cuda {
						want = append(want, "cuda_example")
					}
					if vulkan {
						want = append(want, "triangle_vert", "triangle_frag")
					}
					if !reflect.DeepEqual(cfg.Build.DefaultTargets, want) || len(cfg.Targets) != len(want) {
						t.Fatalf("starter targets differ or duplicate: %+v", cfg)
					}
					for _, target := range cfg.Targets {
						for _, source := range target.Sources {
							info, err := os.Stat(filepath.Join(dir, source))
							if err != nil || !info.Mode().IsRegular() {
								t.Fatalf("invalid starter source %s: %v", source, err)
							}
						}
					}
					path := filepath.Join(dir, config.DefaultFileName)
					if err := config.Save(path, cfg); err != nil {
						t.Fatal(err)
					}
					if workspace {
						rootCfg := config.Default("workspace")
						rootCfg.Workspace = &config.Workspace{Members: []string{StarterMember}}
						rootCfg.Targets = map[string]config.Target{}
						rootCfg.Build.DefaultTargets = nil
						rootPath := filepath.Join(root, config.DefaultFileName)
						if err := config.Save(rootPath, rootCfg); err != nil {
							t.Fatal(err)
						}
						wc, err := config.ParseWorkspace(rootPath)
						if err != nil {
							t.Fatal(err)
						}
						project, err := graph.ResolveWorkspace(t.Context(), wc, filepath.Join(root, "build"))
						if err != nil {
							t.Fatal(err)
						}
						if len(project.Targets) != len(want) {
							t.Fatalf("workspace lost starter targets: %+v", project)
						}
						document := vscode.Generate(vscode.FromWorkspace(root, wc))
						if len(document.Tasks) == 0 {
							t.Fatal("workspace has no editor tasks")
						}
					}
				})
			}
		}
	}
}
func TestStarterRejectsDirectoryAtSourcePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "main.cpp")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("demo")
	if err := applyExamples(&cfg, root, initChoices{}, os.Stdout); err == nil {
		t.Fatal("source directory accepted as a generated source")
	}
}
func TestWorkspaceMemberConflictPreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, StarterMember)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.DefaultFileName)
	want := []byte("user configuration")
	if err := os.WriteFile(path, want, 0644); err != nil {
		t.Fatal(err)
	}
	if err := initWorkspaceMember(root, StarterMember, initChoices{CUDA: true}, os.Stdout); err == nil {
		t.Fatal("member config overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("existing member changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src")); !os.IsNotExist(err) {
		t.Fatal("conflict generated sources")
	}
}

func TestInitializedWorkspaceMemberInheritsRootToolchainChanges(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("workspace")
	cfg.Workspace = &config.Workspace{Members: []string{StarterMember}}
	cfg.Targets = map[string]config.Target{}
	cfg.Build.DefaultTargets = nil
	cfg.Toolchain.CXX = "root-compiler"
	cfg.Toolchain.CUDA = "auto"
	cfg.Toolchain.CUDAMode = "rdc"
	cfg.Toolchain.CUDAArchitectures = []string{"sm_90"}
	if err := initWorkspaceMember(root, StarterMember, initChoices{CUDA: true}, os.Stdout); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, config.DefaultFileName)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	wc, err := config.ParseWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	member := wc.Member(StarterMember)
	if member.Toolchain.CXX != "root-compiler" || member.Toolchain.CUDAMode != "rdc" || !reflect.DeepEqual(member.Toolchain.CUDAArchitectures, []string{"sm_90"}) {
		t.Fatalf("starter member shadowed shared toolchain: %+v", member.Toolchain)
	}
}

func TestWorkspaceScaffoldRejectsEscapingMember(t *testing.T) {
	root := t.TempDir()
	for _, member := range []string{"../escape", ".", ""} {
		if err := initWorkspaceMember(root, member, initChoices{}, os.Stdout); err == nil {
			t.Fatalf("unsafe member scaffolded: %q", member)
		}
	}
}
