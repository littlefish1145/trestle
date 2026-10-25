package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLowering(t *testing.T) {
	tests := []struct {
		name string
		tc   Toolchain
		kind string
	}{
		{"gcc", Toolchain{Kind: GCC, CXX: "g++"}, "-std=c++20"},
		{"msvc", Toolchain{Kind: MSVC, CXX: "cl.exe"}, "/std:c++20"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, args, err := test.tc.Compile(CompileSpec{Source: "main.cpp", Output: "main.obj", CXXStandard: "c++20", Includes: []string{"include"}, Defines: []string{"MODE=1"}})
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join(args, " ")
			if !strings.Contains(text, test.kind) {
				t.Fatalf("missing standard flag: %s", text)
			}
			if !strings.Contains(text, "include") || !strings.Contains(text, "MODE=1") {
				t.Fatalf("missing usage flags: %s", text)
			}
			if test.name == "msvc" && !strings.Contains(text, "/utf-8") {
				t.Fatalf("MSVC compile should use UTF-8 source and execution charsets: %s", text)
			}
		})
	}
}

func TestMSVCCompilerDriverLinkUsesFe(t *testing.T) {
	executable, args, err := (Toolchain{Kind: MSVC, CXX: "cl.exe", Linker: "cl.exe"}).Link(LinkSpec{
		Inputs: []string{"main.obj"}, Output: "bin/app.dll", ImportLibrary: "bin/app.lib", Shared: true, LibraryDirs: []string{"lib"}, Libraries: []string{"user32"}, Options: []string{"/DEBUG"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if executable != "cl.exe" || !strings.Contains(text, "/Febin/app.dll") || strings.Contains(text, "/OUT:") {
		t.Fatalf("compiler-driver link was lowered incorrectly: %s %s", executable, text)
	}
	if !strings.Contains(text, "/link /IMPLIB:bin/app.lib /LIBPATH:lib /DEBUG") {
		t.Fatalf("linker-only flags must follow /link: %s", text)
	}
}

func TestMSVCDriverLinkPrefersSiblingLinkExe(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "cl.exe")
	if err := os.WriteFile(driver, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "link.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	executable, args, err := (Toolchain{Kind: MSVC, CXX: driver, Linker: driver}).Link(LinkSpec{
		Inputs: []string{"main.obj"}, Output: "bin/app.exe", LibraryDirs: []string{"lib"}, Libraries: []string{"user32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if executable != filepath.Join(dir, "link.exe") {
		t.Fatalf("linker = %q", executable)
	}
	if !strings.Contains(text, "/OUT:bin/app.exe") || strings.Contains(text, "/Fe") || strings.Contains(text, "/link") {
		t.Fatalf("native linker args = %s", text)
	}
	if !strings.Contains(text, "/LIBPATH:lib") || !strings.Contains(text, "user32.lib") {
		t.Fatalf("native linker lost inputs: %s", text)
	}
}

func TestClangMSVCABILinkPrefersLLD(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "clang++.exe")
	for _, name := range []string{"clang++.exe", "lld-link.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tc := Toolchain{Kind: Clang, CXX: driver, Linker: driver, Target: "x86_64-pc-windows-msvc"}
	executable, args, err := tc.Link(LinkSpec{Inputs: []string{"main.o"}, Output: "bin/app.exe", LibraryDirs: []string{"lib"}, Libraries: []string{"greeter"}})
	if err != nil {
		t.Fatal(err)
	}
	if executable != driver || len(args) == 0 || args[0] != "-fuse-ld=lld" {
		t.Fatalf("clang MSVC link did not prefer lld: %s %v", executable, args)
	}
	text := strings.Join(args, " ")
	if !strings.Contains(text, "-Llib") || !strings.Contains(text, "-lgreeter") {
		t.Fatalf("gnu-style link arguments lost: %s", text)
	}
}

func TestClangCLLinkPrefersLLD(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "clang-cl.exe")
	if err := os.WriteFile(filepath.Join(dir, "lld-link.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tc := Toolchain{Kind: MSVC, CXX: driver, Linker: driver, Target: "x86_64-pc-windows-msvc"}
	executable, args, err := tc.Link(LinkSpec{Inputs: []string{"main.obj"}, Output: "bin/app.exe"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	// With no link.exe beside it, the toolset directory's lld-link is invoked
	// directly with native flags; that still avoids link.exe temp files.
	if executable != filepath.Join(dir, "lld-link.exe") || !strings.Contains(text, "/OUT:bin/app.exe") || strings.Contains(text, "/link") {
		t.Fatalf("clang-cl link did not use lld-link directly: %s %s", executable, text)
	}
}

func TestClangNonMSVCTargetKeepsDefaultLinker(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "clang++.exe")
	if err := os.WriteFile(filepath.Join(dir, "lld-link.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tc := Toolchain{Kind: Clang, CXX: driver, Linker: driver, Target: "x86_64-pc-linux-gnu"}
	_, args, err := tc.Link(LinkSpec{Inputs: []string{"main.o"}, Output: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "-fuse-ld") {
		t.Fatalf("non-MSVC target gained a lld flag: %v", args)
	}
}

func TestMSVCLinkSkipsCMakePthreadShim(t *testing.T) {
	tc := Toolchain{Kind: MSVC, CXX: "cl.exe", Linker: "cl.exe"}
	_, args, err := tc.Link(LinkSpec{Inputs: []string{"main.obj"}, Output: "app.exe", Libraries: []string{"pthreads", "advapi32"}})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if strings.Contains(text, "pthreads.lib") || !strings.Contains(text, "advapi32.lib") {
		t.Fatalf("unexpected MSVC libraries: %s", text)
	}
}

func TestCCompileUsesCCAndWritesDepfile(t *testing.T) {
	executable, args, err := (Toolchain{Kind: GCC, CC: "gcc", CXX: "g++"}).Compile(CompileSpec{Source: "wrapper.c", Output: "wrapper.o", CStandard: "c17", Depfile: "wrapper.o.d"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if executable != "gcc" || !strings.Contains(text, "-MMD -MF wrapper.o.d") {
		t.Fatalf("C dependency command is incorrect: %s %s", executable, text)
	}
}

func TestWSLWrapsCompilerCommand(t *testing.T) {
	executable, args, err := (Toolchain{Kind: GCC, CC: "/usr/bin/gcc", CXX: "/usr/bin/g++", Runner: "wsl.exe", RunnerArgs: []string{"-d", "Ubuntu", "--cd", "/mnt/c/project/build"}}).Compile(CompileSpec{Source: "../../../src/main.cpp", Output: "obj/main.o", CXXStandard: "c++20"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if executable != "wsl.exe" || !strings.Contains(text, "--exec /usr/bin/g++") {
		t.Fatalf("WSL command is incorrect: %s %s", executable, text)
	}
}
