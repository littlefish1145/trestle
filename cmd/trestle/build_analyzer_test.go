package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"trestle/internal/buildanalyzer"
	"trestle/internal/config"
)

func TestNormalizeBuildProfileFlag(t *testing.T) {
	for _, test := range []struct{ args, want []string }{
		{[]string{"--profile", "app"}, []string{"--profile-build", "app"}},
		{[]string{"--profile", "release"}, []string{"--profile", "release"}},
		{[]string{"--profile=debug", "--profile"}, []string{"--profile=debug", "--profile-build"}},
		{[]string{"-C", "--profile", "-profile", "release"}, []string{"-C", "--profile", "-profile", "release"}},
		{[]string{"--", "--profile"}, []string{"--", "--profile"}},
	} {
		if got := normalizeBuildProfileFlag(test.args); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%v => %v, want %v", test.args, got, test.want)
		}
	}
}

func TestProfileBuildRealCLI(t *testing.T) {
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip(err)
	}
	compiler := os.Getenv("TRESTLE_TEST_COMPILER")
	if compiler == "" {
		t.Skip("set TRESTLE_TEST_COMPILER to a working native C++ compiler")
	}
	root := filepath.Join(t.TempDir(), "中文 项目")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := run("init", []string{"-C", root, "-name", "analyzer"}); err != nil {
		t.Fatal(err)
	}
	if err := run("configure", []string{"-toolchain", compiler}); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "src", "main.cpp")
	header := filepath.Join(root, "src", "local.h")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(header, "#define RETURN_CODE 0\n")
	write(main, "#include \"local.h\"\nint main() { return RETURN_CODE; }\n")
	capture := func(fail bool, args ...string) *buildanalyzer.Session {
		t.Helper()
		prior, _ := filepath.Glob(filepath.Join(buildanalyzer.ReportsDir(root), "*", "report.json"))
		err := run("build", append([]string{"--profile", "--jobs", "2"}, args...))
		if (err != nil) != fail {
			t.Fatalf("build (expected failure %v): %v", fail, err)
		}
		paths, err := filepath.Glob(filepath.Join(buildanalyzer.ReportsDir(root), "*", "report.json"))
		if err != nil || len(paths) != len(prior)+1 {
			t.Fatalf("reports: %v, %v, before %v", paths, err, prior)
		}
		data, err := os.ReadFile(paths[len(paths)-1])
		if err != nil {
			t.Fatal(err)
		}
		var report buildanalyzer.Session
		if err := json.Unmarshal(data, &report); err != nil || report.SchemaVersion != 1 {
			t.Fatalf("report: %s, %v", data, err)
		}
		return &report
	}
	first := capture(false)
	if len(first.Executions) != 1 || !first.Executions[0].LogComplete || len(first.Executions[0].Entries) < 2 {
		t.Fatalf("first report: %+v", first)
	}
	reportDir := filepath.Join(buildanalyzer.ReportsDir(root), first.ID)
	traceData, err := os.ReadFile(filepath.Join(reportDir, "trace.json"))
	if err != nil || !json.Valid(traceData) || !strings.Contains(string(traceData), "\"ph\":\"X\"") {
		t.Fatalf("Trace JSON: %s, %v", traceData, err)
	}
	if err := run("analyze", []string{"html"}); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(reportDir, "report.html"))
	if err != nil || !strings.Contains(string(html), "Critical path") {
		t.Fatalf("offline HTML: %s, %v", html, err)
	}
	empty := capture(false)
	if len(empty.Executions) != 1 || !empty.Executions[0].LogComplete || len(empty.Executions[0].Entries) != 0 {
		t.Fatalf("empty report: %+v", empty)
	}
	impact, err := captureStdout(t, func() error { return run("analyze", []string{"impact", "src/local.h"}) })
	if err != nil || !strings.Contains(impact, "compile_") || !strings.Contains(impact, "link_") {
		t.Fatalf("impact: %s, %v", impact, err)
	}
	cfg, err := config.Load("trestle.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Build.CXXFlags = append(cfg.Build.CXXFlags, "-DTRESTLE_ANALYZER_TEST=1")
	if err := config.Save("trestle.toml", cfg); err != nil {
		t.Fatal(err)
	}
	changed := capture(false)
	why, err := captureStdout(t, func() error { return run("analyze", []string{"why"}) })
	if err != nil || len(changed.Executions[0].Entries) == 0 || !strings.Contains(why, "command_changed") {
		t.Fatalf("command change: %s, %v, explanations %+v", why, err, changed.Executions[0].Explanations)
	}
	write(header, "#define RETURN_CODE 1\n")
	rebuilt := capture(false)
	if len(rebuilt.Executions[0].Entries) < 2 {
		t.Fatalf("header did not reach compile and link: %+v", rebuilt.Executions[0])
	}
	for i, value := range []string{"2", "3"} {
		write(header, "#define RETURN_CODE "+value+"\n")
		next := capture(false)
		history := next.Executions[0].History
		if i == 1 && (history == nil || len(history.Predictions) == 0 || history.Simulation == nil) {
			t.Fatalf("history and constrained simulation: %+v, graph %+v", history, next.Executions[0].Graph)
		}
	}
	write(header, "#define RETURN_CODE 4\n")
	scheduled := capture(false, "--scheduler=history")
	execution := scheduled.Executions[0]
	if execution.RequestedScheduler != "history" ||
		(os.Getenv("TRESTLE_HISTORY_NINJA") != "" && (execution.Scheduler != "history" || execution.AppliedWeights == 0)) ||
		(os.Getenv("TRESTLE_HISTORY_NINJA") == "" && execution.Scheduler != "ninja") {
		t.Fatalf("history scheduler: %+v", execution)
	}
	write(main, "#error deliberate failure\n")
	failed := capture(true)
	if failed.Outcome != "failed" || len(failed.Executions) != 1 || failed.Executions[0].Outcome != "failed" {
		t.Fatalf("failed report: %+v", failed)
	}
}
