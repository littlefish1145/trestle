package vcpkg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPortVersionsReadsRealCheckout exercises the history lookup against an
// installed vcpkg checkout, so the version database and git-tree resolution
// cannot drift.
//
// It skips unless the checkout is a full git clone carrying the version
// database. A bootstrap vcpkg — the kind CI images ship, which has a binary and
// ports but no versions/ directory — cannot answer this and is skipped rather
// than failed.
func TestPortVersionsReadsRealCheckout(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Skipf("vcpkg is not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(client.Root, "versions")); err != nil {
		t.Skipf("vcpkg at %s is not a full checkout: no versions directory", client.Root)
	}
	if _, err := client.PortVersions(context.Background(), "fmt"); err != nil {
		if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no version history") {
			t.Fatalf("fmt history: %v", err)
		}
		t.Skipf("the installed vcpkg has no version database for fmt: %v", err)
	}
	versions, err := client.PortVersions(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("fmt history: %v", err)
	}
	if len(versions) < 2 {
		t.Fatalf("expected historical versions, got %d: %+v", len(versions), versions)
	}
	// The newest entry must be the port currently checked out.
	if !versions[0].Current {
		t.Fatalf("newest entry %+v is not marked as current", versions[0])
	}
	if versions[0].GitTree == "" {
		t.Fatalf("version is missing its git tree: %+v", versions[0])
	}
	seen := map[string]bool{}
	for _, version := range versions {
		if version.Version == "" {
			t.Fatalf("version entry without a version: %+v", version)
		}
		key := version.FormatVersion()
		if seen[key] {
			t.Fatalf("duplicate version %q in history", key)
		}
		seen[key] = true
	}
	// Ordering must be newest first.
	for index := 1; index < len(versions); index++ {
		if compareVersionStrings(versions[index-1].Version, versions[index].Version) < 0 {
			t.Fatalf("versions not newest-first at %d: %s then %s", index, versions[index-1].Version, versions[index].Version)
		}
	}
	if len(versions[0].Description) == 0 && !client.hasGitCheckout() {
		t.Fatalf("no metadata available: %+v", versions[0])
	}
	t.Logf("fmt has %d versions; newest=%s description=%q date=%q", len(versions), versions[0].FormatVersion(), versions[0].Description, versions[0].Date)
	if len(versions) > 1 {
		t.Logf("older: %s date=%q", versions[len(versions)-1].FormatVersion(), versions[len(versions)-1].Date)
	}
}

func TestPortVersionsRejectsUnknownPort(t *testing.T) {
	client := Client{Root: t.TempDir()}
	if _, err := client.PortVersions(context.Background(), "fmt"); err == nil {
		t.Fatal("expected an error without a version database")
	}
}

// TestVersionDatabasePathShardsByFirstLetter pins the on-disk layout. The
// expected path is built with the platform separator instead of a literal, so
// the test holds on non-Windows hosts as well.
func TestVersionDatabasePathShardsByFirstLetter(t *testing.T) {
	root := filepath.Join("home", "vcpkg")
	for name, file := range map[string]string{
		"fmt":    "fmt.json",
		"7zip":   "7zip.json",
		"catch2": "catch2.json",
		"Boost":  "boost.json",
	} {
		got := versionDatabasePath(root, name)
		want := filepath.Join(root, "versions", strings.ToLower(string(name[0]))+"-", file)
		if got != want {
			t.Fatalf("versionDatabasePath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPortVersionFormatIncludesPortVersion(t *testing.T) {
	if got := (PortVersion{Version: "11.0.2", PortVersion: 0}).FormatVersion(); got != "11.0.2" {
		t.Fatalf("FormatVersion = %q", got)
	}
	if got := (PortVersion{Version: "11.0.2", PortVersion: 3}).FormatVersion(); got != "11.0.2#3" {
		t.Fatalf("FormatVersion = %q", got)
	}
}

// The version database is read straight off disk, so a synthetic one proves the
// lookup and ordering without needing a vcpkg checkout at all.
func TestPortVersionsReadsVersionDatabase(t *testing.T) {
	root := t.TempDir()
	shard := filepath.Join(root, "versions", "f-")
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	database := `{"versions":[
		{"git-tree":"aaa","version":"11.2.0","port-version":0},
		{"git-tree":"bbb","version":"10.2.1","port-version":2},
		{"git-tree":"ccc","version":"11.0.2","port-version":1},
		{"git-tree":"ddd","version":"11.0.2","port-version":0},
		{"git-tree":"eee","version":"9.1.0","port-version":0}
	]}`
	if err := os.WriteFile(filepath.Join(shard, "fmt.json"), []byte(database), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	versions, err := client.PortVersions(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var got []string
	for _, version := range versions {
		got = append(got, version.FormatVersion())
	}
	// Newest first, and a higher port-version of the same version leads it.
	want := []string{"11.2.0", "11.0.2#1", "11.0.2", "10.2.1#2", "9.1.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if versions[0].Port != "fmt" {
		t.Fatalf("port name not recorded: %+v", versions[0])
	}
	if versions[0].GitTree != "aaa" {
		t.Fatalf("git tree not recorded: %+v", versions[0])
	}
}

func TestResolveVersionMatchesExactAndPortVersion(t *testing.T) {
	root := t.TempDir()
	shard := filepath.Join(root, "versions", "f-")
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	database := `{"versions":[
		{"git-tree":"aaa","version":"11.2.0","port-version":0},
		{"git-tree":"bbb","version":"11.0.2","port-version":1},
		{"git-tree":"ccc","version":"11.0.2","port-version":0}
	]}`
	if err := os.WriteFile(filepath.Join(shard, "fmt.json"), []byte(database), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	// No port-version suffix: the newest revision of that version wins.
	matched, err := client.ResolveVersion(context.Background(), "fmt", "11.0.2")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(matched) != 1 || matched[0].PortVersion != 1 {
		t.Fatalf("resolve 11.0.2 = %+v, want port-version 1", matched)
	}
	// An explicit #n pins the revision.
	matched, err = client.ResolveVersion(context.Background(), "fmt", "11.0.2#0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(matched) != 1 || matched[0].PortVersion != 0 {
		t.Fatalf("resolve 11.0.2#0 = %+v", matched)
	}
	// An unknown version names the newest ones available.
	if _, err := client.ResolveVersion(context.Background(), "fmt", "1.2.3"); err == nil {
		t.Fatal("expected an error for an unknown version")
	} else if !strings.Contains(err.Error(), "11.2.0") {
		t.Fatalf("error should list the newest version, got %v", err)
	}
}
