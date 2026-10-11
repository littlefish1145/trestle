package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requireWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows and MSVC path semantics")
	}
}

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
	requireWindows(t)

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
	requireWindows(t)

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
	requireWindows(t)

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
	environment, err := environmentForSetup(context.Background(), setup)
	if err != nil {
		t.Fatalf("environment probe failed: %v", err)
	}
	if environment["INCLUDE"] != `C:\ABI SDK\include` || environment["LIB"] != `C:\ABI SDK\lib` || !strings.HasPrefix(environment["PATH"], `C:\ABI SDK\bin;`) {
		t.Fatalf("setup environment was not captured: %#v", environment)
	}
}

// A probe that the caller's deadline kills must report itself as incomplete.
// Reporting it as a missing environment told users to reinstall Visual Studio
// when their tools were present and only the detection budget had run out.
func TestEnvironmentForSetupReportsKilledProbeAsError(t *testing.T) {
	requireWindows(t)

	root := filepath.Join(t.TempDir(), "Visual Studio")
	setup := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	if err := os.MkdirAll(filepath.Dir(setup), 0o755); err != nil {
		t.Fatal(err)
	}
	// A script that outlives the deadline below, so the kill lands mid-probe.
	// The short ping also keeps the test quick: cmd.exe's output pipe stays
	// open until this grandchild exits, so the probe only returns once it does.
	data := "@echo off\r\nping -n 3 127.0.0.1 >nul\r\nset INCLUDE=C:\\ABI SDK\\include\r\nset LIB=C:\\ABI SDK\\lib\r\n"
	if err := os.WriteFile(setup, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	environment, err := environmentForSetup(ctx, setup)
	if err == nil {
		t.Fatalf("killed probe reported success with environment %#v", environment)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("killed probe reported %v, want a deadline error", err)
	}
}

// A setup script that runs to completion but sets nothing usable is a genuine
// absence of environment, so it must stay an error-free nil result rather than
// being reported as a timeout.
func TestEnvironmentForSetupTreatsEmptyResultAsAbsent(t *testing.T) {
	requireWindows(t)

	setup := filepath.Join(t.TempDir(), "vcvars64.bat")
	if err := os.WriteFile(setup, []byte("@echo off\r\necho nothing useful\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	environment, err := environmentForSetup(context.Background(), setup)
	if err != nil {
		t.Fatalf("empty environment reported as failure: %v", err)
	}
	if environment["INCLUDE"] != "" || environment["LIB"] != "" {
		t.Fatalf("unexpected environment: %#v", environment)
	}
}
