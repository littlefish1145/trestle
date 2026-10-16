package vcpkg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRegistryCache(t *testing.T, path string, savedAt time.Time, document string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"saved_at":"` + savedAt.UTC().Format(time.RFC3339Nano) + `","ports":` + document + `}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The in-process official index serves a search without touching disk or
// network; its timestamp and emptiness decide whether it may be used.
func TestSearchCachedUsesOfficialMemoryIndex(t *testing.T) {
	officialCache.Lock()
	previous := officialCache.registryCache
	officialCache.registryCache = registryCache{
		url:    OfficialIndexURL,
		loaded: time.Now(),
		ports:  []Port{{Name: "seeded", Version: "1.0.0"}},
	}
	officialCache.Unlock()
	defer func() {
		officialCache.Lock()
		officialCache.registryCache = previous
		officialCache.Unlock()
	}()

	ports, ok := (WebRegistry{}).SearchCached("seeded", 0)
	if !ok || len(ports) != 1 || ports[0].Name != "seeded" {
		t.Fatalf("ports = %+v, ok = %v", ports, ok)
	}
	if ports, ok := (WebRegistry{}).SearchCached("missing", 0); !ok || len(ports) != 0 {
		t.Fatalf("filtered ports = %+v, ok = %v", ports, ok)
	}
}

func TestSearchCachedUsesFreshDiskCache(t *testing.T) {
	path := writeRegistryCache(t, filepath.Join(t.TempDir(), "cache", "vcpkg-index.json"), time.Now(), `[{"Name":"fmt","Version":"11.2.0"}]`)
	ports, ok := (WebRegistry{CachePath: path}).SearchCached("fmt", 0)
	if !ok || len(ports) != 1 || ports[0].Name != "fmt" {
		t.Fatalf("ports = %+v, ok = %v", ports, ok)
	}
}

func TestSearchCachedRejectsStaleCorruptAndEmptyDiskCaches(t *testing.T) {
	fresh := func(t *testing.T) string { return filepath.Join(t.TempDir(), "cache", "vcpkg-index.json") }
	stale := writeRegistryCache(t, fresh(t), time.Now().Add(-8*24*time.Hour), `[{"Name":"fmt","Version":"11.2.0"}]`)
	if _, ok := (WebRegistry{CachePath: stale}).SearchCached("fmt", 0); ok {
		t.Fatal("a cache older than a week must not answer")
	}
	corruptPath := filepath.Join(t.TempDir(), "cache", "vcpkg-index.json")
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corruptPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := (WebRegistry{CachePath: corruptPath}).SearchCached("fmt", 0); ok {
		t.Fatal("a corrupt cache must not answer")
	}
	empty := writeRegistryCache(t, fresh(t), time.Now(), `[]`)
	if _, ok := (WebRegistry{CachePath: empty}).SearchCached("fmt", 0); ok {
		t.Fatal("an empty cache must not count as a hit")
	}
	if _, ok := (WebRegistry{CachePath: fresh(t)}).SearchCached("fmt", 0); ok {
		t.Fatal("a missing cache file must not answer")
	}
}

// A search that finds the disk cache never calls the network, which is what
// keeps the package page fast and offline-friendly.
func TestSearchPrefersFreshDiskCacheOverNetwork(t *testing.T) {
	path := writeRegistryCache(t, filepath.Join(t.TempDir(), "cache", "vcpkg-index.json"), time.Now(), `[{"Name":"fmt","Version":"11.2.0"}]`)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	registry := WebRegistry{IndexURL: closed.URL, Client: closed.Client(), CachePath: path}
	ports, err := registry.Search(context.Background(), "fmt", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ports) != 1 || ports[0].Version != "11.2.0" {
		t.Fatalf("ports = %+v", ports)
	}
}

const registryFixtureDocument = `{"Source":[
	{"Name":"fmt","Version":"12.2.0","Port-Version":1,"Description":["fast","safe"],"homepage":"https://fmt.dev","License":"MIT","Features":["unicode"],"Dependencies":[{"name":"vcpkg-cmake"}],"LastModified":"2024-01-01"},
	{"Name":"legacy","Version-semver":"1.2.3","Homepage":"https://legacy.example"},
	{"Name":"dated","Version-date":"2024-05-05","Port-Version":2},
	{"Name":"","Version":"9.9"}
]}`

