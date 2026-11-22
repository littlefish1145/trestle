package buildanalyzer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"trestle/internal/ninja"
	"trestle/internal/plan"
)

func TestGraphWithRealDyndepAndMultiOutput(t *testing.T) {
	executable, err := exec.LookPath("ninja")
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "分析 工作区")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save("template.dd", "ninja_dyndep_version = 1\nbuild left | module: dyndep\nbuild result: dyndep | module\n")
	save("compile.d", "result: header\\ file.h\n")
	save("header file.h", "")
	commands := []string{"cp template.dd graph.dd", "touch left right module", "touch result"}
	if runtime.GOOS == "windows" {
		commands = []string{
			"cmd.exe /d /c copy /y template.dd graph.dd",
			"cmd.exe /d /c type nul > left & type nul > right & type nul > module",
			"cmd.exe /d /c type nul > result",
		}
	}
	buildPlan := plan.BuildPlan{
		Defaults: []string{"all"}, Pools: map[string]int{"serial": 1},
		Actions: []plan.Action{
			{ID: "scan", Rule: "scan", Command: plan.Command{Exe: "SCAN_FIXTURE"}, Inputs: []string{"template.dd"}, Outputs: []string{"graph.dd"}},
			{ID: "produce", Rule: "produce", Command: plan.Command{Exe: "PRODUCE_FIXTURE"}, Outputs: []string{"left"}, ImplicitOutputs: []string{"right"}, Dyndep: "graph.dd", OrderOnly: []string{"graph.dd"}, Pool: "serial"},
			{ID: "consume", Rule: "consume", Command: plan.Command{Exe: "CONSUME_FIXTURE"}, Implicit: []string{"right"}, Outputs: []string{"result"}, Dyndep: "graph.dd", OrderOnly: []string{"graph.dd"}, Depfile: &plan.DepfileSpec{Path: "compile.d"}},
			{ID: "all", Rule: "phony", Inputs: []string{"result"}, Outputs: []string{"all"}},
		},
	}
	// The fixture uses shell builtins. Write their command lines literally
	// while using the emitter for graph syntax and bindings.
	manifest, err := ninja.Emit(buildPlan)
	if err != nil {
		t.Fatal(err)
	}
	save("build.ninja", strings.NewReplacer("SCAN_FIXTURE", commands[0], "PRODUCE_FIXTURE", commands[1], "CONSUME_FIXTURE", commands[2]).Replace(string(manifest)))
	session := &Session{}
	ctx := context.WithValue(t.Context(), sessionKey{}, session)
	run := BeginExecution(ctx, dir, filepath.Join(dir, "trestle.toml"), "debug", []string{"all"})
	cmd := exec.CommandContext(ctx, executable)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ninja: %s, %v", out, err)
	}
	run.Finish(ctx, err, &buildPlan)
	graph := run.Graph
	if !run.LogComplete || !graph.Complete || len(run.Entries) != 5 || len(run.Metrics.Timings) != 3 {
		t.Fatalf("log warnings %v, graph warnings %v, entries %+v, timings %+v", run.Warnings, graph.Warnings, run.Entries, run.Metrics.Timings)
	}
	if graph.Pools["serial"] != 1 || graph.producers["module"] != 1 || graph.producers["right"] != 1 ||
		!slices.Equal(graph.Edges[2].Prerequisites, []int{0, 1}) ||
		!slices.Equal(graph.Edges[2].Discovered, []string{"header file.h"}) {
		t.Fatalf("incorrect graph: %+v", graph)
	}
}

func TestCriticalPathAndParallelTimeline(t *testing.T) {
	graph := &Graph{Edges: []Edge{{}, {Prerequisites: []int{0}}, {}, {Prerequisites: []int{1, 2}}}}
	order, _, err := graph.topological()
	if err != nil {
		t.Fatal(err)
	}
	path, total := criticalPath(graph, order, []int64{10, 0, 5, 7})
	if total != 17 || !slices.Equal(path, []int{0, 1, 3}) {
		t.Fatalf("critical path %v, %d", path, total)
	}
	timeline, peak := parallelTimeline([]EdgeTiming{{StartMS: 1, EndMS: 11}, {StartMS: 6, EndMS: 11}, {StartMS: 14, EndMS: 20}})
	want := []ParallelInterval{{0, 1, 0}, {1, 6, 1}, {6, 11, 2}, {11, 14, 0}, {14, 20, 1}}
	if peak != 2 || !reflect.DeepEqual(timeline, want) {
		t.Fatalf("parallel timeline %+v, peak %d", timeline, peak)
	}
	graph.Edges[0].Prerequisites = []int{3}
	if _, _, err := graph.topological(); err == nil {
		t.Fatal("cycle accepted")
	}
}
