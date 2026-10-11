package cmake

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("expected generated CMakeLists.txt to contain %q; got:\n%s", want, content)
	}
}

func TestGenerateExportsTargetsScopesAndTests(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "src/main.cpp", "int main() { return 0; }\n")
	writeFixture(t, dir, "src/util.cpp", "int util() { return 0; }\n")
	writeFixture(t, dir, "core/core.cpp", "int core() { return 0; }\n")
	writeFixture(t, dir, "tests/smoke.c", "int main(void) { return 0; }\n")

	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "demo"},
		Build: config.Build{
			CStandard:       "c17",
			CXXStandard:     "c++20",
			CompileFlags:    []string{"-Wall"},
			CFlags:          []string{"-Wextra"},
			LinkFlags:       []string{"-Wl,--as-needed"},
			DefaultTargets:  []string{"app"},
			CompileCommands: "compile_commands.json",
		},
		Targets: map[string]config.Target{
			"core": {
				Type:        "static",
				Sources:     []string{"core/*.cpp"},
				OutputName:  "core_lib",
				IncludeDirs: []string{"core/include dir with space"},
				Defines:     []string{"CORE_API=1"},
			},
			"app": {
				Type:               "executable",
				Sources:            []string{"src/*.cpp"},
				PrivateIncludeDirs: []string{"src"},
				PrivateDefines:     []string{"APP_DEBUG"},
				CXXFlags:           []string{"-fcoroutines"},
				Dependencies: []config.Dependency{
					{Target: "core", Scope: "public"},
					{Package: "fmt", Scope: "private"},
				},
			},
			"smoke": {
				Type:           "test",
				Sources:        []string{"tests/*.c"},
				TestGroup:      "fast",
				TestWorkingDir: "tests",
				TestArgs:       []string{"--verbose"},
			},
		},
		Packages: map[string]config.Package{
			"fmt": {Provider: "vcpkg", Port: "fmt", Libraries: []string{"fmt"}, IncludeDirs: []string{"include/fmt"}},
		},
	}

	result, err := Generate(cfg, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	content := result.Content
	assertContains(t, content, "cmake_minimum_required(VERSION 3.20)")
	assertContains(t, content, "project(demo LANGUAGES C CXX)")
	assertContains(t, content, "set(CMAKE_C_STANDARD 17)")
	assertContains(t, content, "set(CMAKE_CXX_STANDARD 20)")
	assertContains(t, content, "set(CMAKE_EXPORT_COMPILE_COMMANDS ON)")
	assertContains(t, content, "add_compile_options(-Wall)")
	assertContains(t, content, `"$<$<COMPILE_LANGUAGE:C>:-Wextra>"`)
	assertContains(t, content, "add_link_options(-Wl,--as-needed)")
	assertContains(t, content, "add_library(core STATIC")
	assertContains(t, content, "core/core.cpp")
	assertContains(t, content, "set_target_properties(core PROPERTIES OUTPUT_NAME core_lib)")
	assertContains(t, content, `target_include_directories(core PUBLIC "core/include dir with space")`)
	assertContains(t, content, "target_compile_definitions(core PUBLIC CORE_API=1)")
	assertContains(t, content, "add_executable(app")
	assertContains(t, content, "target_include_directories(app PRIVATE src)")
	assertContains(t, content, "target_compile_definitions(app PRIVATE APP_DEBUG)")
	assertContains(t, content, `target_compile_options(app PRIVATE "$<$<COMPILE_LANGUAGE:CXX>:-fcoroutines>")`)
	assertContains(t, content, "target_link_libraries(app PUBLIC core)")
	assertContains(t, content, "target_include_directories(app PRIVATE include/fmt)")
	assertContains(t, content, "target_link_libraries(app PRIVATE fmt)")
	assertContains(t, content, "enable_testing()")
	assertContains(t, content, "add_test(NAME smoke COMMAND smoke --verbose)")
	assertContains(t, content, "set_tests_properties(smoke PROPERTIES LABELS fast WORKING_DIRECTORY tests)")
	assertContains(t, content, "# trestle default targets: app")

	if result.Targets != 3 {
		t.Fatalf("expected 3 exported targets, got %d", result.Targets)
	}
}

func TestGenerateSkipsShaderTargetsWithWarning(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "shaders/triangle.vert", "#version 450\n")
	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "shaded"},
		Targets: map[string]config.Target{
			"triangle": {Type: "shader", Sources: []string{"shaders/*.vert"}, ShaderStage: "vertex"},
		},
	}
	result, err := Generate(cfg, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Targets != 0 {
		t.Fatalf("shader targets must not be exported, got %d", result.Targets)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", result.Warnings)
	}
	assertContains(t, result.Content, `# shader target "triangle" (stage vertex) is not exported`)
}

func TestGenerateRejectsUnmatchedSourcePattern(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "empty"},
		Targets: map[string]config.Target{
			"app": {Type: "executable", Sources: []string{"src/*.cpp"}},
		},
	}
	_, err := Generate(cfg, Options{Root: dir})
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("expected unmatched pattern error, got %v", err)
	}
}

func TestGenerateKeepsTrestleOnlySectionsAsComments(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "src/main.cpp", "int main() { return 0; }\n")
	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "notes"},
		Targets: map[string]config.Target{
			"app": {Type: "executable", Sources: []string{"src/main.cpp"}, When: "os == windows"},
		},
		Rules: []config.Rule{{When: "profile == release", Target: "app"}},
		Tasks: map[string]config.Task{"fmt": {Command: []string{"clang-format"}}},
	}
	result, err := Generate(cfg, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Content, "# trestle condition: when = os == windows")
	assertContains(t, result.Content, "# trestle-only sections are not exported to CMake: rules (1), tasks (1)")
}

func TestGenerateWarnsAboutUnrecognizedStandards(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "src/main.c", "int main(void) { return 0; }\n")
	cfg := config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Project:       config.Project{Name: "odd"},
		Build:         config.Build{CStandard: "gnu11", CXXStandard: "c++20"},
		Targets: map[string]config.Target{
			"app": {Type: "executable", Sources: []string{"src/main.c"}},
		},
	}
	result, err := Generate(cfg, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "gnu11") {
		t.Fatalf("expected a warning about gnu11, got %#v", result.Warnings)
	}
	if strings.Contains(result.Content, "set(CMAKE_C_STANDARD") {
		t.Fatalf("unrecognized C standard must not be exported:\n%s", result.Content)
	}
	assertContains(t, result.Content, "set(CMAKE_CXX_STANDARD 20)")
}

func TestStandardParsing(t *testing.T) {
	for value, want := range map[string]string{"c17": "17", "c11": "11", "C99": "99", "c++20": "", "gnu11": "", "": ""} {
		if got := cStandard(value); got != want {
			t.Fatalf("cStandard(%q) = %q, want %q", value, got, want)
		}
	}
	for value, want := range map[string]string{"c++20": "20", "cxx17": "17", "C++14": "14", "20": "", "c++": "", "c++2a": ""} {
		if got := cxxStandard(value); got != want {
			t.Fatalf("cxxStandard(%q) = %q, want %q", value, got, want)
		}
	}
}
