package buildanalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"trestle/internal/ninja"
	"trestle/internal/plan"
)

func TestNinjaHistoryWorkItem(t *testing.T) {
	if os.Getenv("TRESTLE_NINJA_TEST_HELPER") != "1" {
		return
	}
	output := os.Args[len(os.Args)-1]
	data, err := os.ReadFile("delays.json")
	if err != nil {
		t.Fatal(err)
	}
	var delays map[string]int
	if err := json.Unmarshal(data, &delays); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Duration(delays[output]) * time.Millisecond)
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeNinjaHistoryExperiment(t *testing.T) {
	if os.Getenv("TRESTLE_HISTORY_NINJA") == "" {
		t.Skip("set TRESTLE_HISTORY_NINJA to the patched Ninja a00b184")
	}
	native, err := exec.LookPath("ninja")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(native, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "1.14.0.git" {
		t.Fatalf("experiment requires stock Ninja a00b184 (1.14.0.git): %s, %v", version, err)
	}
	t.Setenv("TRESTLE_NINJA_TEST_HELPER", "1")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var buildPlan plan.BuildPlan
	for _, spec := range []struct{ output, parent string }{{"prep", ""}, {"link", "prep"}, {"side1", ""}, {"side2", ""}} {
		inputs := []string{"delays.json"}
		if spec.parent != "" {
			inputs = append(inputs, spec.parent)
		}
		buildPlan.Actions = append(buildPlan.Actions, plan.Action{
			ID: plan.ID(spec.output), Rule: plan.ID(spec.output),
			Command: plan.Command{Exe: helper, Args: []string{"-test.run=^TestNinjaHistoryWorkItem$", "--", spec.output}},
			Inputs:  inputs, Outputs: []string{spec.output},
		})
	}
	buildPlan.Actions = append(buildPlan.Actions, plan.Action{ID: "all", Rule: "phony", Outputs: []string{"all"}, Inputs: []string{"link", "side1", "side2"}})
	manifest, err := ninja.Emit(buildPlan)
	if err != nil {
		t.Fatal(err)
	}
	write := func(dir, name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir string, useHistory bool, prior *history) *Execution {
		t.Helper()
		session := &Session{Outcome: "succeeded", history: prior}
		ctx := context.WithValue(t.Context(), sessionKey{}, session)
		if useHistory {
			ctx = WithHistoryScheduler(ctx)
		}
		execution := BeginExecution(ctx, dir, filepath.Join(dir, "trestle.toml"), "debug", []string{"all"})
		execution.Scope = "same-experiment-configuration"
		command, args, cleanup := execution.SchedulerCommand(ctx, []string{"-j", "2", "all"}, &buildPlan, nil)
		defer cleanup()
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Dir = dir
		execution.StartTiming()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ninja: %s, %v", out, err)
		}
		writer := execution.ExplainWriter()
		_, _ = writer.Write(out)
		writer.Flush()
		execution.Finish(ctx, err, &buildPlan)
		if !execution.LogComplete || len(execution.Metrics.Timings) != 4 {
			t.Fatalf("expected four dirty actions: %+v, %s", execution, out)
		}
		return execution
	}
	prior := &history{samples: map[historyKey][]int64{}}
	training := t.TempDir()
	write(training, "build.ninja", manifest)
	normal := []byte(`{"prep":100,"link":440,"side1":245,"side2":245}`)
	recordTraining := func(run *Execution) {
		prior.add((&Session{Outcome: "succeeded", Executions: []*Execution{run}}).historyIndex())
	}
	for i := 0; i < 3; i++ {
		write(training, "delays.json", normal)
		recordTraining(run(training, false, prior))
	}
	for delay := 1250; ; delay *= 2 {
		write(training, "delays.json", fmt.Appendf(nil, `{"prep":1,"link":1,"side1":%d,"side2":%d}`, delay, delay))
		last := run(training, false, prior)
		elapsed := func(index int) int64 {
			timing := last.Metrics.Timings[index]
			return timing.EndMS - timing.StartMS
		}
		if elapsed(0)+elapsed(1) < min(elapsed(2), elapsed(3)) {
			recordTraining(last)
			break
		}
		if delay >= 5000 {
			t.Fatal("training could not create an outlying last-sample priority; rerun the experiment on an idle machine")
		}
	}
	seed, err := os.ReadFile(filepath.Join(training, ".ninja_log"))
	if err != nil {
		t.Fatal(err)
	}
	var pairs [][2]int64
	for i := 0; i < 6; i++ {
		var measurements [2]*Execution
		for _, index := range []int{i % 2, 1 - i%2} {
			dir := filepath.Join(t.TempDir(), fmt.Sprintf("样本 %d", i))
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write(dir, "build.ninja", manifest)
			write(dir, ".ninja_log", seed)
			write(dir, "delays.json", normal)
			// Each invocation gets its own directory and the same warm Ninja
			// timing log. All four command outputs are absent in both arms.
			measurements[index] = run(dir, index == 1, prior)
		}
		stock, historical := measurements[0], measurements[1]
		if historical.Scheduler != "history" || historical.AppliedWeights != 4 ||
			!slices.Equal(edgeHashes(stock), edgeHashes(historical)) ||
			stock.Metrics.Timings[0].StartMS <= stock.Metrics.Timings[3].StartMS ||
			historical.Metrics.Timings[0].StartMS >= historical.Metrics.Timings[3].StartMS {
			t.Fatalf("expected equivalent dirty sets and a changed start order; native %+v, history %+v, warnings %v",
				stock.Metrics, historical.Metrics, historical.Warnings)
		}
		pairs = append(pairs, [2]int64{stock.DurationMS, historical.DurationMS})
	}
	t.Logf("native/history paired wall times in ms: %v", pairs)
}

func edgeHashes(run *Execution) []string {
	keys := make([]string, 0, len(run.Metrics.Timings))
	for _, timing := range run.Metrics.Timings {
		keys = append(keys, run.Graph.Edges[timing.Edge].ID+":"+timing.CommandHash)
	}
	return keys
}
