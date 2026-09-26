package vcpkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebRegistrySearchAndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"Source":[
			{"Name":"fmt","Version":"12.2.0","Port-Version":1,"Description":"formatting library","homepage":"https://fmt.dev","License":"MIT","Features":[],"Dependencies":[{"name":"vcpkg-cmake"}]},
			{"Name":"spdlog","Version":"1.17.0","Description":["fast logging","for C++"],"Features":[{"name":"fmt"}]}
		]}`))
	}))
	defer server.Close()

	registry := WebRegistry{IndexURL: server.URL, Client: server.Client()}
	ports, err := registry.Search(context.Background(), "formatting", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 || ports[0].Name != "fmt" || ports[0].Version != "12.2.0#1" {
		t.Fatalf("unexpected results: %#v", ports)
	}
	if ports[0].License != "MIT" || len(ports[0].Dependencies) != 1 {
		t.Fatalf("metadata was not decoded: %#v", ports[0])
	}
}

func TestWebRegistryRanksExactNamesFirst(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"Source":[{"Name":"fmt-doc","Version":"1"},{"Name":"fmt","Version":"2"}]}`))
	}))
	defer server.Close()
	ports, err := (WebRegistry{IndexURL: server.URL, Client: server.Client()}).Search(context.Background(), "fmt", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].Name != "fmt" {
		t.Fatalf("exact match should rank first: %#v", ports)
	}
}

func TestParseSearchOutputSkipsFeatureRows(t *testing.T) {
	lines := parseSearchOutput("fmt 12.0 formatting library\n    fmt[unicode] Unicode support\nspdlog 1.0 logging\n")
	if len(lines) != 2 || lines[0] != "fmt 12.0 formatting library" || lines[1] != "spdlog 1.0 logging" {
		t.Fatalf("feature rows should not become packages: %#v", lines)
	}
}
