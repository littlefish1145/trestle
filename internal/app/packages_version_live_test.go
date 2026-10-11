package app

import (
	"context"
	"path/filepath"
	"testing"

	"trestle/internal/config"
	"trestle/internal/deps"
	"trestle/internal/deps/vcpkg"
)

func vcpkgRootForTest() (vcpkg.Client, error) { return vcpkg.NewClient("") }

// TestNameVersionQueryResolvesRealVcpkgHistory walks the whole "fmt:11.0.2"
// path against an installed vcpkg checkout: the query must resolve through the
// version database and come back with that release marked as requested.
func TestNameVersionQueryResolvesRealVcpkgHistory(t *testing.T) {
	client, err := vcpkgRootForTest()
	if err != nil {
		t.Skipf("vcpkg is not installed: %v", err)
	}
	all, err := client.PortVersions(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("fmt history: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("expected a real history, got %d versions", len(all))
	}
	// Pick an older release that is definitely not the newest one.
	target := all[len(all)/2]
	root := t.TempDir()
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := SearchPackages(context.Background(), path, "fmt:"+target.Version)
	if err != nil {
		t.Fatalf("name:version search failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("name:version search returned nothing")
	}
	requested := 0
	for _, entry := range entries {
		if entry.Version == target.Version {
			requested++
			if !entry.Requested {
				t.Fatalf("entry %+v should be marked requested", entry)
			}
			if entry.Name != "fmt" {
				t.Fatalf("unexpected name %q", entry.Name)
			}
		}
	}
	if requested == 0 {
		t.Fatalf("requested version %s missing from %+v", target.Version, entries)
	}
	t.Logf("resolved fmt:%s from %d entries", target.Version, len(entries))
}

func TestNameVersionQueryRejectsUnknownVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := SearchPackages(context.Background(), path, "vcpkg:fmt:0.0.0-nope")
	if err == nil && len(entries) > 0 {
		for _, entry := range entries {
			if entry.Requested {
				t.Fatalf("a nonexistent version was reported as found: %+v", entry)
			}
		}
	}
	t.Logf("unknown version handled: err=%v entries=%d", err, len(entries))
}

func TestBareSearchStillMergesBothProviders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "trestle.toml")
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	entries, err := SearchPackages(context.Background(), path, "fmt")
	if err != nil {
		t.Fatalf("bare search failed: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("no provider returned results for fmt")
	}
	packages := deps.Merge(entries)
	count := 0
	for _, pkg := range packages {
		if pkg.Name == "fmt" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("fmt appeared %d times after merging: %+v", count, packages)
	}
}
