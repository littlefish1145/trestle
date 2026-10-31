package graph

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWorkspaceResolutionIsRepeatableAndDoesNotMutateConfiguration(t *testing.T) {
	wc, dir := workspaceFixture(t, wsRoot, map[string]string{"core": `schema_version=7
[project]
name="core"
[targets.provider]
type="static"
sources=["src/main.cpp"]
include_dirs=["include"]
[packages.manual]
provider="manual"
include_dirs=["external/include"]
`, "app": `schema_version=7
[project]
name="app"
[targets.consumer]
type="executable"
sources=["src/main.cpp"]
dependencies=[{target="core:provider",scope="public"}]
`})
	before, err := json.Marshal(wc)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ResolveWorkspace(t.Context(), wc, filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		next, err := ResolveWorkspace(t.Context(), wc, filepath.Join(dir, "build"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("resolution %d changed graph", i)
		}
	}
	after, err := json.Marshal(wc)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("graph resolution changed caller configuration:\nbefore=%s\nafter=%s", before, after)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveWorkspace(ctx, wc, filepath.Join(dir, "build")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution: %v", err)
	}
}
