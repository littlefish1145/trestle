package conan

import (
	"os"
	"os/exec"
	"testing"
)

// TestParseRealConanOutput feeds the parser whatever the installed conan
// actually prints, so the remote-keyed shape can never drift again. It is
// skipped when conan is unavailable.
func TestParseRealConanOutput(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_LIVE") != "1" {
		t.Skip("set TRESTLE_TEST_LIVE=1 to exercise installed package managers and remote services")
	}
	if _, err := exec.LookPath("conan"); err != nil {
		t.Skip("conan is not installed")
	}
	client := Client{Executable: "conan"}
	remotes, err := client.Remotes(t.Context())
	if err != nil {
		t.Fatalf("remote list: %v", err)
	}
	if len(remotes) == 0 {
		t.Skip("no conan remotes configured")
	}
	t.Logf("remotes: %v", remotes)

	found, err := client.SearchRemotes(t.Context(), "fmt", remotes)
	if err != nil {
		t.Fatalf("remote search: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("remote search returned nothing")
	}
	var newest, oldest string
	for _, entry := range found {
		if entry.Name != "fmt" {
			t.Fatalf("unexpected package name %q", entry.Name)
		}
		if entry.Source != remotes[0] {
			t.Fatalf("unexpected source %q", entry.Source)
		}
		if entry.Installed {
			t.Fatal("a remote recipe must not be reported as installed")
		}
		if entry.PackageURL != "https://conan.io/center/fmt" {
			t.Fatalf("unexpected package URL %q", entry.PackageURL)
		}
		if entry.Version == "" {
			t.Fatalf("missing version: %+v", entry)
		}
		// compareVersions reports whether the first argument is newer.
		if newest == "" || compareVersions(entry.Version, newest) > 0 {
			newest = entry.Version
		}
		if oldest == "" || compareVersions(entry.Version, oldest) < 0 {
			oldest = entry.Version
		}
	}
	t.Logf("found %d versions, newest=%s oldest=%s", len(found), newest, oldest)
	if compareVersions(newest, oldest) <= 0 {
		t.Fatalf("newest should sort above oldest: newest=%q oldest=%q", newest, oldest)
	}
	if found[0].Version != newest {
		t.Fatalf("first row should be the newest version, got %q want %q", found[0].Version, newest)
	}
}