func TestRefreshDownloadsDecodesAndCaches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(registryFixtureDocument))
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "cache", "vcpkg-index.json")
	registry := WebRegistry{IndexURL: server.URL, Client: server.Client(), CachePath: cachePath}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("refresh did not write the disk cache: %v", err)
	}
	// The refresh hit also populated the in-process cache used by Search.
	ports, err := registry.Search(context.Background(), "dated", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ports) != 1 || ports[0].Version != "2024-05-05#2" {
		t.Fatalf("ports = %+v", ports)
	}
	// The written cache must be readable on its own.
	stored, ok := (WebRegistry{CachePath: cachePath}).SearchCached("legacy", 0)
	if !ok || len(stored) != 1 || stored[0].Version != "1.2.3" || stored[0].Homepage != "https://legacy.example" {
		t.Fatalf("stored = %+v, ok = %v", stored, ok)
	}
}

func TestRefreshReportsRegistryFailures(t *testing.T) {
	status := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "boom", http.StatusInternalServerError)
	}))
	defer status.Close()
	_, err := (WebRegistry{IndexURL: status.URL, Client: status.Client()}).Search(context.Background(), "", 0)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want the HTTP status", err)
	}
	if err := (WebRegistry{IndexURL: status.URL, Client: status.Client()}).Refresh(context.Background()); err == nil {
		t.Fatal("refresh must report a non-200 status")
	}

	broken := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("{not json"))
	}))
	defer broken.Close()
	if err := (WebRegistry{IndexURL: broken.URL, Client: broken.Client()}).Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want a decode error", err)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if err := (WebRegistry{IndexURL: closed.URL, Client: closed.Client()}).Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err = %v, want a transport error", err)
	}

	if err := (WebRegistry{IndexURL: "://bad", Client: http.DefaultClient}).Refresh(context.Background()); err == nil {
		t.Fatal("a malformed index URL must be reported")
	}
}

// A cache path whose parent cannot be created must not fail the download the
// user actually asked for.
func TestRefreshSurvivesUnwritableCachePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"Source":[{"Name":"fmt","Version":"12.2.0"}]}`))
	}))
	defer server.Close()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := WebRegistry{IndexURL: server.URL, Client: server.Client(), CachePath: filepath.Join(blocker, "cache.json")}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestRankPortScores(t *testing.T) {
	cases := []struct {
		port  Port
		query string
		want  int
	}{
		{Port{Name: "fmt", Description: "formatting"}, "fmt", 1000},
		{Port{Name: "fmt-doc"}, "fmt", 700},
		{Port{Name: "libfmt"}, "fmt", 500},
		{Port{Name: "spdlog", Description: "uses fmt internally"}, "fmt", 200},
		{Port{Name: "fast-memory-telemetry"}, "fmt", 100},
		{Port{Name: "zlib", Description: "compression"}, "fmt", 0},
		{Port{Name: "zlib"}, "", 1},
	}
	for _, test := range cases {
		if got := rankPort(test.port, test.query); got != test.want {
			t.Fatalf("rankPort(%+v, %q) = %d, want %d", test.port, test.query, got, test.want)
		}
	}
}

func TestIsSubsequenceEdges(t *testing.T) {
	cases := []struct {
		needle, value string
		want          bool
	}{
		{"", "anything", true},
		{"fmt", "fast-memory-telemetry", true},
		{"ba", "abc", false},
		{"abcdef", "abc", false},
	}
	for _, test := range cases {
		if got := isSubsequence(test.needle, test.value); got != test.want {
			t.Fatalf("isSubsequence(%q, %q) = %v, want %v", test.needle, test.value, got, test.want)
		}
	}
}

func TestDecodeNamesHandlesEveryDependencyShape(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{`null`, nil},
		{`["a","","b"]`, []string{"a", "b"}},
		{`[{"name":"x"},{"Name":"y"}]`, []string{"x", "y"}},
		{`[17]`, nil},
		{`"nope"`, nil},
	}
	for _, test := range cases {
		got := decodeNames(json.RawMessage(test.raw))
		if strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Fatalf("decodeNames(%s) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestFilterPortsLimits(t *testing.T) {
	ports := []Port{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if got := filterPorts(ports, "", 0); len(got) != 3 {
		t.Fatalf("unlimited = %+v", got)
	}
	if got := filterPorts(ports, "", 2); len(got) != 2 || got[0].Name != "a" {
		t.Fatalf("limited = %+v", got)
	}
	if got := filterPorts(ports, "zzz", 0); len(got) != 0 {
		t.Fatalf("non-matching = %+v", got)
	}
}
