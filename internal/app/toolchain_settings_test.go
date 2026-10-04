package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/toolchain"
)

func TestToolchainRequestCarriesPortableSpecification(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("demo")
	cfg.Toolchain.MSVC = "14.30~14.50"
	cfg.Toolchain.CUDA = "12.0~12.9"
	if err := config.SaveExpected(path, cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	request := toolchainRequest(loaded)
	if request.MSVC != "14.30~14.50" || request.CUDA != "12.0~12.9" {
		t.Fatalf("the version ranges must reach detection: %#v", request)
	}
	if request.Mode != "native" {
		t.Fatalf("mode = %q", request.Mode)
	}
	// The cache directory must be resolved against the project, not the process
	// working directory, so a build started from elsewhere still shares it.
	if want := filepath.Join(root, ".trestle", "toolchain"); request.CacheDir != want {
		t.Fatalf("cache dir = %q, want %q", request.CacheDir, want)
	}
}

func TestCudaRootHonoursEnvironmentCacheOverride(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("demo")
	if err := config.SaveExpected(path, cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	override := t.TempDir()
	t.Setenv("TRESTLE_TOOLCHAIN_CACHE", override)
	// No toolkit satisfies the range here, but the failure must name the cache
	// that was consulted rather than silently rescanning elsewhere.
	_, err = cudaRoot(t.Context(), loaded)
	if err == nil || !strings.Contains(err.Error(), "E_CUDA_NOT_FOUND") {
		t.Fatalf("expected an unsatisfiable CUDA range, got %v", err)
	}
}

// configureCUDA stores a version range whenever the layout names one, so a
// toolkit path handed to the CLI does not end up in trestle.toml.
func TestConfigureCUDAPrefersRangeOverPath(t *testing.T) {
	root := t.TempDir()
	toolkit := filepath.Join(root, "v12.6")
	if err := os.MkdirAll(filepath.Join(toolkit, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	nvcc := "nvcc"
	if runtime.GOOS == "windows" {
		nvcc += ".exe"
	}
	if err := os.WriteFile(filepath.Join(toolkit, "bin", nvcc), []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pin discovery to the fake toolkit so the machine's own CUDA cannot satisfy
	// or contradict the range under test.
	t.Setenv(toolchain.CUDARootsEnv, toolkit)
	path := filepath.Join(root, config.DefaultFileName)
	if err := config.SaveExpected(path, config.Default("demo"), "missing"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	version, override, err := configureCUDA(t.Context(), cfg, toolkit)
	if err != nil {
		t.Fatal(err)
	}
	if version != "12.6~12.6" || override != "" {
		t.Fatalf("a versioned toolkit must not leave a machine path: %q %q", version, override)
	}
}

// A toolkit directory that carries no version keeps working through an explicit
// override instead of guessing.
func TestConfigureCUDAKeepsUnversionedToolkitAsOverride(t *testing.T) {
	root := t.TempDir()
	toolkit := filepath.Join(root, "toolkit")
	if err := os.MkdirAll(filepath.Join(toolkit, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, config.DefaultFileName)
	if err := config.SaveExpected(path, config.Default("demo"), "missing"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	version, override, err := configureCUDA(t.Context(), cfg, toolkit)
	if err != nil {
		t.Fatal(err)
	}
	if version != "auto" || override != toolkit {
		t.Fatalf("an unversioned toolkit must stay as an override: %q %q", version, override)
	}
}

func TestSetCompilerStoresPortableSelector(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := config.SaveExpected(path, config.Default("demo"), "missing"); err != nil {
		t.Fatal(err)
	}
	name := integrationCompiler()
	resolved, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("no %s on PATH", name)
	}
	if err := SetCompiler(path, resolved); err != nil {
		t.Skipf("%s is present but not usable in this environment: %v", name, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.CXX != name || cfg.Toolchain.Archiver != "auto" || cfg.Toolchain.Linker != "auto" {
		t.Fatalf("SetCompiler must persist selectors, not paths: %#v", cfg.Toolchain)
	}
	if cfg.Toolchain.Setup != "" {
		t.Fatalf("setup must not be persisted: %q", cfg.Toolchain.Setup)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), resolved) {
		t.Fatalf("the compiler path leaked into trestle.toml: %s", written)
	}
}
