package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
)

func TestWorkspaceInitDiscoversMembersAndRefreshesPlan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	writeWorkspaceFile(t, root, "modules/base/trestle.toml", simpleMember)
	writeWorkspaceFile(t, root, "build/cache/trestle.toml", "ignored")
	writeWorkspaceFile(t, root, "modules/base/embedded/trestle.toml", "ignored")
	if err := InitWorkspace(t.Context(), path, WorkspaceInitOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	first := readGeneratedWorkspacePlan(t, root, "debug")
	if first.Backend != "trestle" || len(first.Members) != 1 || first.Members[0].Name != "modules/base" {
		t.Fatalf("unexpected discovery: %+v", first)
	}
	writeWorkspaceFile(t, root, "consumer/trestle.toml", `
schema_version = 7
[project]
name = "consumer"
[targets.consumer]
type = "executable"
sources = ["main.cpp"]
dependencies = [{ target = "modules/base:app", scope = "private" }]
`)
	if err := GenerateWorkspacePlan(t.Context(), path, "release", nil); err != nil {
		t.Fatal(err)
	}
	plan := readGeneratedWorkspacePlan(t, root, "release")
	if len(plan.Members) != 2 || plan.Members[0].Name != "modules/base" || plan.Members[1].Name != "consumer" || !slices.Equal(plan.Members[1].DependsOn, []string{"modules/base"}) {
		t.Fatalf("incorrect member order or dependencies: %+v", plan.Members)
	}
}

func TestCMakeWorkspaceBuildPreservesGeneratedDependencies(t *testing.T) {
	for _, exe := range []string{"cmake", "ninja", "ctest"} {
		if _, err := exec.LookPath(exe); err != nil {
			t.Skipf("%s unavailable: %v", exe, err)
		}
	}
	root := t.TempDir()
	// Use the monorepo layout so source autodetection and relative paths are exercised.
	writeWorkspaceFile(t, root, "llvm/CMakeLists.txt", `
cmake_minimum_required(VERSION 3.20)
project(workspace_fixture LANGUAGES NONE)
if(NOT FIXTURE_VALUE STREQUAL "a;b")
  message(FATAL_ERROR "cache value missing")
endif()
enable_testing()
add_subdirectory(generator)
add_subdirectory(cli components)
`)
	writeWorkspaceFile(t, root, "llvm/generator/CMakeLists.txt", `
add_custom_command(OUTPUT generated.txt
  COMMAND ${CMAKE_COMMAND} -E copy ${CMAKE_CURRENT_SOURCE_DIR}/input.txt generated.txt
  DEPENDS input.txt VERBATIM)
add_custom_target(generate_file DEPENDS generated.txt)
`)
	writeWorkspaceFile(t, root, "llvm/generator/input.txt", "generated content\n")
	writeWorkspaceFile(t, root, "llvm/cli/CMakeLists.txt", `
add_custom_command(OUTPUT output.txt
  COMMAND ${CMAKE_COMMAND} -E copy ../generator/generated.txt output.txt
  DEPENDS ${CMAKE_BINARY_DIR}/generator/generated.txt VERBATIM)
add_custom_target(tool ALL DEPENDS output.txt)
add_dependencies(tool generate_file)
add_test(NAME generated_file COMMAND ${CMAKE_COMMAND} -E compare_files output.txt ../generator/generated.txt)
`)
	path := filepath.Join(root, config.DefaultFileName)
	options := WorkspaceInitOptions{Cache: map[string]string{"FIXTURE_VALUE:STRING": "a;b"}, Jobs: 2}
	progress := func(line string) { t.Log(line) }
	if err := InitWorkspace(t.Context(), path, options, progress); err != nil {
		t.Fatal(err)
	}
	plan := readGeneratedWorkspacePlan(t, root, "debug")
	if plan.Backend != "cmake" || plan.Project != "workspace_fixture" {
		t.Fatalf("invalid plan: %+v", plan)
	}
	found := slices.ContainsFunc(plan.Members, func(member model.WorkspaceGroup) bool {
		return member.Name == "components" && member.Source == "cli" && member.Goal == "components/all" && slices.Contains(member.DependsOn, "generator")
	})
	if !found {
		t.Fatalf("missing member dependency: %+v", plan.Members)
	}
	output := filepath.Join(plan.BuildDir, "components", "output.txt")
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("plan ran a build: %v", err)
	}
	if err := BuildTargetsWithOptions(t.Context(), path, []string{"components:tool"}, false, false, progress); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"generated content\n", "updated content\n"} {
		if content == "updated content\n" {
			writeWorkspaceFile(t, root, "llvm/generator/input.txt", content)
			if err := BuildWorkspace(t.Context(), path, WorkspaceBuildOptions{Member: "components"}, progress); err != nil {
				t.Fatal(err)
			}
		}
		if data, err := os.ReadFile(output); err != nil || string(data) != content {
			t.Fatalf("generated output = %q, %v", data, err)
		}
	}
	if err := RunTests(t.Context(), path, TestSelection{Job: "generated_file"}, progress); err != nil {
		t.Fatal(err)
	}
}

func writeWorkspaceFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGeneratedWorkspacePlan(t *testing.T, root, profile string) model.WorkspacePlan {
	t.Helper()
	var plan model.WorkspacePlan
	data, err := os.ReadFile(filepath.Join(root, "build", profile, "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
