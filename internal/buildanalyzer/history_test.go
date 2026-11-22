package buildanalyzer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryIsolationPredictionAndCorruption(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	cfg := filepath.Join(dir, "trestle.toml")
	scope := ConfigurationID(cfg, "debug", "clang-19")
	otherProfile := ConfigurationID(cfg, "release", "clang-19")
	otherToolchain := ConfigurationID(cfg, "debug", "clang-20")
	makeRun := func(scope, hash string, duration int64) *Execution {
		return &Execution{
			Scope: scope, Outcome: "succeeded", LogComplete: true, Jobs: 2, DurationMS: duration,
			Graph:   &Graph{Complete: true, Edges: []Edge{{ID: "compile", Outputs: []string{"obj/中文.obj"}}}},
			Metrics: &Metrics{Timings: []EdgeTiming{{Edge: 0, EndMS: duration, CommandHash: hash}}},
		}
	}
	for i, run := range []*Execution{
		makeRun(scope, "abcd", 90), makeRun(scope, "abcd", 100), makeRun(scope, "abcd", 10000),
		makeRun(otherProfile, "abcd", 100000), makeRun(otherToolchain, "abcd", 200000),
		makeRun(scope, "dcba", 800000),
	} {
		session := &Session{SchemaVersion: SchemaVersion, StartedAt: now.Add(time.Duration(i-10) * time.Hour), Outcome: "succeeded", Executions: []*Execution{run}}
		path, err := session.save(dir)
		if err != nil {
			t.Fatal(err)
		}
		if i == 4 {
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "history.json"), []byte("corrupt index"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	broken := filepath.Join(ReportsDir(dir), "corrupt")
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "report.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := makeRun(scope, "abcd", 110)
	session := &Session{StartedAt: now, Executions: []*Execution{run}}
	session.analyzeHistory(dir)
	result := run.History
	if result.SkippedReports != 2 || len(result.Predictions) != 1 ||
		result.Predictions[0].Samples != 3 || result.Predictions[0].PredictedMS != 100 ||
		*result.MeanAbsErrorMS != 10 || *result.LastSampleMAEMS != 9890 {
		t.Fatalf("history: %+v", result)
	}
	if session, _, err := LatestReport(dir); err != nil || session == nil {
		t.Fatalf("latest valid report: %+v, %v", session, err)
	}
}

func TestSimulationJobAndPoolLimits(t *testing.T) {
	graph := &Graph{
		Pools: map[string]int{"serial": 1},
		Edges: []Edge{{Pool: "serial"}, {Pool: "serial"}, {}, {Prerequisites: []int{0, 1, 2}}},
	}
	tasks := map[int]simTask{0: {10, 10, 10}, 1: {10, 10, 10}, 2: {15, 15, 15}, 3: {5, 5, 5}}
	if result := simulate(graph, tasks, 2); result == nil || result.MedianMS != 25 || result.LastSampleMS != 25 {
		t.Fatalf("parallel simulation: %+v", result)
	}
	if result := simulate(graph, tasks, 1); result == nil || result.MedianMS != 40 {
		t.Fatalf("serial simulation: %+v", result)
	}
}
