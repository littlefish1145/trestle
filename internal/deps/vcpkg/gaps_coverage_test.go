package vcpkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/fsx"
)

// SearchPorts propagates the underlying vcpkg failure instead of returning a
// half-filled page of ports.
func TestSearchPortsPropagatesSearchFailure(t *testing.T) {
	client := newFakeClient(t, "search")
	ports, err := client.SearchPorts(context.Background(), "boom")
	if err == nil || !strings.Contains(err.Error(), "vcpkg search failed") {
		t.Fatalf("SearchPorts = %v, %v; want search failure", ports, err)
	}
	if ports != nil {
		t.Fatalf("ports = %v; want nil", ports)
	}
}

// Every install entry point refuses to run while another operation holds the
// installed tree lock.
func TestInstallOperationsReportBusyResource(t *testing.T) {
	client := newFakeClient(t, "install")
	lock, err := fsx.TryLock(context.Background(), filepath.Join(client.Root, "installed"), "test holds the tree")
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer lock.Close()
	if err := client.Install(context.Background(), []string{"quiet"}, ""); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("Install error = %v; want E_RESOURCE_BUSY", err)
	}
	if _, err := client.InstallCapture(context.Background(), []string{"quiet"}, ""); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("InstallCapture error = %v; want E_RESOURCE_BUSY", err)
	}
	if err := client.InstallWithProgress(context.Background(), []string{"quiet"}, "", nil); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("InstallWithProgress error = %v; want E_RESOURCE_BUSY", err)
	}
}

// InstallWithProgress surfaces loud failures with the captured output tail and
// silent failures with the exit error alone.
func TestInstallWithProgressReportsFailures(t *testing.T) {
	client := newFakeClient(t, "install")
	var seen []string
	err := client.InstallWithProgress(context.Background(), []string{"fail-loud"}, "", func(line string) {
		seen = append(seen, line)
	})
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_INSTALL") || !strings.Contains(err.Error(), "compile error") {
		t.Fatalf("loud failure = %v", err)
	}
	if len(seen) == 0 || !strings.Contains(seen[0], "compile error") {
		t.Fatalf("progress lines = %v; want the error line", seen)
	}
	err = client.InstallWithProgress(context.Background(), []string{"fail-silent"}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_INSTALL") {
		t.Fatalf("silent failure = %v", err)
	}
}

// Refresh falls back to the shared default HTTP client when none is injected.
func TestRefreshUsesDefaultHTTPClient(t *testing.T) {
	document := `{"Source": [{"Name": "fmt", "Version": "11.2.0", "Version-semver": "11.2.0", "Port-Version": 1, "Description": "formatting"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	registry := WebRegistry{IndexURL: server.URL, CachePath: filepath.Join(t.TempDir(), "cache.json")}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	ports, err := registry.Search(context.Background(), "fmt", 10)
	if err != nil || len(ports) != 1 || ports[0].Name != "fmt" {
		t.Fatalf("search = %v, %v", ports, err)
	}
}

// A status database without an entry yields no dependencies instead of an error.
func TestInstalledDependenciesWithoutStatusEntry(t *testing.T) {
	if deps := InstalledDependencies(Layout{Root: t.TempDir(), Triplet: "x64-windows"}, "demo"); deps != nil {
		t.Fatalf("deps = %v; want nil", deps)
	}
}

// Go toolchain architecture and OS names map onto vcpkg triplet names.
func TestRankTripletsTranslatesGoNomenclature(t *testing.T) {
	candidates, err := RankTriplets(TripletRequest{OS: "windows", Arch: "386", Available: []string{"x86-windows", "x64-windows"}})
	if err != nil || len(candidates) != 1 || candidates[0].Name != "x86-windows" {
		t.Fatalf("386 candidates = %v, %v", candidates, err)
	}
	candidates, err = RankTriplets(TripletRequest{OS: "darwin", Arch: "amd64", Available: []string{"x64-osx", "x64-linux"}})
	if err != nil || len(candidates) != 1 || candidates[0].Name != "x64-osx" {
		t.Fatalf("darwin candidates = %v, %v", candidates, err)
	}
}

// A summary line is skipped instead of being mistaken for a port named "The".
func TestSearchPortsVersionsSkipsNonPortLines(t *testing.T) {
	client := newFakeClient(t, "search")
	versions, err := client.SearchPortsVersions(context.Background(), "truthy", 0)
	if err != nil || versions != nil {
		t.Fatalf("versions = %v, %v; want empty", versions, err)
	}
}

// Resolving against a checkout without a version database fails loudly.
func TestResolveVersionWithoutDatabase(t *testing.T) {
	client := newFakeClient(t, "search")
	if _, err := client.ResolveVersion(context.Background(), "fmt", "11.2.0"); err == nil {
		t.Fatal("ResolveVersion accepted a checkout without a version database")
	}
}

// Private requires and private libs are parsed instead of being dropped.
func TestParsePCReadsPrivateMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "priv.pc")
	mustWrite(t, path, "Name: priv\nRequires.private: zlib (>= 1.3)\nLibs.private: -lz\n")
	parsed, err := ParsePC(path)
	if err != nil || len(parsed.RequiresPrivate) != 1 || len(parsed.LibsPrivate) != 1 {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
}

// A malformed root makes the glob fail instead of silently listing nothing.
func TestAvailableTripletsReportsGlobErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "[")
	if _, err := AvailableTriplets(root); err == nil {
		t.Fatal("AvailableTriplets accepted a malformed pattern")
	}
}

// Directories cannot be scanned as pkg-config files; the read failure reaches
// the caller instead of producing an empty PcFile.
func TestParsePCReportsReadFailures(t *testing.T) {
	if _, err := ParsePC(t.TempDir()); err == nil {
		t.Fatal("ParsePC accepted a directory")
	}
}
