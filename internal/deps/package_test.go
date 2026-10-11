package deps

import (
	"testing"

	"trestle/internal/deps/conan"
	"trestle/internal/deps/vcpkg"
)

func TestConvertEntriesStampsVcpkgProviderAndKeepsSource(t *testing.T) {
	entries := ConvertEntries([]vcpkg.Port{{
		Name: "fmt", Version: "10.2.1", Description: "formatting", Installed: true,
		Source: "local", PackageURL: "https://vcpkg.io/en/package/fmt.html",
		License: "MIT", Features: []string{"format"}, Dependencies: []string{"zlib"},
	}})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Provider != "vcpkg" {
		t.Fatalf("provider not stamped: %+v", entry)
	}
	if entry.Source != "local" || entry.Name != "fmt" || !entry.Installed {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.License != "MIT" || len(entry.Features) != 1 || len(entry.Dependencies) != 1 {
		t.Fatalf("metadata lost in conversion: %+v", entry)
	}
}

func TestConvertConanEntriesStampsConanProvider(t *testing.T) {
	entries := ConvertConanEntries([]conan.Package{
		{Name: "zlib", Version: "1.3.1", Installed: true, Source: "local", Updated: "2024-01-02 03:04:05 UTC"},
		{Name: "fmt", Version: "10.2.1", Source: "conancenter"},
	})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Provider != "conan" || entries[0].Name != "zlib" || !entries[0].Installed {
		t.Fatalf("unexpected local conan entry: %+v", entries[0])
	}
	if entries[1].Installed || entries[1].Source != "conancenter" {
		t.Fatalf("unexpected remote conan entry: %+v", entries[1])
	}
}

func TestConvertEntriesHandleEmptyInput(t *testing.T) {
	if got := ConvertEntries(nil); len(got) != 0 {
		t.Fatalf("expected empty vcpkg conversion, got %+v", got)
	}
	if got := ConvertConanEntries(nil); len(got) != 0 {
		t.Fatalf("expected empty conan conversion, got %+v", got)
	}
}

// A library available from both providers must appear once, with a candidate
// per provider, rather than as two separate rows.
func TestMergeCollapsesSameNameAcrossProviders(t *testing.T) {
	entries := []PackageEntry{
		{Name: "fmt", Version: "11.1.4", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "10.2.1", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Source: "vcpkg.io", Installed: true},
		{Name: "zlib", Version: "1.3.1", Provider: "conan", Source: "local", Installed: true},
	}
	packages := Merge(entries)
	if len(packages) != 2 {
		t.Fatalf("expected 2 merged packages, got %d: %+v", len(packages), packages)
	}
	// Installed packages lead the list.
	if packages[0].Name != "fmt" || !packages[0].Installed {
		t.Fatalf("installed package must sort first: %+v", packages)
	}
	if len(packages[0].Candidates) != 3 {
		t.Fatalf("fmt should keep one candidate per provider/version: %+v", packages[0].Candidates)
	}
	if packages[1].Name != "zlib" || len(packages[1].Candidates) != 1 {
		t.Fatalf("unexpected second package: %+v", packages[1])
	}
}

func TestMergeOrdersCandidatesNewestFirstPerProvider(t *testing.T) {
	packages := Merge([]PackageEntry{
		{Name: "fmt", Version: "9.1.0", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "12.2.0", Provider: "conan", Source: "conancenter"},
		{Name: "fmt", Version: "10.2.0", Provider: "conan", Source: "conancenter"},
	})
	if len(packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(packages))
	}
	var versions []string
	for _, candidate := range packages[0].Candidates {
		versions = append(versions, candidate.Version)
	}
	want := []string{"12.2.0", "10.2.0", "9.1.0"}
	for index, value := range want {
		if versions[index] != value {
			t.Fatalf("candidates not newest-first: got %v want %v", versions, want)
		}
	}
}

func TestMergePrefersInstalledCandidateWithinProvider(t *testing.T) {
	packages := Merge([]PackageEntry{
		{Name: "zlib", Version: "1.3.1", Provider: "conan", Source: "conancenter"},
		{Name: "zlib", Version: "1.2.13", Provider: "conan", Source: "local", Installed: true},
	})
	first := packages[0].Candidates[0]
	if !first.Installed || first.Version != "1.2.13" || first.Source != "local" {
		t.Fatalf("installed candidate must lead its provider group: %+v", packages[0].Candidates)
	}
}

func TestMergeFillsDescriptionAndSkipsBlankNames(t *testing.T) {
	packages := Merge([]PackageEntry{
		{Name: "  ", Version: "1.0", Provider: "conan"},
		{Name: "fmt", Version: "11.1.4", Provider: "conan"},
		{Name: "fmt", Version: "11.0.0", Provider: "vcpkg", Description: "formatting library"},
	})
	if len(packages) != 1 {
		t.Fatalf("blank names must be dropped: %+v", packages)
	}
	if packages[0].Description != "formatting library" {
		t.Fatalf("description not carried into the merged row: %+v", packages[0])
	}
}

func TestCompareVersionPrefersNewerAndFallsBackLexically(t *testing.T) {
	if compareVersion("12.2.0", "9.1.0") <= 0 {
		t.Fatal("12.2.0 must be newer than 9.1.0")
	}
	if compareVersion("1.0.1", "1.0") <= 0 {
		t.Fatal("1.0.1 must be newer than 1.0")
	}
	if compareVersion("1.0.1-rc1", "1.0.1") >= 0 {
		t.Fatal("an unparsable version must not outrank a real one")
	}
	if compareVersion("1.0", "1.0") != 0 {
		t.Fatal("equal versions must compare equal")
	}
}
