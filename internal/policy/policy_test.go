package policy

import (
	"context"
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
