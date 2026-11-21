package app

import (
	"testing"

	"trestle/internal/plan"
)

func TestMeasuredCriticalPathUsesDependenciesAndDeduplicatesOutputs(t *testing.T) {
	actions := []plan.Action{
		{ID: "parallel", Outputs: []string{"parallel.o"}},
		{ID: "link", Outputs: []string{"program"}, Inputs: []string{"alias", "parallel.o"}},
		{ID: "alias", Outputs: []string{"alias"}, OrderOnly: []string{"main.o", "main.dwo"}},
		{ID: "compile", Outputs: []string{"main.o", "main.dwo"}},
	}
	timings := []NinjaTiming{
		{Output: "parallel.o", StartMS: 0, EndMS: 6},
		{Output: "main.o", StartMS: 1, EndMS: 9},
		{Output: "main.dwo", StartMS: 1, EndMS: 9},
		{Output: "program", StartMS: 15, EndMS: 20},
	}
	result := measuredCriticalPath(actions, timings)
	if result == nil || result.DurationMS != 13 || len(result.Actions) != 2 ||
		result.Actions[0].Action != "compile" || result.Actions[1].Action != "link" {
		t.Fatalf("unexpected critical path: %+v", result)
	}
	timings[2].EndMS++
	if result := measuredCriticalPath(actions, timings); result != nil {
		t.Fatalf("accepted ambiguous multi-output timing: %+v", result)
	}
}

func TestMeasuredCriticalPathRejectsCyclesAndUnrelatedTimings(t *testing.T) {
	actions := []plan.Action{
		{ID: "one", Outputs: []string{"one"}, Inputs: []string{"two"}},
		{ID: "two", Outputs: []string{"two"}, Inputs: []string{"one"}},
	}
	if result := measuredCriticalPath(actions, []NinjaTiming{{Output: "one", EndMS: 10}}); result != nil {
		t.Fatalf("accepted an action cycle: %+v", result)
	}
	if result := measuredCriticalPath(actions[:1], []NinjaTiming{{Output: "other", EndMS: 10}}); result != nil {
		t.Fatalf("attributed an unrelated output: %+v", result)
	}
}
