package vcpkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPortVersionsReadsEveryVersionScheme(t *testing.T) {
	for _, scheme := range []string{"version", "version-semver", "version-date", "version-string"} {
		t.Run(scheme, func(t *testing.T) {
			root := t.TempDir()
			current, old := "2.0.0", "1.0.0"
			if scheme == "version-date" {
				current, old = "2025-10-01", "2024-12-01"
			}
			writeVersionDatabase(t, root, "demo", fmt.Sprintf(`{"versions":[
				{%q:%q,"port-version":0}, {%q:%q,"port-version":1}, {%q:%q}, {}
			]}`, scheme, current, scheme, current, scheme, old))
			portDir := filepath.Join(root, "ports", "demo")
			if err := os.MkdirAll(portDir, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf(`{"name":"demo",%q:%q,"port-version":1}`, scheme, current)
			if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			client := Client{Root: root}
			versions, err := client.PortVersions(context.Background(), "demo")
			if err != nil || len(versions) != 3 {
				t.Fatalf("versions = %+v, err = %v", versions, err)
			}
			if versions[0].Version != current || versions[0].PortVersion != 1 || !versions[0].Current || versions[1].Current || versions[2].Version != old {
				t.Fatalf("scheme %s history/current revision incorrect: %+v", scheme, versions)
			}
			matched, err := client.ResolveVersion(context.Background(), "demo", old)
			if err != nil || len(matched) != 1 || matched[0].Version != old {
				t.Fatalf("historical lookup = %+v, err = %v", matched, err)
			}
		})
	}
}

func TestExpandPortVersionsKeepsFullHistoryAndSearchMetadata(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "demo", hugeDatabase(250))
	client := Client{Root: root}
	versions, err := client.ExpandPortVersions(context.Background(), []Port{
		{Name: "demo", Version: "2.0.249", Description: "demo library", Installed: true, Features: []string{"tools"}, License: "MIT"},
		{Name: "new-port", Version: "1.0.0#2", Description: "not versioned yet", Features: []string{"extra"}},
	})
	if err != nil || len(versions) != 251 {
		t.Fatalf("versions = %d, err = %v; history must not be truncated", len(versions), err)
	}
	if !versions[0].Installed || !versions[0].Current || versions[0].License != "MIT" || versions[1].Installed || versions[1].Current {
		t.Fatalf("current/installed state incorrect: %+v", versions[:2])
	}
	if versions[1].Description != "demo library" || len(versions[1].Features) != 1 {
		t.Fatalf("search summary metadata lost: %+v", versions[1])
	}
	fallback := versions[250]
	if fallback.Port != "new-port" || fallback.Version != "1.0.0" || fallback.PortVersion != 2 || !fallback.Current || len(fallback.Features) != 1 {
		t.Fatalf("fallback = %+v", fallback)
	}
}

func TestExpandPortVersionsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Client{Root: t.TempDir()}).ExpandPortVersions(ctx, []Port{{Name: "demo"}})
	if err != context.Canceled {
		t.Fatalf("err = %v, want canceled", err)
	}
}
