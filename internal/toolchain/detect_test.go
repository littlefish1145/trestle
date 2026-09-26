package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVisualStudioRootFromSetup(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "Common7", "Tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "VsDevCmd.bat"), []byte("@echo off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	if err := os.MkdirAll(filepath.Dir(setup), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setup, []byte("@echo off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if actual := visualStudioRootFromSetup(setup); actual != root {
		t.Fatalf("unexpected Visual Studio root: %q", actual)
	}
	environment := EnvironmentForSetup(context.Background(), setup)
	if environment["VCPKG_VISUAL_STUDIO_PATH"] != root {
		t.Fatalf("vcpkg did not receive the configured Visual Studio root: %#v", environment)
	}
}

func TestEnvironmentForVcpkgUsesSelectedToolchainInstallation(t *testing.T) {
	root := t.TempDir()
	setup := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	if err := os.MkdirAll(filepath.Dir(setup), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setup, []byte("@echo off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archiver := filepath.Join(root, "VC", "Tools", "MSVC", "14.44", "bin", "HostX64", "x64", "lib.exe")
	if err := os.MkdirAll(filepath.Dir(archiver), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archiver, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	environment := EnvironmentForVcpkg(context.Background(), "", archiver)
	if environment["VCPKG_VISUAL_STUDIO_PATH"] != root {
		t.Fatalf("vcpkg selected %q, want %q", environment["VCPKG_VISUAL_STUDIO_PATH"], root)
	}
}

func TestResolveMSVCSetupFromConfiguredArchiver(t *testing.T) {
	root := t.TempDir()
	setup := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	compiler := filepath.Join(root, "VC", "Tools", "MSVC", "14.44", "bin", "Hostx64", "x64", "cl.exe")
	archiver := filepath.Join(filepath.Dir(compiler), "lib.exe")
	for _, path := range []string{setup, compiler, archiver} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("@echo off\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := ResolveMSVCSetup(context.Background(), "", archiver); got != setup {
		t.Fatalf("resolved setup %q, want %q", got, setup)
	}
	if !NeedsMSVCEnvironment(context.Background(), `D:\LLVM\bin\clang-cl.exe`) {
		t.Fatal("MSVC ABI compiler classification is incorrect")
	}
}

func TestTargetUsesMSVCABI(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc19.44"} {
		if !targetUsesMSVCABI(target) {
			t.Fatalf("MSVC target %q was not recognized", target)
		}
	}
	for _, target := range []string{"x86_64-w64-windows-gnu", "x86_64-w64-mingw32", "x86_64-unknown-linux-gnu", ""} {
		if targetUsesMSVCABI(target) {
			t.Fatalf("non-MSVC target %q was misclassified", target)
		}
	}
}

func TestEnvironmentForSetupHandlesQuotedPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Visual Studio")
	setup := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	compiler := filepath.Join(root, "VC", "Tools", "MSVC", "14.44", "bin", "Hostx64", "x64", "cl.exe")
	if err := os.MkdirAll(filepath.Dir(setup), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(compiler), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compiler, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	data := "@echo off\r\nset INCLUDE=C:\\ABI SDK\\include\r\nset LIB=C:\\ABI SDK\\lib\r\nset PATH=C:\\ABI SDK\\bin;%PATH%\r\n"
	if err := os.WriteFile(setup, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := environmentForSetup(context.Background(), setup)
	if environment["INCLUDE"] != `C:\ABI SDK\include` || environment["LIB"] != `C:\ABI SDK\lib` || !strings.HasPrefix(environment["PATH"], `C:\ABI SDK\bin;`) {
		t.Fatalf("setup environment was not captured: %#v", environment)
	}
}
