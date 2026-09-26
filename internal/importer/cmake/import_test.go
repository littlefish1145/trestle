package cmake

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestImportUsesFileAPICodemodel(t *testing.T) {
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake is not installed")
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("ninja is not installed")
	}
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CMakeLists.txt", `cmake_minimum_required(VERSION 3.20)
project(file_api_fixture LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
add_library(core STATIC src/core.cpp)
target_include_directories(core PUBLIC include)
target_compile_definitions(core PRIVATE CORE_BUILD=1)
add_custom_command(OUTPUT generated.cpp COMMAND ${CMAKE_COMMAND} -E copy ${CMAKE_CURRENT_SOURCE_DIR}/src/generated.in.cpp generated.cpp DEPENDS src/generated.in.cpp)
add_library(generated STATIC ${CMAKE_CURRENT_BINARY_DIR}/generated.cpp)
add_executable(app src/main.cpp)
target_link_libraries(app PRIVATE core user32)
add_subdirectory(vendor/cpp-httplib)
enable_testing()
add_test(NAME app COMMAND app --smoke)
`)
	write("src/core.cpp", "int core() { return 7; }\n")
	write("src/generated.in.cpp", "int generated() { return 8; }\n")
	write("src/main.cpp", "int core(); int main() { return core() == 7 ? 0 : 1; }\n")
	write("include/core.h", "int core();\n")
	write("vendor/cpp-httplib/CMakeLists.txt", "add_library(cpp-httplib STATIC httplib.cpp)\n")
	write("vendor/cpp-httplib/httplib.cpp", "int httplib_fixture() { return 1; }\n")
	configPath := filepath.Join(root, config.DefaultFileName)
	result, err := Import(context.Background(), root, configPath, func(line string) { t.Log(line) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "file_api_fixture" || result.Targets != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Targets["core"].Type != "static" || cfg.Targets["app"].Type != "test" {
		t.Fatalf("unexpected targets: %#v", cfg.Targets)
	}
	if cfg.Targets["app"].CXXStandard != "c++17" {
		t.Fatalf("CMake language standard was not imported: %#v", cfg.Targets["app"])
	}
	if cfg.Targets["app"].TestGroup != "cmake" || len(cfg.Targets["app"].TestArgs) != 1 || cfg.Targets["app"].TestArgs[0] != "--smoke" {
		t.Fatalf("CTest job was not imported: %#v", cfg.Targets["app"])
	}
	if len(cfg.Targets["app"].Dependencies) != 1 || cfg.Targets["app"].Dependencies[0].Target != "core" {
		t.Fatalf("app dependency was not imported: %#v", cfg.Targets["app"].Dependencies)
	}
	if !containsFold(cfg.Targets["app"].Libraries, "user32") || containsFold(cfg.Targets["app"].Libraries, "core") {
		t.Fatalf("external link library was not imported: %#v", cfg.Targets["app"].Libraries)
	}
	vendorSources := cfg.Targets["cpp-httplib"].Sources
	if len(vendorSources) != 1 || vendorSources[0] != filepath.Join("vendor", "cpp-httplib", "httplib.cpp") {
		t.Fatalf("nested target source was resolved incorrectly: %#v", vendorSources)
	}
	if _, err := os.Stat(filepath.Join(root, vendorSources[0])); err != nil {
		t.Fatalf("nested target source does not exist: %v", err)
	}
	generatedSources := cfg.Targets["generated"].Sources
	if len(generatedSources) != 1 {
		t.Fatalf("generated source was not imported: %#v", generatedSources)
	}
	if _, err := os.Stat(filepath.Join(root, generatedSources[0])); err != nil {
		t.Fatalf("generated source was not materialized: %v", err)
	}
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

func TestObjectLibraryMapsToStaticArchive(t *testing.T) {
	targetType, supported := mapTargetType("OBJECT_LIBRARY")
	if !supported || targetType != "static" {
		t.Fatalf("object library mapping = %q, %v", targetType, supported)
	}
}
