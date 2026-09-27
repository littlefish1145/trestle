package policy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"trestle/internal/config"
)

func TestRulesApplyInOrderWithoutMutatingSource(t *testing.T) {
	cfg := config.Default("demo")
	cfg.Targets["lib"] = config.Target{Type: "static", Sources: []string{"src/lib.cpp"}}
	cfg.Packages["fmt"] = config.Package{Provider: "manual", IncludeDirs: []string{"include"}}
	cfg.Rules = []config.Rule{
		{When: `os == "never"`, CXX: "ignored"},
		{When: `os == "` + runtime.GOOS + `"`, CXX: "clang++", CompileFlags: []string{"-g"}},
		{When: `mode == "native"`, Target: "app", CompileFlags: []string{"-Wall"}, Packages: []string{"fmt"}, DependsOn: []string{"lib"}},
	}
	resolved, err := Apply(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Toolchain.CXX != "clang++" || len(resolved.Build.CompileFlags) != 1 || len(resolved.Targets["app"].Dependencies) != 2 || len(resolved.Targets["app"].CompileOptions) != 1 {
		t.Fatalf("unexpected resolution: %+v %+v", resolved.Build, resolved.Targets["app"])
	}
	if len(cfg.Targets["app"].Dependencies) != 0 || cfg.Toolchain.CXX != "auto" {
		t.Fatal("source mutated")
	}
}

func TestUnavailableTargetReasons(t *testing.T) {
	cfg := config.Default("demo")
	target := cfg.Targets["app"]
	target.When = `os == "never"`
	target.RequiresTools = []string{"definitely-absent-trestle-tool"}
	cfg.Targets["app"] = target
	status := Assess(context.Background(), cfg)["app"]
	if status.Ready || len(status.Reasons) < 2 {
		t.Fatalf("expected detailed reasons: %+v", status)
	}
	if err := CheckSelected(map[string]Status{"app": status}, []string{"app"}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("bad preflight: %v", err)
	}
}

func TestRuleReportAndTargetFlagScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("demo")
	cfg.Targets["worker"] = config.Target{Type: "executable", Sources: []string{"worker.cpp"}}
	cfg.Rules = []config.Rule{
		{When: `path("marker.txt")`, CXX: "clang++", CXXFlags: []string{"-O2"}},
		{When: `true`, Target: "app", CompileFlags: []string{"-Wall"}, CFlags: []string{"-Werror"}, CXXFlags: []string{"-Wextra"}, LinkFlags: []string{"-Wl,--as-needed"}},
		{When: `false`, Target: "worker", CFlags: []string{"-wrong"}},
	}
	resolved, report, err := ApplyWithReportAt(context.Background(), cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rules) != 3 || !report.Rules[0].Matched || !report.Rules[1].Matched || report.Rules[2].Matched {
		t.Fatalf("incorrect rule trace: %+v", report.Rules)
	}
	if got := resolved.Build.CXXFlags; len(got) != 1 || got[0] != "-O2" {
		t.Fatalf("project flags changed unexpectedly: %q", got)
	}
	app := resolved.Targets["app"]
	if len(app.CFlags) != 1 || app.CFlags[0] != "-Werror" || len(app.CXXFlags) != 1 || app.CXXFlags[0] != "-Wextra" || len(app.LinkOptions) != 1 {
		t.Fatalf("target flags not applied: %+v", app)
	}
	if len(resolved.Targets["worker"].CFlags) != 0 || len(cfg.Targets["app"].CFlags) != 0 {
		t.Fatal("target flags leaked or changed the source config")
	}
	found := false
	for _, change := range report.Changes {
		if change.Field == "targets.app.c_flags" && strings.Contains(change.After, "-Werror") {
			found = true
		}
	}
	if !found {
		t.Fatalf("final configuration diff omitted target flags: %+v", report.Changes)
	}
}
