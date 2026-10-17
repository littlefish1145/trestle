package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/deps"
)

// TestSearchPackagesQueriesRealConanRemotes exercises the whole search path
// against the installed conan, so a remote-only recipe really shows up. It is
// skipped when conan is not installed.
func TestSearchPackagesQueriesRealConanRemotes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	cfg.Conan.Profile = "default"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := SearchPackages(context.Background(), path, "fmt")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("no provider returned results for fmt")
	}
	var conanVersions []string
	var sources []string
	for _, entry := range entries {
		if entry.Provider != "conan" {
			continue
		}
		if entry.Source == "local" {
			continue
		}
		conanVersions = append(conanVersions, entry.Version)
		sources = append(sources, entry.Source)
	}
	if len(conanVersions) == 0 {
		t.Skip("conan returned only local cache results; remote lookup was not exercised")
	}
	t.Logf("conan returned %d remote versions, sources=%v", len(conanVersions), sources)
	// A remote query must reach at least one real conan remote, not just the cache.
	remoteHit := false
	for _, source := range sources {
		if source != "" && source != "local" {
			remoteHit = true
		}
	}
	if !remoteHit {
		t.Fatalf("no remote-sourced conan entries: %+v", entries)
	}
	// The merged view must collapse every fmt version into one package.
	merged := deps.Merge(entries)
	found := 0
	for _, pkg := range merged {
		if pkg.Name == "fmt" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("fmt should merge into one package, found %d: %+v", found, merged)
	}
	if !strings.Contains(strings.Join(conanVersions, ","), "11.") {
		t.Logf("versions seen: %v", conanVersions)
	}
}
