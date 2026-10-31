package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"trestle/internal/config"
	"trestle/internal/model"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
)

func TestRealWSLVulkanBuildAndIncrementalDependencies(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_WSL_VULKAN") != "1" {
		t.Skip("set TRESTLE_TEST_WSL_VULKAN=1 to require real WSL shader compilation and SPIR-V validation")
	}
	if runtime.GOOS != "windows" {
		t.Fatal("Windows-driven WSL Vulkan integration requires Windows")
	}
	distribution := os.Getenv("TRESTLE_WSL_DISTRIBUTION")
	if distribution == "" {
		t.Fatal("TRESTLE_WSL_DISTRIBUTION is required")
	}
	runRealVulkanBuild(t, distribution)
}
func TestRealVulkanNativeBuildAndIncrementalDependencies(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_VULKAN") != "1" {
		t.Skip("set TRESTLE_TEST_VULKAN=1 to require actual shader compilers and SPIR-V validation")
	}
	runRealVulkanBuild(t, "")
}
func runRealVulkanBuild(t *testing.T, distribution string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	var sdk vulkan.SDK
	var err error
	if distribution != "" {
		sdk, err = vulkan.DetectWSL(ctx, distribution, os.Getenv("TRESTLE_TEST_VULKAN_ROOT"))
	} else {
		root := os.Getenv("TRESTLE_TEST_VULKAN_ROOT")
		if root == "" {
			root = os.Getenv("VULKAN_SDK")
		}
		sdk, err = vulkan.DetectRoot(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	var validator string
	if distribution != "" {
		validator, err = toolchain.WSLExecutable(ctx, distribution, "spirv-val")
	} else {
		validator, err = exec.LookPath("spirv-val")
	}
	if err != nil {
		t.Fatalf("SPIR-V validator explicitly required: %v", err)
	}
	t.Logf("distribution=%s SDK=%s version=%s glslc=%s glslang=%s", distribution, sdk.Root, sdk.Version, sdk.Glslc, sdk.Glslang)
	for _, tool := range []string{"glslc", "glslangValidator"} {
		for _, workspace := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/workspace=%t", tool, workspace), func(t *testing.T) {
				if tool == "glslc" && sdk.Glslc == "" || tool == "glslangValidator" && sdk.Glslang == "" {
					t.Fatalf("both real shader compilers are required; missing %s", tool)
				}
				root := filepath.Join(t.TempDir(), "Vulkan project space")
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
				memberRoot := root
				if workspace {
					memberRoot = filepath.Join(root, "shaders")
				}
				prefix, includeName := "shader source", "include space"
				if tool == "glslc" {
					prefix, includeName = "shader_source", "include"
				}
				shaderDir := filepath.Join(memberRoot, prefix)
				includeDir := filepath.Join(shaderDir, includeName)
				if err := os.MkdirAll(includeDir, 0755); err != nil {
					t.Fatal(err)
				}
				compute := `#version 450
#extension GL_GOOGLE_include_directive : require
#include "constants.glsl"
#ifndef LOCAL_SIZE
#error LOCAL_SIZE must come from target defines
#endif
layout(local_size_x=LOCAL_SIZE) in;
layout(set=0,binding=0,std430) buffer Result { uint value; } result;
void main() { result.value = VALUE; }
`
				files := map[string]string{prefix + "/main.comp": compute, prefix + "/triangle.vert": starterTemplate("triangle.vert"), prefix + "/triangle.frag": starterTemplate("triangle.frag"), prefix + "/" + includeName + "/constants.glsl": "#define VALUE 7u\n"}
				for name, content := range files {
					if err := os.WriteFile(filepath.Join(memberRoot, filepath.FromSlash(name)), []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				cfg := config.Default("vulkan")
				cfg.Toolchain.Vulkan = sdk.Root
				cfg.Toolchain.VulkanExecution = "native"
				if distribution != "" {
					cfg.Toolchain.VulkanExecution = "wsl"
				}
				cfg.Toolchain.VulkanWSLDistribution = distribution
				// Pure shader projects keep a native host configuration. Its distribution
				// fallback deliberately differs, so action paths must use the SDK runner.
				cfg.Toolchain.WSLDistribution = "Unselected-Distribution-Must-Not-Be-Used"
				cfg.Toolchain.CacheDir = filepath.Join(root, ".trestle", "toolchain")
				cfg.Targets = map[string]config.Target{
					"compute":  {Type: "shader", Sources: []string{prefix + "/main.comp"}, ShaderStage: "comp", ShaderTargetEnv: "vulkan1.3", ShaderTool: tool, IncludeDirs: []string{includeDir}, Defines: []string{"LOCAL_SIZE=1"}},
					"vertex":   {Type: "shader", Sources: []string{prefix + "/triangle.vert"}, ShaderStage: "vert", ShaderTargetEnv: "vulkan1.3", ShaderTool: tool},
					"fragment": {Type: "shader", Sources: []string{prefix + "/triangle.frag"}, ShaderStage: "frag", ShaderTargetEnv: "vulkan1.3", ShaderTool: tool},
				}
				cfg.Build.DefaultTargets = []string{"compute", "vertex", "fragment"}
				path := filepath.Join(root, config.DefaultFileName)
				if workspace {
					memberCfg := cfg
					if err := writeStarterMemberConfig(filepath.Join(memberRoot, config.DefaultFileName), memberCfg); err != nil {
						t.Fatal(err)
					}
					cfg.Workspace = &config.Workspace{Members: []string{"shaders"}}
					cfg.Targets = map[string]config.Target{}
					cfg.Build.DefaultTargets = nil
				}
				if err := config.Save(path, cfg); err != nil {
					t.Fatal(err)
				}
				build := func() error {
					if workspace {
						return BuildWorkspace(ctx, path, WorkspaceBuildOptions{All: true}, nil)
					}
					return BuildTargetsWithProgress(ctx, path, nil, true, nil)
				}
				requireBuild := func() {
					t.Helper()
					if err := build(); err != nil {
						integrationError(t, err)
					}
				}
				requireBuild()
				buildDir := filepath.Join(root, "build", "debug")
				if workspace {
					buildDir = filepath.Join(buildDir, "shaders")
				}
				validate := func(target string) []byte {
					t.Helper()
					file := filepath.Join(buildDir, "bin", target+".spv")
					data, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					if len(data) < 20 || len(data)%4 != 0 || binary.LittleEndian.Uint32(data[:4]) != 0x07230203 {
						t.Fatalf("invalid SPIR-V header %s: %x", file, data)
					}
					exe, args := validator, []string{"--target-env", "vulkan1.3", file}
					if distribution != "" {
						linuxPath, err := toolchain.WSLPath(ctx, distribution, file)
						if err != nil {
							t.Fatal(err)
						}
						exe, args, err = toolchain.WSLRunner(distribution)
						if err != nil {
							t.Fatal(err)
						}
						args = append(args, "--exec", validator, "--target-env", "vulkan1.3", linuxPath)
					}
					if output, err := exec.CommandContext(ctx, exe, args...).CombinedOutput(); err != nil {
						t.Fatalf("spirv-val %s: %s %v", target, output, err)
					}

					return data
				}
				original := validate("compute")
				validate("vertex")
				validate("fragment")
				artifact := filepath.Join(buildDir, "bin", "compute.spv")
				before, err := os.Stat(artifact)
				if err != nil {
					t.Fatal(err)
				}
				requireBuild()
				after, err := os.Stat(artifact)
				if err != nil {
					t.Fatal(err)
				}
				if !after.ModTime().Equal(before.ModTime()) {
					depfile, _ := os.ReadFile(artifact + ".d")
					explain, explainErr := exec.Command("ninja", "-C", buildDir, "-d", "explain", "-n").CombinedOutput()
					t.Fatalf("unchanged shader inputs caused recompilation: depfile=%q explain=%s %v", depfile, explain, explainErr)
				}
				include := filepath.Join(includeDir, "constants.glsl")
				if err := os.WriteFile(include, []byte("#define VALUE 8u\n"), 0644); err != nil {
					t.Fatal(err)
				}
				requireBuild()
				updated := validate("compute")
				if bytes.Equal(original, updated) {
					t.Fatal("included GLSL change did not change compiled SPIR-V")
				}
				source := filepath.Join(shaderDir, "main.comp")
				if err := os.WriteFile(source, []byte(compute+"\nTHIS IS INVALID GLSL\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := build(); err == nil {
					t.Fatal("invalid GLSL was reported as successful")
				}
				if err := os.WriteFile(source, []byte(compute), 0644); err != nil {
					t.Fatal(err)
				}
				requireBuild()
				if repaired := validate("compute"); !bytes.Equal(repaired, updated) {
					t.Fatal("repair changed deterministic SPIR-V output")
				}
				rawBefore, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := SetVulkanConnection(path, true, filepath.Join(root, "missing-sdk-for-negative-test"), cfg.Toolchain.VulkanExecution, distribution); err == nil {
					t.Fatal("explicit missing WSL SDK accepted")
				}
				rawAfter, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(rawBefore, rawAfter) {
					t.Fatal("failed SDK connection modified configuration")
				}
			})
		}
	}
}

func TestDetectVulkanSDKPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := detectVulkanSDK(ctx, config.Config{}, model.ResolvedProject{}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
