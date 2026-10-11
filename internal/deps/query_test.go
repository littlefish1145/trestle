package deps

import "testing"

func TestParseQuerySplitsNameAndVersion(t *testing.T) {
	for raw, want := range map[string]PackageQuery{
		"fmt":              {Name: "fmt"},
		"  fmt  ":          {Name: "fmt"},
		"fmt:11.2.0":       {Name: "fmt", Version: "11.2.0"},
		"vcpkg:fmt:11.2.0": {Name: "fmt", Provider: "vcpkg", Version: "11.2.0"},
		"conan:fmt":        {Name: "fmt", Provider: "conan"},
		"manual:fmt":       {Name: "fmt", Provider: "manual"},
	} {
		got := ParseQuery(raw)
		if got.Name != want.Name || got.Version != want.Version || got.Provider != want.Provider {
			t.Fatalf("ParseQuery(%q) = name=%q provider=%q version=%q, want name=%q provider=%q version=%q",
				raw, got.Name, got.Provider, got.Version, want.Name, want.Provider, want.Version)
		}
	}
}

// A conan reference already carries its own version and channel syntax, so it
// must not be mistaken for a name:version pair.
func TestParseQueryLeavesConanReferencesAlone(t *testing.T) {
	for _, raw := range []string{
		"fmt/11.1.4",
		"zlib/1.3.1@conan/stable",
		"zlib[shared,fPIC]",
		"openssl/3.2.1@x/stable[with_tls]",
	} {
		got := ParseQuery(raw)
		if got.Version != "" || got.Provider != "" || got.Name != raw {
			t.Fatalf("ParseQuery(%q) = %+v, want it untouched", raw, got)
		}
	}
}

// A version-like suffix that is not a provider prefix must not be swallowed.
func TestParseQueryKeepsUnknownPrefixesInTheName(t *testing.T) {
	got := ParseQuery("weird:thing")
	if got.Name != "weird:thing" || got.Provider != "" || got.Version != "" {
		t.Fatalf("ParseQuery(weird:thing) = %+v", got)
	}
	if got := ParseQuery("fmt:"); got.Name != "fmt:" || got.Version != "" {
		t.Fatalf("ParseQuery(fmt:) = %+v", got)
	}
	if got := ParseQuery(":11.2.0"); got.Name != ":11.2.0" {
		t.Fatalf("ParseQuery(:11.2.0) = %+v", got)
	}
}

func TestParseQueryEmptyTerm(t *testing.T) {
	if got := ParseQuery("   "); got.Name != "" || got.Exact() {
		t.Fatalf("ParseQuery(blank) = %+v", got)
	}
}

func TestSearchTermAppendsVersionOnlyForConan(t *testing.T) {
	if got := (PackageQuery{Name: "fmt", Version: "11.2.0"}).SearchTerm(); got != "fmt" {
		t.Fatalf("vcpkg-style term = %q", got)
	}
	if got := (PackageQuery{Name: "fmt", Provider: "conan", Version: "11.2.0"}).SearchTerm(); got != "fmt/11.2.0" {
		t.Fatalf("conan term = %q", got)
	}
}

func TestParseQueryRoundTrips(t *testing.T) {
	for _, raw := range []string{"fmt", "fmt:11.2.0", "vcpkg:fmt:11.2.0", "conan:fmt"} {
		if got := ParseQuery(raw).String(); got != raw {
			t.Fatalf("round trip %q -> %q", raw, got)
		}
	}
}

func TestMarkRequestedVersionFlagsMatches(t *testing.T) {
	entries := []PackageEntry{
		{Name: "fmt", Version: "11.2.0"},
		{Name: "fmt", Version: "11.0.2"},
		{Name: "fmt", Version: "10.2.1"},
	}
	MarkRequestedVersion(entries, "11.0.2")
	if entries[0].Requested {
		t.Fatal("wrong version was flagged")
	}
	if !entries[1].Requested {
		t.Fatal("requested version was not flagged")
	}
}

func TestMarkRequestedVersionAcceptsPortVersionSuffix(t *testing.T) {
	entries := []PackageEntry{{Name: "fmt", Version: "11.0.2"}}
	MarkRequestedVersion(entries, "11.0.2#3")
	if !entries[0].Requested {
		t.Fatal("a #n suffix should still match the upstream version")
	}
	MarkRequestedVersion(entries, "")
	if !entries[0].Requested {
		t.Fatal("an empty request must not clear an existing flag")
	}
}

// The quick install prefers vcpkg even when conan offers a newer version, since
// vcpkg is the toolchain trestle drives by default.
func TestPreferredPrefersVcpkgOverNewerConan(t *testing.T) {
	pkg := Package{Name: "fmt", Candidates: []Candidate{
		{Version: "12.2.0", Provider: "conan", Source: "conancenter"},
		{Version: "11.2.0", Provider: "vcpkg", Source: "current"},
	}}
	candidate, ok := pkg.Preferred("")
	if !ok || candidate.Provider != "vcpkg" || candidate.Version != "11.2.0" {
		t.Fatalf("Preferred = %+v (ok=%v), want the vcpkg release", candidate, ok)
	}
}

func TestPreferredFallsBackToConanWhenVcpkgAbsent(t *testing.T) {
	pkg := Package{Name: "zlib", Candidates: []Candidate{
		{Version: "1.2.13", Provider: "conan"},
		{Version: "1.3.1", Provider: "conan"},
	}}
	candidate, ok := pkg.Preferred("")
	if !ok || candidate.Version != "1.3.1" {
		t.Fatalf("Preferred = %+v (ok=%v), want the newest conan release", candidate, ok)
	}
}

func TestPreferredHonoursRequestedVersion(t *testing.T) {
	pkg := Package{Name: "fmt", Candidates: []Candidate{
		{Version: "12.2.0", Provider: "conan"},
		{Version: "11.2.0", Provider: "vcpkg"},
		{Version: "10.2.1", Provider: "vcpkg"},
	}}
	candidate, ok := pkg.Preferred("10.2.1")
	if !ok || candidate.Version != "10.2.1" {
		t.Fatalf("Preferred = %+v (ok=%v), want the requested version", candidate, ok)
	}
}

func TestPreferredWithoutCandidates(t *testing.T) {
	if _, ok := (Package{Name: "fmt"}).Preferred(""); ok {
		t.Fatal("an empty package has no preferred candidate")
	}
}

// A version the user asked for must survive merging and stay findable.
func TestRequestedVersionSurvivesMerge(t *testing.T) {
	entries := []PackageEntry{
		{Name: "fmt", Version: "11.2.0", Provider: "vcpkg"},
		{Name: "fmt", Version: "10.2.1", Provider: "vcpkg", Requested: true},
	}
	packages := Merge(entries)
	if len(packages) != 1 {
		t.Fatalf("expected one merged package, got %d", len(packages))
	}
	found := false
	for _, candidate := range packages[0].Candidates {
		if candidate.Version == "10.2.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("requested version missing from candidates: %+v", packages[0].Candidates)
	}
}
