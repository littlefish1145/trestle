package runner

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

type outputCollector struct{ lines strings.Builder }

func (sink *outputCollector) Progress(Progress)     {}
func (sink *outputCollector) Output(_, text string) { sink.lines.WriteString(text) }

func TestRunWithEventsHelper(t *testing.T) {
	if os.Getenv("TRESTLE_RUNNER_LONG_LINE_HELPER") != "1" {
		return
	}
	_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 200000)+"TAIL\n")
}

func TestRunWithEventsPreservesLongLine(t *testing.T) {
	sink := &outputCollector{}
	err := (Runner{}).RunWithEvents(context.Background(), os.Args[0], []string{"-test.run=^TestRunWithEventsHelper$"}, Options{Env: []string{"TRESTLE_RUNNER_LONG_LINE_HELPER=1"}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sink.lines.String(), strings.Repeat("x", 200000)+"TAIL") {
		t.Fatalf("long log line was cut: got %d bytes", sink.lines.Len())
	}
}

func TestParseStatus(t *testing.T) {
	line := "@@TRESTLE:3:10: 30%:2@@ CXX main.cpp"
	progress, ok := ParseStatus(line)
	if !ok || progress.Finished != 3 || progress.Total != 10 || progress.Percent != 30 || progress.Running != 2 {
		t.Fatalf("unexpected progress: %#v ok=%v", progress, ok)
	}
	if detail := StatusDetail(line); detail != "CXX main.cpp" {
		t.Fatalf("unexpected status detail: %q", detail)
	}
}
