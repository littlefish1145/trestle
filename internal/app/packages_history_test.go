package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
)

func TestSearchPackagesExpandsCachedVcpkgHistory(t *testing.T) {
	root := t.TempDir()
	vcpkgRoot := filepath.Join(root, "vcpkg")
	cacheDir := filepath.Join(root, ".trestle", "cache")
	for _, dir := range []string{filepath.Join(vcpkgRoot, "versions", "f-"), cacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// No executable is needed on the cached path: finding this placeholder must
	// be enough to read the version database instead of running vcpkg search.
	if err := os.WriteFile(filepath.Join(vcpkgRoot, "vcpkg.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	database := `{"versions":[{"version-semver":"11.0.0","port-version":2},{"version-semver":"10.0.0"}]}`
	if err := os.WriteFile(filepath.Join(vcpkgRoot, "versions", "f-", "fmt.json"), []byte(database), 0o644); err != nil {
		t.Fatal(err)
	}
	cache, err := json.Marshal(struct {
		SavedAt time.Time    `json:"saved_at"`
		Ports   []vcpkg.Port `json:"ports"`
	}{time.Now(), []vcpkg.Port{{Name: "fmt", Version: "11.0.0#2", Description: "format library", Features: []string{"tools"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "vcpkg-index.json"), cache, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	cfg.Vcpkg.Root = vcpkgRoot
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := SearchPackages(context.Background(), path, "vcpkg:fmt")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
	if entries[0].Version != "11.0.0" || entries[0].PortVersion != 2 || entries[1].Version != "10.0.0" {
		t.Fatalf("cached search did not include full version history: %+v", entries)
	}
	if entries[0].Source != "current" || entries[1].Source != "vcpkg.io" || len(entries[0].Features) != 1 {
		t.Fatalf("history metadata lost: %+v", entries)
	}
}
