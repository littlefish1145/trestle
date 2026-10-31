package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"trestle/internal/plan"
)

func TestTranslateWSLShaderActionPathsPrecisely(t *testing.T) {
	action := plan.Action{Command: plan.Command{Exe: "wsl.exe", Args: []string{"-d", "ArchLinux", "--cd", "/build space", "--exec", "/usr/bin/glslc", "-Iinclude space", "-DVALUE=1", `C:/shader space/main.comp`, "-o", `C:/out/result.spv`, "-MF", `C:/out/result.spv.d`}}}
	var paths []string
	err := translateWSLAction(&action, func(value string) (string, error) { paths = append(paths, value); return "mapped:" + value, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"include space", "C:/shader space/main.comp", "C:/out/result.spv", "C:/out/result.spv.d"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if action.Command.Args[3] != "/build space" || action.Command.Args[7] != "-DVALUE=1" || action.Command.Args[6] != "-Imapped:include space" {
		t.Fatalf("runner/nonpath arguments changed: %v", action.Command.Args)
	}
	native := plan.Action{Command: plan.Command{Exe: "glslc", Args: []string{"-Iinclude"}}}
	if err := translateWSLAction(&native, func(value string) (string, error) { t.Fatal("native shader argument translated"); return "", nil }); err != nil {
		t.Fatal(err)
	}
}
func TestWSLArgumentTranslationUsesEachShaderRunnerDistribution(t *testing.T) {
	// UNC validation happens before invoking WSL. The error must name the
	// action's distribution, not the unrelated native host fallback.
	build := plan.BuildPlan{Actions: []plan.Action{{Command: plan.Command{Exe: "wsl.exe", Args: []string{"-d", "ArchLinux", "--exec", "glslc", `-I\\wsl.localhost\Foreign\include`}}}}}
	err := translateWSLArguments(context.Background(), "Ubuntu-24.04", &build)
	if err == nil || !strings.Contains(err.Error(), `selected distribution is "ArchLinux"`) {
		t.Fatalf("shader used wrong distribution: %v", err)
	}
}

func TestWindowsWSLPathSyntaxIndependentOfHost(t *testing.T) {
	for _, value := range []string{`C:/source/main.cpp`, `d:\build space\out.o`, `C:relative`, `\\server\share\file`, `//wsl.localhost/Ubuntu/file`} {
		if !isWindowsWSLPath(value) {
			t.Errorf("Windows path not recognized: %q", value)
		}
	}
	for _, value := range []string{"", "/usr/include", "../src/main.cpp", "-DVALUE=C:/data", "https://example.com", "1:/invalid"} {
		if isWindowsWSLPath(value) {
			t.Errorf("non-Windows path misclassified: %q", value)
		}
	}
	for _, runner := range []string{"wsl.exe", `C:\Windows\System32\wsl.exe`, "C:/Windows/System32/WSL.EXE"} {
		action := plan.Action{Command: plan.Command{Exe: runner, Args: []string{"--exec", "/usr/bin/glslc", "C:/source/main.comp"}}}
		calls := 0
		err := translateWSLAction(&action, func(value string) (string, error) {
			calls++
			return "/mnt/c/source/main.comp", nil
		})
		if err != nil || calls != 1 || action.Command.Args[2] != "/mnt/c/source/main.comp" {
			t.Errorf("runner %q failed to translate its Windows argument: %+v, calls=%d, err=%v", runner, action, calls, err)
		}
	}
}
