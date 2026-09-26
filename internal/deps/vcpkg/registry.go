package vcpkg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const OfficialIndexURL = "https://vcpkg.io/output.json"

type WebRegistry struct {
	IndexURL  string
	Client    *http.Client
	CachePath string
}

type registryCache struct {
	url    string
	loaded time.Time
	ports  []Port
}

var officialCache struct {
	sync.Mutex
	registryCache
}

type registryDocument struct {
	Source []registryPort `json:"Source"`
}

type diskRegistryCache struct {
	SavedAt time.Time `json:"saved_at"`
	Ports   []Port    `json:"ports"`
}

type registryPort struct {
	Name           string          `json:"Name"`
	Version        string          `json:"Version"`
	VersionSemver  string          `json:"Version-semver"`
	VersionDate    string          `json:"Version-date"`
	PortVersion    int             `json:"Port-Version"`
	Description    json.RawMessage `json:"Description"`
	Homepage       string          `json:"homepage"`
	HomepageLegacy string          `json:"Homepage"`
	License        string          `json:"License"`
	LastModified   string          `json:"LastModified"`
	Features       json.RawMessage `json:"Features"`
	Dependencies   json.RawMessage `json:"Dependencies"`
}

func (registry WebRegistry) Search(ctx context.Context, query string, limit int) ([]Port, error) {
	ports, err := registry.load(ctx)
	if err != nil {
		return nil, err
	}
	return filterPorts(ports, query, limit), nil
}

// SearchCached returns immediately and never accesses the network.
func (registry WebRegistry) SearchCached(query string, limit int) ([]Port, bool) {
	if ports, ok := registry.cached(); ok {
		return filterPorts(ports, query, limit), true
	}
	return nil, false
}

// Refresh bypasses persistent cache and downloads a current official index.
func (registry WebRegistry) Refresh(ctx context.Context) error {
	_, err := registry.loadRemote(ctx)
	return err
}

func filterPorts(ports []Port, query string, limit int) []Port {
	query = strings.ToLower(strings.TrimSpace(query))
	type rankedPort struct {
		port  Port
		score int
	}
	ranked := make([]rankedPort, 0)
	for _, port := range ports {
		score := rankPort(port, query)
		if query != "" && score == 0 {
			continue
		}
		ranked = append(ranked, rankedPort{port: port, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].port.Name < ranked[j].port.Name
	})
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, len(ranked))
	result := make([]Port, limit)
	for index := range result {
		result[index] = ranked[index].port
	}
	return result
}

func (registry WebRegistry) load(ctx context.Context) ([]Port, error) {
	if ports, ok := registry.cached(); ok {
		return ports, nil
	}
	return registry.loadRemote(ctx)
}

func (registry WebRegistry) cached() ([]Port, bool) {
	indexURL := registry.IndexURL
	if indexURL == "" {
		indexURL = OfficialIndexURL
	}
	if indexURL == OfficialIndexURL {
		officialCache.Lock()
		defer officialCache.Unlock()
		if officialCache.url == indexURL && time.Since(officialCache.loaded) < 15*time.Minute && len(officialCache.ports) > 0 {
			return append([]Port(nil), officialCache.ports...), true
		}
	}
	if registry.CachePath != "" {
		data, err := os.ReadFile(registry.CachePath)
		if err == nil {
			var cached diskRegistryCache
			if json.Unmarshal(data, &cached) == nil && len(cached.Ports) > 0 && time.Since(cached.SavedAt) < 7*24*time.Hour {
				return cached.Ports, true
			}
		}
	}
	return nil, false
}

func (registry WebRegistry) loadRemote(ctx context.Context) ([]Port, error) {
	indexURL := registry.IndexURL
	if indexURL == "" {
		indexURL = OfficialIndexURL
	}
	client := registry.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Trestle/1 vcpkg-registry-client")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("official vcpkg registry is unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official vcpkg registry returned %s", response.Status)
	}
	var document registryDocument
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode official vcpkg registry: %w", err)
	}
	ports := make([]Port, 0, len(document.Source))
	for _, item := range document.Source {
		if item.Name == "" {
			continue
		}
		version := firstNonEmpty(item.Version, item.VersionSemver, item.VersionDate)
		if item.PortVersion > 0 {
			version += fmt.Sprintf("#%d", item.PortVersion)
		}
		homepage := firstNonEmpty(item.Homepage, item.HomepageLegacy)
		ports = append(ports, Port{
			Name: item.Name, Version: version, Description: decodeText(item.Description),
			Source: "vcpkg.io", Homepage: homepage, License: item.License,
			Updated: item.LastModified, Features: decodeNames(item.Features),
			Dependencies: decodeNames(item.Dependencies),
			PackageURL:   "https://vcpkg.io/en/package/" + url.PathEscape(item.Name) + ".html",
		})
	}
	if indexURL == OfficialIndexURL {
		officialCache.Lock()
		officialCache.registryCache = registryCache{url: indexURL, loaded: time.Now(), ports: append([]Port(nil), ports...)}
		officialCache.Unlock()
	}
	if registry.CachePath != "" {
		if data, marshalErr := json.Marshal(diskRegistryCache{SavedAt: time.Now(), Ports: ports}); marshalErr == nil {
			if mkdirErr := os.MkdirAll(filepath.Dir(registry.CachePath), 0o755); mkdirErr == nil {
				_ = os.WriteFile(registry.CachePath, data, 0o644)
			}
		}
	}
	return ports, nil
}

func rankPort(port Port, query string) int {
	if query == "" {
		return 1
	}
	name := strings.ToLower(port.Name)
	description := strings.ToLower(port.Description)
	switch {
	case name == query:
		return 1000
	case strings.HasPrefix(name, query):
		return 700
	case strings.Contains(name, query):
		return 500
	case strings.Contains(description, query):
		return 200
	case isSubsequence(query, name):
		return 100
	}
	return 0
}

func isSubsequence(needle, value string) bool {
	if needle == "" {
		return true
	}
	index := 0
	for _, char := range value {
		if char == rune(needle[index]) {
			index++
			if index == len(needle) {
				return true
			}
		}
	}
	return false
}

func decodeText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var lines []string
	if json.Unmarshal(raw, &lines) == nil {
		return strings.Join(lines, " ")
	}
	return ""
}

func decodeNames(raw json.RawMessage) []string {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) == nil && name != "" {
			result = append(result, name)
			continue
		}
		var object map[string]any
		if json.Unmarshal(value, &object) != nil {
			continue
		}
		name, _ = object["name"].(string)
		if name == "" {
			name, _ = object["Name"].(string)
		}
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}
