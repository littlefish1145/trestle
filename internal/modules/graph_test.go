package modules

import (
	"testing"

	"trestle/internal/modules/p1689"
)

func TestBuildGraphAndMissingProvider(t *testing.T) {
	documents := []p1689.Document{
		{Rules: []p1689.Rule{{PrimaryOutput: "math.cppm", Provides: []p1689.Module{{LogicalName: "math"}}}}},
		{Rules: []p1689.Rule{{PrimaryOutput: "main.cpp", Requires: []p1689.Module{{LogicalName: "math"}}}}},
	}
	graph, err := BuildGraph(documents)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges["math.cppm"]) != 1 {
		t.Fatalf("missing edge: %#v", graph.Edges)
	}
	missing := []p1689.Document{{Rules: []p1689.Rule{{PrimaryOutput: "main.cpp", Requires: []p1689.Module{{LogicalName: "missing"}}}}}}
	if _, err := BuildGraph(missing); err == nil {
		t.Fatal("expected missing provider error")
	}
}
