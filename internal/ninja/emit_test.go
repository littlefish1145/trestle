package ninja

import (
	"runtime"
	"strings"
	"testing"

	"trestle/internal/plan"
)

func TestEscapePath(t *testing.T) {
	got := EscapePath(`C:\work space\a$b:out`)
	want := `C$:\work$ space\a$$b$:out`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmit(t *testing.T) {
	buildPlan := plan.BuildPlan{
		Actions: []plan.Action{
			{ID: "compile", Rule: "compile", Command: plan.Command{Exe: `C:\Program Files\cc.exe`, Args: []string{"-c", "source.cpp", "-o", "object.o"}}, Inputs: []string{"source.cpp"}, Outputs: []string{"object.o"}, Depfile: &plan.DepfileSpec{Path: "object.o.d"}},
			{ID: "all", Rule: "phony", Command: plan.Command{Exe: "ninja"}, Inputs: []string{"object.o"}, Outputs: []string{"all"}},
		},
		Defaults: []string{"all"},
	}
	data, err := Emit(buildPlan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "rule compile") || !strings.Contains(text, "build object.o: compile") {
		t.Fatalf("missing compile edge:\n%s", text)
	}
	if !strings.Contains(text, "$out") || !strings.Contains(text, "depfile = object.o.d") {
		t.Fatalf("missing depfile/output variable:\n%s", text)
	}
}

func TestEmitUsesNinjaDefaultMSVCDependencyPrefix(t *testing.T) {
	data, err := Emit(plan.BuildPlan{Actions: []plan.Action{
		{ID: "compile", Rule: "compile", Command: plan.Command{Exe: "cl.exe", Args: []string{"/c", "main.cpp"}}, Inputs: []string{"main.cpp"}, Outputs: []string{"main.obj"}, Deps: "msvc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "msvc_deps_prefix") {
		t.Fatalf("MSVC prefix must remain Ninja's English default because the runner forces VSLANG=1033:\n%s", data)
	}
}

func TestEmitDoesNotQuoteWSLOptions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("WSL command quoting uses Windows shell semantics")
	}

	data, err := Emit(plan.BuildPlan{Actions: []plan.Action{
		{ID: "compile", Rule: "compile", Command: plan.Command{Exe: `C:\Windows\System32\wsl.exe`, Args: []string{"-d", "Ubuntu-24.04", "--cd", "/mnt/c/project", "--exec", "/usr/bin/gcc", "-c", "main.c"}}, Inputs: []string{"main.c"}, Outputs: []string{"main.o"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `"-d"`) || strings.Contains(text, `"--cd"`) || strings.Contains(text, `"--exec"`) {
		t.Fatalf("WSL options must not be quoted because wsl.exe treats quoted options as a Linux command:\n%s", text)
	}
	if !strings.Contains(text, `wsl.exe -d Ubuntu-24.04 --cd /mnt/c/project --exec /usr/bin/gcc`) {
		t.Fatalf("missing WSL command:\n%s", text)
	}
}

func TestEmitRuntimeCopyDoesNotReplaceOutputSuffixInsideInput(t *testing.T) {
	data, err := Emit(plan.BuildPlan{Actions: []plan.Action{
		{
			ID: "runtime", Rule: "runtime-copy",
			Command: plan.Command{Exe: "cmake", Args: []string{"-E", "copy_if_different", "../../installed/debug/bin/glfw3.dll", "bin/glfw3.dll"}},
			Inputs:  []string{"../../installed/debug/bin/glfw3.dll"}, Outputs: []string{"bin/glfw3.dll"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "cmake -E copy_if_different $in $out") || strings.Contains(text, "debug/$out") {
		t.Fatalf("runtime copy rule uses an ambiguous path substitution:\n%s", text)
	}
}
