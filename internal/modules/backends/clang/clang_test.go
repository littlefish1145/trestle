package clang

import (
	"reflect"
	"testing"

	"trestle/internal/modules"
)

func TestWSLModuleCommandsUseRunnerAndRelativeBMIReferences(t *testing.T) {
	backend := Backend{Compiler: "/usr/bin/clang++", Standard: "c++20", Runner: "wsl.exe", RunnerArgs: []string{"-d", "Ubuntu", "--cd", "/mnt/c/work/build/debug"}}
	references := []modules.Reference{{LogicalName: "math", Path: "bmi/math.pcm"}}
	exe, args, err := backend.CompileModuleObject("../../main.cpp", "obj/main.o", references)
	if err != nil || exe != "wsl.exe" {
		t.Fatalf("command = %s %#v: %v", exe, args, err)
	}
	want := []string{"-d", "Ubuntu", "--cd", "/mnt/c/work/build/debug", "--exec", "/usr/bin/clang++", "-std=c++20", "-MMD", "-MF", "obj/main.o.d", "-c", "../../main.cpp", "-o", "obj/main.o", "-fmodule-file=math=bmi/math.pcm"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
