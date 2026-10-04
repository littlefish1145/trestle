package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"trestle/internal/fsx"
)

// CacheFormatVersion invalidates inventories written by an incompatible build.
const CacheFormatVersion = 1

// DefaultCacheTTL bounds how long a cached inventory is trusted, so installing a
// new toolset is picked up without needing an explicit invalidation command.
const DefaultCacheTTL = 10 * time.Minute

const cacheFileName = "inventory.json"

// Inventory is the machine-local result of scanning for compilers, MSVC
// toolsets, and CUDA toolkits. It is never committed: trestle.toml stores the
// version ranges that are matched against this inventory.
type Inventory struct {
	Version   int           `json:"version"`
	Created   time.Time     `json:"created"`
	Platform  string        `json:"platform"`
	MSVC      []MSVCInstall `json:"msvc"`
	CUDA      []CUDAInstall `json:"cuda"`
	Compilers []Component   `json:"compilers"`
}

// Cache reads and writes the inventory inside one designated directory.
type Cache struct {
	Dir  string
	Path string
}

// NewCache returns the cache stored in dir. An empty dir disables caching, which
// keeps ephemeral callers (tests, one-shot doctor runs) free of disk state.
func NewCache(dir string) Cache {
	if dir == "" {
		return Cache{}
	}
	return Cache{Dir: dir, Path: filepath.Join(dir, cacheFileName)}
}

// DefaultCacheDir is the project-relative location used when trestle.toml does
// not name one. It sits under .trestle so it is ignored by git and by source
// scanners alike.
func DefaultCacheDir(root string) string {
	return filepath.Join(root, ".trestle", "toolchain")
}

// CacheDir resolves where the inventory is cached: an explicit environment
// override wins, then [toolchain].cache_dir relative to the project root, then
// the default project-local directory.
func CacheDir(configured, root string) string {
	if override := strings.TrimSpace(os.Getenv("TRESTLE_TOOLCHAIN_CACHE")); override != "" {
		return absoluteOrJoin(root, override)
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return absoluteOrJoin(root, configured)
	}
	return DefaultCacheDir(root)
}

func absoluteOrJoin(root, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if root == "" {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(root, value))
}

// Load returns the cached inventory when it is readable, current, and fresh.
// A stale or corrupt cache is reported as a miss instead of an error: the cache
// is disposable and must never fail an otherwise valid build.
func (c Cache) Load(ttl time.Duration) (Inventory, bool) {
	if c.Path == "" {
		return Inventory{}, false
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return Inventory{}, false
	}
	var inventory Inventory
	if json.Unmarshal(data, &inventory) != nil {
		return Inventory{}, false
	}
	if inventory.Version != CacheFormatVersion || inventory.Platform != platformKey() {
		return Inventory{}, false
	}
	if ttl > 0 && time.Since(inventory.Created) > ttl {
		return Inventory{}, false
	}
	return inventory, true
}

// Save writes the inventory atomically so a concurrent reader never observes a
// half-written file.
func (c Cache) Save(inventory Inventory) error {
	if c.Path == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	inventory.Version = CacheFormatVersion
	inventory.Platform = platformKey()
	inventory.Created = time.Now().UTC()
	data, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(c.Path, append(data, '\n'))
}

// Clear removes the cached inventory so the next resolution rescans.
func (c Cache) Clear() error {
	if c.Path == "" {
		return nil
	}
	if err := os.Remove(c.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func platformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// DiscoverInventory returns the cached inventory when it is fresh and rescans
// otherwise. Scanning is serialized through the cache directory lock so parallel
// trestle invocations do not rewrite the file at the same time.
func DiscoverInventory(ctx context.Context, cacheDir string) Inventory {
	return DiscoverInventoryWithTTL(ctx, cacheDir, DefaultCacheTTL)
}

// DiscoverInventoryWithTTL exposes the freshness window for callers that need a
// stricter or disabled cache, such as doctor and configure.
func DiscoverInventoryWithTTL(ctx context.Context, cacheDir string, ttl time.Duration) Inventory {
	cache := NewCache(cacheDir)
	if inventory, ok := cache.Load(ttl); ok {
		return inventory
	}
	inventory := ScanInventory(ctx)
	if cache.Path != "" {
		if lock, err := fsx.TryLock(ctx, cache.Path, "scan toolchains"); err == nil {
			defer lock.Close()
			// Another process may have refreshed the cache while we waited.
			if existing, ok := cache.Load(ttl); ok {
				return existing
			}
			// A failed write only costs a rescan next time; keep the result.
			_ = cache.Save(inventory)
		}
	}
	return inventory
}

// ScanInventory rescans the machine without touching the cache.
func ScanInventory(ctx context.Context) Inventory {
	return Inventory{
		Version:   CacheFormatVersion,
		Created:   time.Now().UTC(),
		Platform:  platformKey(),
		MSVC:      DiscoverMSVCInstalls(ctx),
		CUDA:      DiscoverCUDAInstalls(ctx),
		Compilers: discoverCompilerComponents(ctx),
	}
}

// Invalidate drops the cached inventory so the next resolution reflects the
// current machine state.
func Invalidate(cacheDir string) error {
	if cacheDir == "" {
		return fmt.Errorf("toolchain cache directory is not configured")
	}
	return NewCache(cacheDir).Clear()
}

func sortComponentsByVersion(components []Component) {
	sort.SliceStable(components, func(i, j int) bool {
		left, leftOK := ExtractVersion(components[i].Version)
		right, rightOK := ExtractVersion(components[j].Version)
		if leftOK != rightOK {
			return leftOK
		}
		return CompareVersions(left, right) > 0
	})
}
