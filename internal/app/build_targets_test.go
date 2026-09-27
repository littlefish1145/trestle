package app

import (
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
)

func TestSelectBuildTargetsUsesConfiguredDefaultAndExplicitIsolation(t *testing.T) {
	cfg := config.Default("demo")
	cfg.Targets["dump"] = config.Target{Type: "executable"}
	cfg.Build.DefaultTargets = []string{"app"}
	selected, err := selectBuildTargets(cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "app" {
		t.Fatalf("unexpected default targets: %#v", selected)
	}
	selected, err = selectBuildTargets(cfg, []string{"dump"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "dump" {
		t.Fatalf("explicit target was not isolated: %#v", selected)
	}
}

func TestProgressWriterGroupsCompileFailure(t *testing.T) {
	w := &progressWriter{outputs: map[model.TargetID]string{}, failedTargets: map[string]bool{}, diagnostics: map[string][]string{}}
	w.observe("FAILED: obj/peekg/main.cpp.o")
	w.observe("src/main.cpp(12): error C2065: broken")
	if !w.failedTargets["peekg"] {
		t.Fatalf("target was not classified: %#v", w.failedTargets)
	}
	found := false
	for key := range w.diagnostics {
		if strings.Contains(key, "peekg · compile · main.cpp") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostic was not grouped: %#v", w.diagnostics)
	}
}

func TestProgressWriterPreservesCompleteDiagnostics(t *testing.T) {
	var lines []string
	writer := &progressWriter{callback: func(line string) { lines = append(lines, line) }}
	input := "  indented compiler note\nwsl: startup warning\nNote: including file: generated.hpp\n"
	if _, err := writer.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if len(lines) != 3 || lines[0] != "  indented compiler note" || lines[1] != "wsl: startup warning" || lines[2] != "Note: including file: generated.hpp" {
		t.Fatalf("lost build output: %#v", lines)
	}
}
