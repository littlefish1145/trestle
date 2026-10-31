package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"trestle/internal/config"
)

func TestRealWorkspaceCleanSelectedMemberAndProfiles(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_NATIVE") != "1" {
		t.Skip("set TRESTLE_TEST_NATIVE=1 to require native workspace builds")
	}
	root := filepath.Join(t.TempDir(), "workspace space")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	// Copy only fixture inputs. A developer's example directory may already
	// contain ignored build outputs, which must never seed a clean-build test.
	exampleRoot := filepath.Join("..", "..", "examples", "hello-workspace")
	for _, name := range []string{"trestle.toml", "core/trestle.toml", "core/include/greeter/greeter.h", "core/src/greeter.cpp", "core/tests/greeter_test.cpp", "cli/trestle.toml", "cli/src/main.cpp"} {
		data, err := os.ReadFile(filepath.Join(exampleRoot, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, config.DefaultFileName)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Toolchain.CXX = integrationCompiler()
	cfg.Toolchain.MSVC = "auto"
	cfg.Toolchain.CacheDir = filepath.Join(root, ".trestle", "toolchain")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	for _, profile := range []string{"release", "debug"} {
		if err := BuildWorkspace(ctx, path, WorkspaceBuildOptions{Targets: []string{"cli:hello"}, Profile: profile}, nil); err != nil {
			integrationError(t, err)
		}
		program := filepath.Join(root, "build", profile, "cli", "bin", "hello-workspace")
		if runtime.GOOS == "windows" {
			program += ".exe"
		}
		output, err := exec.CommandContext(ctx, program).CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != "Hello, workspace!" {
			t.Fatalf("%s run: %s %v", profile, output, err)
		}
		data, err := os.ReadFile(filepath.Join(root, "compile_commands.json"))
		if err != nil {
			t.Fatal(err)
		}
		var entries []compileCommandEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Fatalf("aggregate lost provider/test sources: %+v", entries)
		}
		for _, entry := range entries {
			if !strings.Contains(filepath.ToSlash(entry.Directory), "/"+profile+"/") {
				t.Fatalf("wrong profile database: %+v", entry)
			}
		}
	}
	if err := RunWorkspaceTests(ctx, path, "core", TestSelection{All: true}, nil); err != nil {
		integrationError(t, err)
	}
	// A public provider header change must rebuild consumers from a selected build.
	objectDir := filepath.Join(root, "build", "debug", "cli", "obj", "hello")
	objects, err := os.ReadDir(objectDir)
	if err != nil {
		t.Fatal(err)
	}
	objectTimes := map[string]time.Time{}
	for _, object := range objects {
		if ext := filepath.Ext(object.Name()); ext == ".o" || ext == ".obj" {
			info, err := object.Info()
			if err != nil {
				t.Fatal(err)
			}
			objectTimes[object.Name()] = info.ModTime()
		}
	}
	if len(objectTimes) != 1 {
		t.Fatalf("expected one consumer object, got %v", objectTimes)
	}
	header := filepath.Join(root, "core", "include", "greeter", "greeter.h")
	data, err := os.ReadFile(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(header, append(data, []byte("\n// changed public header\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if err := BuildWorkspace(ctx, path, WorkspaceBuildOptions{Member: "cli", Targets: []string{"hello"}}, nil); err != nil {
		integrationError(t, err)
	}
	for object, before := range objectTimes {
		info, err := os.Stat(filepath.Join(objectDir, object))
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().After(before) {
			t.Fatalf("public header change did not recompile consumer %s", object)
		}
	}
}

func TestRealCUDAStarterAndIncrementalBuild(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_CUDA") != "1" {
		t.Skip("set TRESTLE_TEST_CUDA=1 to require an installed CUDA toolkit and supported host compiler")
	}
	if _, err := exec.LookPath("nvcc"); err != nil {
		t.Fatalf("CUDA integration explicitly enabled: %v", err)
	}
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"whole", "rdc"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "CUDA project space")
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, config.DefaultFileName)
				choices := initChoices{CUDA: true, Workspace: workspace}
				cfg, _, err := buildInitConfig(root, "cuda-demo", choices, os.Stdout)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Toolchain.CUDAMode = mode
				cfg.Toolchain.CacheDir = filepath.Join(root, ".trestle", "toolchain")
				if compiler := os.Getenv("TRESTLE_TEST_CUDA_COMPILER"); compiler != "" {
					cfg.Toolchain.CXX = compiler
				}
				if arch := os.Getenv("TRESTLE_TEST_CUDA_ARCH"); arch != "" {
					cfg.Toolchain.CUDAArchitectures = []string{arch}
				}
				if workspace {
					if err := initWorkspaceMember(root, StarterMember, choices, os.Stdout); err != nil {
						t.Fatal(err)
					}
				}
				if err := config.Save(path, cfg); err != nil {
					t.Fatal(err)
				}
				memberRoot := root
				memberPath := path
				if workspace {
					memberRoot = filepath.Join(root, StarterMember)
					memberPath = filepath.Join(memberRoot, config.DefaultFileName)
				}
				memberCfg, err := config.Load(memberPath)
				if err != nil {
					t.Fatal(err)
				}
				memberCfg.Toolchain = cfg.Toolchain
				memberCfg.Toolchain.CUDAMode = mode
				memberCfg.Targets["cuda_check"] = config.Target{Type: "executable", Sources: []string{"src/check.cpp", "src/cuda/check.cu", "src/cuda/device.cu"}, IncludeDirs: []string{"src/cuda"}, Defines: []string{fmt.Sprintf("USE_RDC=%d", map[bool]int{false: 0, true: 1}[mode == "rdc"])}}
				memberCfg.Build.DefaultTargets = append(memberCfg.Build.DefaultTargets, "cuda_check")
				if err := config.Save(memberPath, memberCfg); err != nil {
					t.Fatal(err)
				}
				files := map[string]string{"src/check.cpp": "#include <iostream>\nextern int cudaAnswer();\nint main(){std::cout << cudaAnswer();}\n", "src/cuda/answer.h": "#define ANSWER 42\n", "src/cuda/device.cu": "__device__ int deviceAnswer(){return 7;}\n", "src/cuda/check.cu": "#include <cuda_runtime.h>\n#include \"answer.h\"\n#if USE_RDC\nextern __device__ int deviceAnswer();\n__global__ void checkKernel(int *p){*p=deviceAnswer();}\n#else\n__global__ void checkKernel(int *p){*p=7;}\n#endif\nint cudaAnswer(){return ANSWER;}\n"}
				for name, data := range files {
					if err := os.WriteFile(filepath.Join(memberRoot, filepath.FromSlash(name)), []byte(data), 0644); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
				defer cancel()
				build := func() {
					t.Helper()
					var err error
					if workspace {
						err = BuildWorkspace(ctx, path, WorkspaceBuildOptions{All: true}, nil)
					} else {
						err = BuildTargetsWithProgress(ctx, path, nil, true, nil)
					}
					if err != nil {
						integrationError(t, err)
					}
				}
				build()
				buildDir := filepath.Join(root, "build", "debug")
				if workspace {
					buildDir = filepath.Join(buildDir, StarterMember)
				}
				program := filepath.Join(buildDir, "bin", "cuda_check")
				if runtime.GOOS == "windows" {
					program += ".exe"
				}
				run := func(want string) {
					t.Helper()
					output, err := exec.CommandContext(ctx, program).CombinedOutput()
					if err != nil || string(output) != want {
						t.Fatalf("CUDA host executable: %q %v, want %s", output, err, want)
					}
				}
				run("42")
				before, err := os.Stat(program)
				if err != nil {
					t.Fatal(err)
				}
				build()
				after, err := os.Stat(program)
				if err != nil {
					t.Fatal(err)
				}
				if !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("unchanged CUDA project rebuilt its executable")
				}
				if err := os.WriteFile(filepath.Join(memberRoot, "src", "cuda", "answer.h"), []byte("#define ANSWER 43\n"), 0644); err != nil {
					t.Fatal(err)
				}
				build()
				run("43")
			})
		}
	}
}
