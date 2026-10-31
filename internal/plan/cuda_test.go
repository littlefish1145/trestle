package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"trestle/internal/config"
	"trestle/internal/graph"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

func TestCUDAPlanContracts(t *testing.T) {
	for _, kind := range []toolchain.Kind{toolchain.MSVC, toolchain.GCC} {
		for _, mode := range []cuda.CompileMode{cuda.WholeProgram, cuda.SeparateCompilation} {
			for _, targetType := range []string{"static", "shared", "executable"} {
				t.Run(fmt.Sprintf("%s/%d/%s", kind, mode, targetType), func(t *testing.T) {
					root := t.TempDir()
					source := filepath.Join(root, "kernel.cu")
					if err := os.WriteFile(source, []byte("__global__ void kernel() {}"), 0644); err != nil {
						t.Fatal(err)
					}
					cfg := config.Default("cuda")
					cfg.Build.BuildDir = filepath.Join(root, "build")
					cfg.Targets = map[string]config.Target{"app": {Type: targetType, Sources: []string{source}, CompileOptions: []string{"-O2"}}}
					project, err := graph.Resolve(cfg)
					if err != nil {
						t.Fatal(err)
					}
					host := toolchain.Toolchain{Kind: kind, CXX: "host", Archiver: "archive", Linker: "link"}
					ct := cuda.Toolchain{NVCC: cuda.NVCC{Path: "nvcc"}, Host: host, Toolkit: cuda.Toolkit{Root: filepath.Join(root, "CUDA SDK")}, Architectures: []string{"sm_80", "sm_90"}, Mode: mode}
					build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: host, CUDA: &ct})
					if err != nil {
						t.Fatal(err)
					}
					compile, device, link := 0, 0, 0
					for _, action := range build.Actions {
						args := strings.Join(action.Command.Args, " ")
						switch {
						case strings.HasPrefix(string(action.ID), "compile_"):
							compile++
							if action.Deps == "msvc" || action.Depfile == nil {
								t.Fatalf("CUDA requires a depfile on every host: %+v", action)
							}
							if !strings.Contains(args, "-MD -MF "+action.Depfile.Path) || !strings.Contains(args, "-Xcompiler -O2") {
								t.Fatalf("missing depfile or host flags: %s", args)
							}
							if targetType == "shared" && kind != toolchain.MSVC && !strings.Contains(args, "-Xcompiler -fPIC") {
								t.Fatalf("CUDA shared object lacks PIC: %s", args)
							}
						case strings.HasPrefix(string(action.ID), "device-link_"):
							device++
							for _, arch := range []string{"compute_80", "compute_90"} {
								if !strings.Contains(args, arch) {
									t.Fatalf("device link lost architecture: %s", args)
								}
							}
						case strings.HasPrefix(string(action.ID), "link_"):
							link++
							if !strings.Contains(args, "cudart_static") {
								t.Fatalf("CUDA runtime not linked: %s", args)
							}
						}
					}
					wantDevice := 0
					if mode == cuda.SeparateCompilation && targetType != "static" {
						wantDevice = 1
					}
					wantLink := 1
					if targetType == "static" {
						wantLink = 0
					}
					if compile != 1 || device != wantDevice || link != wantLink {
						t.Fatalf("compile/device/link=%d/%d/%d, expected 1/%d/%d", compile, device, link, wantDevice, wantLink)
					}
				})
			}
		}
	}
}

func TestCUDAStaticProviderGetsDeviceLinkedByCPPConsumer(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("consumer")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	for _, file := range []string{"main.cpp", "kernel.cu"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Targets = map[string]config.Target{"app": {Type: "executable", Sources: []string{filepath.Join(root, "main.cpp")}, Dependencies: []config.Dependency{{Target: "z_kernel"}}}, "z_kernel": {Type: "static", Sources: []string{filepath.Join(root, "kernel.cu")}}}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	host := toolchain.Toolchain{Kind: toolchain.GCC, CXX: "c++", Linker: "c++", Archiver: "ar"}
	ct := cuda.Toolchain{NVCC: cuda.NVCC{Path: "nvcc"}, Host: host, Mode: cuda.SeparateCompilation, Architectures: []string{"sm_80"}, Toolkit: cuda.Toolkit{Root: root}}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: host, CUDA: &ct})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range build.Actions {
		if a.ID == ActionID("device-link", "app") {
			found = true
			if len(a.Inputs) != 1 || !strings.Contains(a.Inputs[0], "z_kernel") {
				t.Fatalf("missing static device provider: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("C++ consumer never device-links its CUDA static provider")
	}
}
