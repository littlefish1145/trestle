package ninja

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trestle/internal/plan"
)

func TestLongArchiveBuildUsesResponseFile(t *testing.T) {
	ninjaPath, err := exec.LookPath("ninja")
	if err != nil {
		t.Skip("ninja unavailable")
	}
	ar, err := exec.LookPath("llvm-ar")
	if err != nil && runtime.GOOS != "darwin" {
		ar, err = exec.LookPath("ar")
	}
	if err != nil {
		t.Skip("response-file capable archiver unavailable")
	}
	root := t.TempDir()
	var inputs []string
	for i := range 750 {
		name := fmt.Sprintf("object with space $%04d %s.o", i, strings.Repeat("a", 72))
		if err := os.WriteFile(filepath.Join(root, name), []byte("archive fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, name)
	}
	output := "large library.a"
	action := plan.Action{
		ID: "archive", Rule: "archive", Inputs: inputs, Outputs: []string{output},
		Command: plan.Command{Exe: ar, Args: append([]string{"rcs", output}, inputs...), Response: plan.ResponseGNU},
	}
	data, err := Emit(plan.BuildPlan{Actions: []plan.Action{action}, Defaults: []string{output}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rspfile = large$ library.a.rsp") {
		t.Fatal("oversized archive command did not use a response file")
	}
	if err := os.WriteFile(filepath.Join(root, "build.ninja"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), ninjaPath, "-C", root)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("ninja archive: %v\n%s", err, output)
	}
	list := exec.CommandContext(t.Context(), ar, "t", filepath.Join(root, output))
	contents, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect archive: %v\n%s", err, contents)
	}
	if entries := strings.Split(strings.TrimSpace(string(contents)), "\n"); len(entries) != len(inputs) ||
		strings.TrimSpace(entries[0]) != inputs[0] || strings.TrimSpace(entries[len(entries)-1]) != inputs[len(inputs)-1] {
		t.Fatalf("incorrect archive members: got %d, want %d", len(entries), len(inputs))
	}
}
