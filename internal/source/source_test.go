package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeCFamilyBuildAwareSource(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "src/main.c", `// #include "fake.h"
const char *value = "#include <fake.h>";
#include "engine.h"
#include <stdint.h>
#define HEADER(x) <x/foo.h>
#include HEADER(example)
#if defined(_WIN32) && !defined(NO_WINDOWS)
#include "win32.h"
TEST(Parser, Unicode) {}
#else
#include "posix.h"
#endif
int main() { return 0; }
`)
	writeSource(t, root, "include/engine.h", "#include \"core.h\"\n")
	writeSource(t, root, "include/core.h", "struct Core {};\n")
	writeSource(t, root, "include/win32.h", "#include <windows.h>\n")

	project, err := Analyze(root, "_WIN32")
	if err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "src", "main.c")
	info := project.Sources[mainPath]
	if len(info.EntryPoints) != 1 || info.EntryPoints[0].Name != "main" {
		t.Fatalf("unexpected entry points: %#v", info.EntryPoints)
	}
	if len(info.Tests) != 1 || info.Tests[0].Name != "Parser.Unicode" {
		t.Fatalf("unexpected tests: %#v", info.Tests)
	}
	if info.Confidence != ConfidenceLow {
		t.Fatalf("dynamic include should lower confidence: %s", info.Confidence)
	}
	dependencies := map[string]SourceDependency{}
	dynamic := 0
	for _, dependency := range info.Dependencies {
		if dependency.Kind == DynamicInclude {
			dynamic++
		}
		dependencies[dependency.Name] = dependency
	}
	if dynamic != 1 {
		t.Fatalf("dynamic include was not recorded: %#v", info.Dependencies)
	}
	if dependencies["engine.h"].Kind != LocalInclude || dependencies["engine.h"].Resolved == "" {
		t.Fatalf("local include was not resolved: %#v", dependencies["engine.h"])
	}
	win32Info := project.Sources[filepath.Join(root, "include", "win32.h")]
	if len(win32Info.Dependencies) != 1 || win32Info.Dependencies[0].Kind != SystemInclude || win32Info.Dependencies[0].Name != "windows.h" {
		t.Fatalf("nested system include was not parsed: %#v", win32Info.Dependencies)
	}
	if len(project.Consumers[filepath.Join(root, "include", "engine.h")]) != 1 {
		t.Fatalf("unexpected include consumers: %#v", project.Consumers)
	}
	headerInfo := project.Sources[filepath.Join(root, "include", "engine.h")]
	if headerInfo.Language != LanguageHeader || headerInfo.InferredLanguage != LanguageC {
		t.Fatalf("header language was not kept separate: %#v", headerInfo)
	}
	if len(project.Includes[filepath.Join(root, "include", "engine.h")]) != 1 {
		t.Fatalf("unexpected recursive includes: %#v", project.Includes)
	}
}

func TestAnalyzeModulesTestsAndLanguages(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "src/parser.cppm", "export module parser;\nimport core;\nimport :detail;\n")
	writeSource(t, root, "tests/parser_test.go", "package tests\nimport \"testing\"\nfunc TestParserUnicode(t *testing.T) {}\n")
	writeSource(t, root, "tests/test_parser.py", "def test_parser_unicode():\n    pass\n")
	writeSource(t, root, "src/lib.rs", "#[test]\nfn parses_unicode() {}\n")
	writeSource(t, root, "web/parser.test.ts", "import x from './x';\nit('parses unicode', () => {});\n")

	project, err := Analyze(root)
	if err != nil {
		t.Fatal(err)
	}
	moduleInfo := project.Sources[filepath.Join(root, "src", "parser.cppm")].Module
	if moduleInfo == nil || moduleInfo.Provides != "parser" || !moduleInfo.IsInterface {
		t.Fatalf("unexpected module info: %#v", moduleInfo)
	}
	if len(moduleInfo.Imports) != 2 || moduleInfo.Imports[0] != "core" || moduleInfo.Imports[1] != ":detail" {
		t.Fatalf("unexpected module imports: %#v", moduleInfo.Imports)
	}
	if project.Languages[LanguageGo] != 1 || project.Languages[LanguagePython] != 1 || project.Languages[LanguageRust] != 1 || project.Languages[LanguageTypeScript] != 1 {
		t.Fatalf("unexpected language counts: %#v", project.Languages)
	}
	if len(project.Sources[filepath.Join(root, "tests", "parser_test.go")].Tests) != 1 {
		t.Fatal("Go test was not detected")
	}
	if len(project.Sources[filepath.Join(root, "tests", "test_parser.py")].Tests) != 1 {
		t.Fatal("Python test was not detected")
	}
	if len(project.Sources[filepath.Join(root, "src", "lib.rs")].Tests) != 1 {
		t.Fatal("Rust test was not detected")
	}
	typescriptInfo := project.Sources[filepath.Join(root, "web", "parser.test.ts")]
	if len(typescriptInfo.Tests) != 1 {
		t.Fatal("TypeScript test was not detected")
	}
}

func writeSource(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
