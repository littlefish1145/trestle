package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/graph"
	"trestle/internal/model"
	"trestle/internal/toolchain"
)

func TestMSVCSharedDependencyUsesImportLibrary(t *testing.T) {
	id := model.TargetID("core")
	project := model.ResolvedProject{ByID: map[model.TargetID]model.ResolvedTarget{
		id: {Target: model.Target{ID: id, Type: model.SharedLibrary}},
	}}
	inputs := relativeTargets("C:/work", "C:/work/build", project, []model.TargetID{id}, map[model.TargetID]string{id: "bin/core.dll"}, toolchain.Toolchain{Kind: toolchain.MSVC})
	if len(inputs) != 1 || inputs[0] != "bin/core.lib" {
		t.Fatalf("shared dependency should link its import library: %#v", inputs)
	}
}

func TestBuildUsesTargetLanguageFlagsOnlyForMatchingSource(t *testing.T) {
	root := t.TempDir()
	cSource := filepath.Join(root, "main.c")
	cppSource := filepath.Join(root, "worker.cpp")
	for _, source := range []string{cSource, cppSource} {
		if err := os.WriteFile(source, []byte("int main(){return 0;}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default("flags")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{
		"app":    {Type: "executable", Sources: []string{cSource}, CFlags: []string{"-Wc-only"}, CXXFlags: []string{"-wrong-cxx"}, LinkOptions: []string{"-Wl,app-only"}},
		"worker": {Type: "executable", Sources: []string{cppSource}, CFlags: []string{"-wrong-c"}, CXXFlags: []string{"-Wcxx-only"}},
	}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.GCC, CC: "cc", CXX: "c++", Linker: "c++", Archiver: "ar"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[ID]bool{}
	for _, action := range build.Actions {
		args := strings.Join(action.Command.Args, " ")
		switch action.ID {
		case ActionID("compile", "app", sourceID(cSource)):
			seen[action.ID] = true
			if !strings.Contains(args, "-Wc-only") || strings.Contains(args, "-wrong-cxx") {
				t.Fatalf("C target flags incorrect: %s", args)
			}
		case ActionID("compile", "worker", sourceID(cppSource)):
			seen[action.ID] = true
			if !strings.Contains(args, "-Wcxx-only") || strings.Contains(args, "-wrong-c") {
				t.Fatalf("C++ target flags incorrect: %s", args)
			}
		case ActionID("link", "app"):
			seen[action.ID] = true
			if !strings.Contains(args, "-Wl,app-only") {
				t.Fatalf("target link flag missing: %s", args)
			}
		case ActionID("link", "worker"):
			seen[action.ID] = true
			if strings.Contains(args, "-Wl,app-only") {
				t.Fatalf("target link flag leaked: %s", args)
			}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected four compile/link actions, found %d", len(seen))
	}
}

func TestBuildKeepsDependenciesThatSortAfterConsumer(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"ggml.cpp", "base.cpp", "cpu.cpp"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("int value_"+name[:len(name)-4]+";\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default("ordering")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{
		"ggml":      {Type: "shared", Sources: []string{filepath.Join(root, "ggml.cpp")}, Dependencies: []config.Dependency{{Target: "ggml-base", Scope: "private"}, {Target: "ggml-cpu", Scope: "private"}}},
		"ggml-base": {Type: "shared", Sources: []string{filepath.Join(root, "base.cpp")}},
		"ggml-cpu":  {Type: "shared", Sources: []string{filepath.Join(root, "cpu.cpp")}},
	}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.MSVC, CC: "cl.exe", CXX: "cl.exe", Linker: "cl.exe", Archiver: "lib.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range build.Actions {
		if action.ID != ActionID("link", "ggml") {
			continue
		}
		joined := " " + filepath.ToSlash(action.Inputs[0])
		for _, input := range action.Inputs[1:] {
			joined += " " + filepath.ToSlash(input)
		}
		if !containsAll(joined, "ggml-base.lib", "ggml-cpu.lib") {
			t.Fatalf("late-sorted dependencies missing from link inputs: %s", joined)
		}
		return
	}
	t.Fatal("ggml link action was not generated")
}

func TestStaticArchiveDoesNotEmbedDependencyLibraries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"base.cpp", "wrapper.cpp"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("int value;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default("archive")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{
		"base":    {Type: "static", Sources: []string{filepath.Join(root, "base.cpp")}},
		"wrapper": {Type: "static", Sources: []string{filepath.Join(root, "wrapper.cpp")}, Dependencies: []config.Dependency{{Target: "base", Scope: "private"}}},
	}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.MSVC, CC: "cl.exe", CXX: "cl.exe", Linker: "cl.exe", Archiver: "lib.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range build.Actions {
		if action.ID == ActionID("archive", "wrapper") {
			if containsAll(strings.Join(action.Inputs, " "), "base.lib") {
				t.Fatalf("static archive contains dependency library: %#v", action.Inputs)
			}
			return
		}
	}
	t.Fatal("wrapper archive action was not generated")
}

