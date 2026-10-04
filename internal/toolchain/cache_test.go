package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheDirPrefersEnvironmentOverride(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(t.TempDir(), "shared-cache")
	t.Setenv("TRESTLE_TOOLCHAIN_CACHE", override)
	if got := CacheDir("configured/relative", root); got != override {
		t.Fatalf("CacheDir = %q, want the environment override %q", got, override)
	}
}

func TestCacheDirResolvesConfigurationAgainstProjectRoot(t *testing.T) {
	t.Setenv("TRESTLE_TOOLCHAIN_CACHE", "")
	root := t.TempDir()
	relative := CacheDir("build/toolchains", root)
	if want := filepath.Join(root, "build", "toolchains"); relative != want {
		t.Fatalf("CacheDir = %q, want %q", relative, want)
	}
	absolute := filepath.Join(t.TempDir(), "elsewhere")
	if got := CacheDir(absolute, root); got != absolute {
		t.Fatalf("an absolute cache_dir must be used as written, got %q", got)
	}
}

func TestCacheDirDefaultsInsideIgnoredStateDirectory(t *testing.T) {
	t.Setenv("TRESTLE_TOOLCHAIN_CACHE", "")
	root := t.TempDir()
	want := filepath.Join(root, ".trestle", "toolchain")
	if got := CacheDir("", root); got != want {
		t.Fatalf("CacheDir = %q, want %q", got, want)
	}
	if DefaultCacheDir(root) != want {
		t.Fatal("DefaultCacheDir must agree with an unset configuration")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	cache := NewCache(t.TempDir())
	if cache.Path == "" || filepath.Base(cache.Path) != "inventory.json" {
		t.Fatalf("unexpected cache path %q", cache.Path)
	}
	if _, ok := cache.Load(DefaultCacheTTL); ok {
		t.Fatal("a missing cache must report a miss")
	}
	wanted := Inventory{MSVC: []MSVCInstall{{Version: "14.44.35207", Compiler: `C:\vs\cl.exe`}}, CUDA: []CUDAInstall{{Version: "12.6", Root: `C:\CUDA\v12.6`}}}
	if err := cache.Save(wanted); err != nil {
		t.Fatal(err)
	}
	loaded, ok := cache.Load(DefaultCacheTTL)
	if !ok {
		t.Fatal("a freshly written cache must be readable")
	}
	if len(loaded.MSVC) != 1 || loaded.MSVC[0].Version != "14.44.35207" {
		t.Fatalf("MSVC installs did not round-trip: %#v", loaded.MSVC)
	}
	if len(loaded.CUDA) != 1 || loaded.CUDA[0].Version != "12.6" {
		t.Fatalf("CUDA installs did not round-trip: %#v", loaded.CUDA)
	}
	if loaded.Version != CacheFormatVersion || loaded.Platform != platformKey() {
		t.Fatalf("cache metadata was not stamped: %#v", loaded)
	}
}

func TestCacheIgnoresExpiredCorruptAndForeignEntries(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	if err := cache.Save(Inventory{Version: CacheFormatVersion}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(time.Nanosecond); ok {
		t.Fatal("an expired cache must report a miss so a new install is discovered")
	}
	if _, ok := cache.Load(DefaultCacheTTL); !ok {
		t.Fatal("the same entry must still be fresh within the window")
	}

	if err := os.WriteFile(cache.Path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A cache is disposable: a corrupt file must degrade to a rescan, not fail.
	if _, ok := cache.Load(DefaultCacheTTL); ok {
		t.Fatal("a corrupt cache must report a miss")
	}

	// Save always stamps the current platform, so write the foreign entry directly.
	foreign := `{"version":1,"platform":"other/arch","created":"2999-01-01T00:00:00Z"}`
	if err := os.WriteFile(cache.Path, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(DefaultCacheTTL); ok {
		t.Fatal("an inventory from another platform must report a miss")
	}
	if err := os.WriteFile(cache.Path, []byte(`{"version":2,"platform":"`+platformKey()+`","created":"2999-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(DefaultCacheTTL); ok {
		t.Fatal("an inventory from another build must report a miss")
	}
}

func TestCacheDisabledWithoutDirectory(t *testing.T) {
	cache := NewCache("")
	if cache.Path != "" {
		t.Fatalf("an empty directory must disable caching, got %q", cache.Path)
	}
	if err := cache.Save(Inventory{}); err != nil {
		t.Fatalf("saving a disabled cache must be a no-op: %v", err)
	}
	if _, ok := cache.Load(DefaultCacheTTL); ok {
		t.Fatal("a disabled cache must always miss")
	}
	if err := cache.Clear(); err != nil {
		t.Fatalf("clearing a disabled cache must be a no-op: %v", err)
	}
}

func TestInvalidateRemovesCachedInventory(t *testing.T) {
	dir := t.TempDir()
	if err := NewCache(dir).Save(Inventory{Version: CacheFormatVersion}); err != nil {
		t.Fatal(err)
	}
	if err := Invalidate(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := NewCache(dir).Load(DefaultCacheTTL); ok {
		t.Fatal("the cache must be gone after Invalidate")
	}
	if err := Invalidate(""); err == nil {
		t.Fatal("Invalidate must reject an unconfigured directory")
	}
	if err := Invalidate(dir); err != nil {
		t.Fatalf("invalidating twice must stay idempotent: %v", err)
	}
}

func TestDiscoverInventoryStoresScanInTheGivenDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inventory")
	inventory := DiscoverInventory(context.Background(), dir)
	if inventory.Platform != platformKey() || inventory.Version != CacheFormatVersion {
		t.Fatalf("unexpected scan metadata: %#v", inventory)
	}
	if _, err := os.Stat(filepath.Join(dir, "inventory.json")); err != nil {
		t.Fatalf("the scan must be cached in the designated directory: %v", err)
	}
	// A zero freshness window forces a rescan, which must still succeed.
	if rescanned := DiscoverInventoryWithTTL(context.Background(), dir, 0); len(rescanned.Compilers) != len(inventory.Compilers) {
		t.Fatalf("a forced rescan returned %d compilers, want %d", len(rescanned.Compilers), len(inventory.Compilers))
	}
}
