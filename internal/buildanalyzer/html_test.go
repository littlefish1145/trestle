package buildanalyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineHTMLReport(t *testing.T) {
	errorMS := 3.25
	session := &Session{ID: "session", Outcome: "succeeded", Executions: []*Execution{{
		Directory: `C:\中文 项目`, Profile: "debug", Scheduler: "ninja", LogComplete: true,
		Graph:   &Graph{Complete: true, Edges: []Edge{{ID: "<script>invalid</script>", Outputs: []string{"app.exe"}}}},
		Metrics: &Metrics{CriticalPath: []int{0}, CriticalPathMS: 10, PeakParallel: 1, Timings: []EdgeTiming{{Edge: 0, EndMS: 10}}},
		History: &HistoryAnalysis{MeanAbsErrorMS: &errorMS, LastSampleMAEMS: &errorMS},
	}}}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := session.ExportHTML(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, want := range []string{"Build Analyzer", "中文 项目", "&lt;script&gt;", "Median-based MAE 3.2 ms", "app.exe"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in HTML", want)
		}
	}
	for _, unwanted := range []string{"<script>invalid", "<link rel=", "%!"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("unexpected %q in HTML", unwanted)
		}
	}
}

func BenchmarkAnalyzerSimulation(b *testing.B) {
	const size = 10000
	graph := &Graph{Edges: make([]Edge, size), Pools: map[string]int{"link": 2}}
	tasks := make(map[int]simTask, size)
	for i := range graph.Edges {
		graph.Edges[i].ID = fmt.Sprint(i)
		if i >= 20 {
			graph.Edges[i].Prerequisites = []int{i - 20}
		}
		if i%10 == 0 {
			graph.Edges[i].Pool = "link"
		}
		tasks[i] = simTask{10, 12, 9}
	}
	for b.Loop() {
		if simulate(graph, tasks, 12) == nil {
			b.Fatal("invalid benchmark graph")
		}
	}
}
