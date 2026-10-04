package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultToolchainDeclaresPortableSelectors(t *testing.T) {
	cfg := Default("demo")
	if cfg.Toolchain.MSVC != "auto" || cfg.Toolchain.C != "auto" || cfg.Toolchain.CXX != "auto" {
		t.Fatalf("a new project must not pin machine paths: %#v", cfg.Toolchain)
	}
	if cfg.Toolchain.CacheDir != DefaultToolchainCacheDir {
		t.Fatalf("cache_dir = %q, want %q", cfg.Toolchain.CacheDir, DefaultToolchainCacheDir)
	}
	path := writeConfig(t, "")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `:\\`) || strings.Contains(string(written), `C:\`) {
		t.Fatalf("Save wrote a machine-local path: %s", written)
	}
}

func TestValidateAcceptsVersionRangesAndSelectors(t *testing.T) {
	for _, spec := range []string{"", "auto", "clang-cl", "clang++", `D:\LLVM\bin\clang-cl.exe`, "12.4", "12.0~12.9", "~14.5", "14.30~"} {
		cfg := Default("demo")
		cfg.Toolchain.MSVC = spec
		cfg.Toolchain.CUDA = spec
		cfg.Toolchain.C = spec
		cfg.Toolchain.CXX = spec
		if err := Validate(cfg); err != nil {
			t.Fatalf("Validate(%q): %v", spec, err)
		}
	}
}

func TestValidateRejectsMalformedVersionRange(t *testing.T) {
	cfg := Default("demo")
	cfg.Toolchain.CUDA = "12.x~12.9"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "E_CONFIG_INVALID_VERSION") {
		t.Fatalf("a malformed cuda range must be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "[toolchain].cuda") {
		t.Fatalf("the error must name the field: %v", err)
	}
	cfg = Default("demo")
	cfg.Toolchain.MSVC = "14..3"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "[toolchain].msvc") {
		t.Fatalf("a malformed msvc range must be rejected, got %v", err)
	}
}

// Version constraints written unquoted are coerced during migration, so an
// existing project can adopt the range syntax without quoting every value.
func TestLoadCoercesUnquotedVersionConstraints(t *testing.T) {
	path := writeConfig(t, "schema_version = 7\n[project]\nname = \"demo\"\n[toolchain]\nmsvc = \"14.30~14.50\"\ncuda = 12.6\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.CUDA != "12.6" || cfg.Toolchain.MSVC != "14.30~14.50" {
		t.Fatalf("unexpected toolchain: %#v", cfg.Toolchain)
	}
}

func TestNormalizeResolvesCacheDirAgainstProjectRoot(t *testing.T) {
	root := t.TempDir()
	cfg := Default("demo")
	if err := cfg.normalize(root); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".trestle", "toolchain"); cfg.Toolchain.CacheDir != want {
		t.Fatalf("cache_dir = %q, want %q", cfg.Toolchain.CacheDir, want)
	}
	if cfg.Toolchain.MSVC != "auto" {
		t.Fatalf("an absent msvc range must normalize to auto, got %q", cfg.Toolchain.MSVC)
	}
}

func TestSaveKeepsCacheDirRelativeToProject(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DefaultFileName)
	cfg := Default("demo")
	if err := SaveExpected(path, cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `cache_dir = "`+DefaultToolchainCacheDir+`"`) {
		t.Fatalf("cache_dir must be stored relative to trestle.toml: %s", written)
	}
	// An absolute override outside the project must survive untouched.
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "toolchains")
	cfg.Toolchain.CacheDir = outside
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	written, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `cache_dir = "`+DefaultToolchainCacheDir+`"`) {
		t.Fatalf("an external cache_dir must not be rewritten to the default: %s", written)
	}
	if !strings.Contains(string(written), "toolchains") {
		t.Fatalf("an external cache_dir must be preserved: %s", written)
	}
}

func TestConfigRootReportsProjectDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DefaultFileName)
	if err := SaveExpected(path, Default("demo"), "missing"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Root() != absolute {
		t.Fatalf("Root() = %q, want %q", cfg.Root(), absolute)
	}
}

func TestTaskSettingsMayPinMSVCRange(t *testing.T) {
	if !taskSettingAllowed("toolchain.msvc") {
		t.Fatal("tasks must be able to pin an msvc version range")
	}
}
