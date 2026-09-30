package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/fsx"
)

func selectRecoveryCompiler(t *testing.T, path string) {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Toolchain.CXX = integrationCompiler()
	if _, err := detectConfigured(context.Background(), cfg); err != nil {
		t.Skipf("real generation requires a configured compiler: %v", err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResourceOwnerProcess(t *testing.T) {
	path := os.Getenv("TRESTLE_RESOURCE_CHILD")
	if path == "" {
		return
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := buildResources(context.Background(), cfg, "child build")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("resources ready")
	time.Sleep(30 * time.Second)
}

func TestBuildAndVcpkgConflictAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("demo")
	cfg.Vcpkg.Root = filepath.Join(root, "vcpkg")
	cfg.Packages["fmt"] = config.Package{Provider: "vcpkg", Port: "fmt", Triplet: "x64-windows"}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestResourceOwnerProcess$")
	cmd.Env = append(os.Environ(), "TRESTLE_RESOURCE_CHILD="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "resources ready" {
			t.Fatalf("child: %s", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child timeout")
	}
	if err := BuildWithProgress(context.Background(), path, nil); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("build conflict missing: %v", err)
	}
	client := vcpkg.Client{Root: cfg.Vcpkg.Root, Executable: "must-not-start"}
	if err := client.InstallWithProgress(context.Background(), []string{"fmt"}, "x64-windows", nil); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("install conflict missing: %v", err)
	}
	other, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Build.BuildDir = filepath.Join(root, "other")
	_, release, err := buildResources(context.Background(), other, "parallel build")
	if err != nil {
		t.Fatalf("independent outputs blocked: %v", err)
	}
	release()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_, release, err = buildResources(context.Background(), other, "after crash")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestPackageFailurePreservesPreviousZIP(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := Init(root, "demo"); err != nil {
		t.Fatal(err)
	}
	selectRecoveryCompiler(t, path)
	old := []byte("previous archive")
	output := filepath.Join(root, "dist", "demo.zip")
	if err := fsx.AtomicWrite(output, old); err != nil {
		t.Fatal(err)
	}
	if err := packageSelected(path, []string{"app"}, nil); err == nil {
		t.Fatal("missing artifact unexpectedly packaged")
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("old archive lost: %q %v", got, err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "dist", ".trestle-archive-*"))
	if len(files) != 0 {
		t.Fatalf("temporary archives leaked: %v", files)
	}
}

func TestGenerationPublishFailureCanRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := Init(root, "retry"); err != nil {
		t.Fatal(err)
	}
	selectRecoveryCompiler(t, path)
	dir := filepath.Join(root, "build", "debug")
	if err := os.MkdirAll(filepath.Join(dir, "build.ninja"), 0700); err != nil {
		t.Fatal(err)
	}
	_, generateErr := Generate(path)
	if generateErr == nil {
		t.Fatal("expected manifest publish failure")
	}
	marker := filepath.Join(dir, ".trestle", "incomplete")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("missing incomplete marker: %v; generation failed with %v", err, generateErr)
	}
	if err := os.Remove(filepath.Join(dir, "build.ninja")); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("completion marker not cleared: %v", err)
	}
}
