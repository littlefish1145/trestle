package vcpkg

import (
	"context"
	"os/exec"
	"testing"
)

// TestPortVersionsReadsRealCheckout exercises the history lookup against an
// installed vcpkg checkout, so the version database and git-tree resolution
// cannot drift. It is skipped when vcpkg is not installed.
func TestPortVersionsReadsRealCheckout(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Skipf("vcpkg is not installed: %v", err)
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

func TestVersionDatabasePathShardsByFirstLetter(t *testing.T) {
	for name, want := range map[string]string{
		"fmt":    `C:\vcpkg\versions\f-\fmt.json`,
		"7zip":   `C:\vcpkg\versions\7-\7zip.json`,
		"catch2": `C:\vcpkg\versions\c-\catch2.json`,
		"Boost":  `C:\vcpkg\versions\b-\boost.json`,
	} {
		if got := versionDatabasePath(`C:\vcpkg`, name); got != want {
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

var _ = exec.Command