func TestGNUSharedLibraryObjectsUsePIC(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "shared.c")
	if err := os.WriteFile(source, []byte("int exported(void) { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("pic")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{"shared": {Type: "shared", Sources: []string{source}}}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.GCC, CC: "gcc", CXX: "g++", Linker: "g++", Archiver: "ar"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range build.Actions {
		if action.ID == ActionID("compile", "shared", sourceID(source)) {
			if !containsAll(strings.Join(action.Command.Args, " "), "-fPIC") {
				t.Fatalf("shared library compile command is missing -fPIC: %#v", action.Command.Args)
			}
			return
		}
	}
	t.Fatal("shared library compile action was not generated")
}

func TestMSVCExportAllSymbolsCreatesDefinitionFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "shared.cpp")
	if err := os.WriteFile(source, []byte("int exported() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("exports")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{"shared": {Type: "shared", Sources: []string{source}, ExportAllSymbols: true}}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.MSVC, CC: "cl.exe", CXX: "cl.exe", Linker: "cl.exe", Archiver: "lib.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range build.Actions {
		if action.ID == ActionID("exports", "shared") {
			if !action.ResponseFile || len(action.Outputs) != 1 || filepath.Ext(action.Outputs[0]) != ".def" {
				t.Fatalf("invalid export definition action: %#v", action)
			}
			return
		}
	}
	t.Fatal("export definition action was not generated")
}

func TestBuildCopiesRuntimeFilesAndKeepsExternalAbsolutePath(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	source := filepath.Join(root, "main.cpp")
	runtimeFile := filepath.Join(external, "glfw3.dll")
	if err := os.WriteFile(source, []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeFile, []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("app")
	cfg.Build.BuildDir = filepath.Join(root, "build")
	cfg.Targets = map[string]config.Target{"app": {Type: "executable", Sources: []string{source}}}
	project, err := graph.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	project.Targets[0].LinkSelf.RuntimeFiles = []string{runtimeFile}
	project.ByID[project.Targets[0].ID] = project.Targets[0]
	build, err := Build(cfg, project, Options{Root: root, BuildDir: cfg.Build.BuildDir, Toolchain: toolchain.Toolchain{Kind: toolchain.MSVC, CC: "cl.exe", CXX: "cl.exe", Linker: "cl.exe", Archiver: "lib.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	expectedSource, err := filepath.Rel(filepath.Join(cfg.Build.BuildDir, cfg.Build.Profile), runtimeFile)
	if err != nil {
		t.Fatal(err)
	}
	expectedSource = filepath.ToSlash(expectedSource)
	expectedOutput := "bin/glfw3.dll"
	if got := build.RuntimeOutputs[model.TargetID("app")]; len(got) != 1 || got[0] != expectedOutput {
		t.Fatalf("runtime outputs = %#v, want %q", got, expectedOutput)
	}
	for _, action := range build.Actions {
		if action.ID == ActionID("runtime", "app", "glfw3.dll") {
			if len(action.Inputs) != 1 || action.Inputs[0] != expectedSource || len(action.Outputs) != 1 || action.Outputs[0] != expectedOutput {
				t.Fatalf("invalid runtime copy action: %#v", action)
			}
			return
		}
	}
	t.Fatal("runtime copy action was not generated")
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
