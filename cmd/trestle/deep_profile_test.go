package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trestle/internal/buildanalyzer"
)

func TestDeepProfileRealCLI(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_DEEP") != "1" {
		t.Skip("set TRESTLE_TEST_DEEP=1 for actual compiler adapter builds")
	}
	tools := t.TempDir()
	binary := filepath.Join(tools, "trestle")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	compile := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compiling CLI: %v\n%s", err, output)
	}
	msvcSetup, cuda := os.Getenv("TRESTLE_TEST_MSVC_SETUP"), os.Getenv("TRESTLE_TEST_CUDA_ROOT")
	gcc := []string{"-toolchain", "g++"}
	if runtime.GOOS == "windows" {
		gcc = append(gcc, "-mode", "wsl", "-wsl-distribution", "Debian")
	}
	for _, fixture := range []struct {
		adapter string
		options []string
		skip    bool
	}{
		{"clang", []string{"-toolchain", "clang++"}, false},
		{"gcc", gcc, os.Getenv("TRESTLE_TEST_DEEP_GCC") != "1"},
		{"msvc", []string{"-toolchain", "cl", "-setup", msvcSetup}, msvcSetup == ""},
		{"nvcc", []string{"-toolchain", "cl", "-setup", msvcSetup, "-cuda", cuda}, msvcSetup == "" || cuda == ""},
	} {
		t.Run(fixture.adapter, func(t *testing.T) {
			if fixture.skip {
				t.Skip("the adapter toolchain is not configured for this test")
			}
			root := t.TempDir()
			invoke := func(args ...string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), binary, args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "VCPKG_ROOT=")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, output)
				}
			}
			invoke("init", "-name", "profile-test")
			if fixture.adapter == "nvcc" {
				source := filepath.Join(root, "src", "main.cu")
				if err := os.WriteFile(source, []byte("__global__ void kernel() {}\nint main() { return 0; }\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "trestle.toml")
				config, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				config = bytes.ReplaceAll(config, []byte("main.cpp"), []byte("main.cu"))
				if err := os.WriteFile(path, config, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			invoke(append([]string{"configure"}, fixture.options...)...)
			invoke("build", "--jobs=2")
			manifest := filepath.Join(root, "build", "debug", "build.ninja")
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			compdb := filepath.Join(root, "compile_commands.json")
			beforeDB, err := os.ReadFile(compdb)
			if err != nil {
				t.Fatal(err)
			}
			invoke("build", "--profile-deep", "--jobs=2")
			session, _, err := buildanalyzer.LatestReport(root)
			if err != nil || !session.DeepProfile || len(session.Executions) != 1 {
				t.Fatalf("deep profile: %+v, %v", session, err)
			}
			for _, path := range []struct {
				name string
				data []byte
			}{{manifest, before}, {compdb, beforeDB}} {
				after, err := os.ReadFile(path.name)
				if err != nil || !bytes.Equal(after, path.data) {
					t.Fatalf("regular build file changed: %s: %v", path.name, err)
				}
			}
			execution := session.Executions[0]
			for _, artifact := range execution.CompilerProfile {
				if artifact.Adapter == fixture.adapter && len(artifact.Events) > 0 &&
					strings.Contains(execution.Directory, "build-analyzer") {
					return
				}
			}
			t.Fatalf("no %s timing: %+v, warnings %v", fixture.adapter, execution.CompilerProfile, execution.Warnings)
		})
	}
	t.Run("workspace", func(t *testing.T) {
		root := t.TempDir()
		for _, name := range []string{
			"trestle.toml", "cli/trestle.toml", "cli/src/main.cpp", "core/trestle.toml",
			"core/src/greeter.cpp", "core/include/greeter/greeter.h", "core/tests/greeter_test.cpp",
		} {
			data, err := os.ReadFile(filepath.Join("../../examples/hello-workspace", name))
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(root, "trestle.toml")
		input, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Replace(input, []byte(`cxx = "auto"`), []byte(`cxx = "clang++"`), 1), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "build", "--profile-deep", "--jobs=2", "cli:hello")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "VCPKG_ROOT=")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("workspace deep build: %v\n%s", err, output)
		}
		session, _, err := buildanalyzer.LatestReport(root)
		if err != nil || !session.DeepProfile || len(session.Executions) != 2 {
			t.Fatalf("workspace deep profile: %+v, %v", session, err)
		}
		for _, execution := range session.Executions {
			if len(execution.CompilerProfile) == 0 || !strings.Contains(execution.Directory, "build-analyzer") {
				t.Fatalf("workspace member missing isolated compiler profile: %+v", execution)
			}
		}
		for _, path := range []string{"build", "compile_commands.json"} {
			if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
				t.Errorf("regular workspace artifact %s exists (%v)", path, err)
			}
		}
	})
}
