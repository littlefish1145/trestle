package tui

import (
	"context"
	"strings"
	"testing"

	"trestle/internal/buildanalyzer"
)

func TestAnalyzerPaletteStartsBuildAndRendersReport(t *testing.T) {
	model, _ := freshConsole(t)
	called := make(chan string, 1)
	model.services.ProfileBuild = func(_ context.Context, member string, _ func(string)) error {
		called <- member
		return nil
	}
	model = queryCommands(openCommands(model), "profile build")
	if matches := model.matchingCommands(); len(matches) == 0 || matches[0].action != "profile-build" {
		t.Fatalf("profile command missing: %v", matches)
	}
	model, cmd := tap(model, "enter")
	if cmd == nil || !model.mutating || model.route != BuildRoute {
		t.Fatal("profiled build was not started")
	}
	feed(t, model, cmd, 2)
	select {
	case member := <-called:
		if member != "" {
			t.Fatalf("unexpected member %q", member)
		}
	default:
		t.Fatal("profiled build service was not called")
	}
	report := &buildanalyzer.Session{Outcome: "succeeded", DurationMS: 10, Executions: []*buildanalyzer.Execution{{
		Profile: "debug", Scheduler: "ninja", LogComplete: true,
		Graph:   &buildanalyzer.Graph{Complete: true, Edges: []buildanalyzer.Edge{{ID: "compile main"}}},
		Metrics: &buildanalyzer.Metrics{CriticalPath: []int{0}, CriticalPathMS: 9, PeakParallel: 1},
	}}}
	model, _ = freshConsole(t)
	model = queryCommands(openCommands(model), "open build analyzer")
	model, cmd = tap(model, "enter")
	if model.route != AnalyzerRoute || cmd == nil {
		t.Fatal("analyzer route unavailable")
	}
	updated, _ := model.Update(analyzerMessage{report: report, path: "report.json"})
	content := strings.Join(updated.(dashboardModel).analyzerLines(model.palette(), 100), "\n")
	for _, want := range []string{"report.json", "Critical path", "compile main"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q from analyzer page", want)
		}
	}
}
