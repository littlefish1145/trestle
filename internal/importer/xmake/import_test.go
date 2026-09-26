package xmake

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLegacyTarget(t *testing.T) {
	data := []byte(`The information of target(swgl):
    kind: shared
    files:
      -> src/*.c -> ./xmake.lua:8
    includedirs:
      -> src -> ./xmake.lua:9
    defines:
      -> SWGL_EXPORTS
    links:
      -> user32
    syslinks:
      -> m
    deps:
      -> platform
    cflags:
      -> -Wall
    cxflags:
      -> -g
    ldflags:
      -> -s
`)
	got, err := ParseLegacyTarget("swgl", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "swgl" || got.Kind != "shared" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	checks := []struct {
		name string
		got  []string
		want []string
	}{
		{"files", got.Files, []string{"src/*.c"}},
		{"includes", got.IncludeDirs, []string{"src"}},
		{"defines", got.Defines, []string{"SWGL_EXPORTS"}},
		{"links", got.Links, []string{"user32"}},
		{"syslinks", got.SysLinks, []string{"m"}},
		{"deps", got.Deps, []string{"platform"}},
		{"cflags", got.CFlags, []string{"-Wall"}},
		{"cxflags", got.CXFlags, []string{"-g"}},
		{"ldflags", got.LDFlags, []string{"-s"}},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %#v; want %#v", check.name, check.got, check.want)
		}
	}
}

func TestParseJSONAliases(t *testing.T) {
	got, err := Parse([]byte(`{"targets":{"app":{"kind":"binary","sourcefiles":["src/main.cpp"],"syslinks":["d2d1"],"cxflags":["-Wall"],"cxxflags":["-std=c++20"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "app" || got[0].Kind != "binary" {
		t.Fatalf("unexpected target: %#v", got)
	}
	if !reflect.DeepEqual(got[0].SourceFiles, []string{"src/main.cpp"}) || !reflect.DeepEqual(got[0].SysLinks, []string{"d2d1"}) {
		t.Fatalf("aliases were not parsed: %#v", got[0])
	}
}

func TestParseXmakeJSONWithValueAndSourceObjects(t *testing.T) {
	data := []byte(`{
		"name": "swgl",
		"kind": {"value": "shared", "source": {"line": 7, "file": ".\\xmake.lua"}},
		"files": [{"value": "src/*.c", "source": {"line": 8, "file": ".\\xmake.lua"}}],
		"includedirs": [{"value": "src", "source": {"line": 9, "file": ".\\xmake.lua"}}],
		"cflags": [
			{"value": "-Wall", "source": {"line": 4, "file": ".\\xmake.lua"}},
			{"value": "-Wextra", "source": {"line": 4, "file": ".\\xmake.lua"}}
		],
		"syslinks": [{"value": "m", "source": {"line": 10, "file": ".\\xmake.lua"}}]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "swgl" || got[0].Kind != "shared" {
		t.Fatalf("unexpected target: %#v", got)
	}
	if !reflect.DeepEqual(got[0].Files, []string{"src/*.c"}) {
		t.Fatalf("files = %#v", got[0].Files)
	}
	if !reflect.DeepEqual(got[0].CFlags, []string{"-Wall", "-Wextra"}) {
		t.Fatalf("cflags = %#v", got[0].CFlags)
	}
	if !reflect.DeepEqual(got[0].SysLinks, []string{"m"}) {
		t.Fatalf("syslinks = %#v", got[0].SysLinks)
	}
}

func TestParsePerTargetJSONInfersKindFromLinker(t *testing.T) {
	data := []byte("xmake diagnostic before JSON\n" + `{
		"linker": {"kind": "sh", "program": "D:\\LLVM\\bin\\clang++.exe"},
		"name": "swgl",
		"includedirs": [{"value": "src", "source": {"file": ".\\xmake.lua", "line": 9}}],
		"cflags": [{"value": "-Wall", "source": {"file": ".\\xmake.lua", "line": 4}}]
	}` + "\ntrailing diagnostic")
	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "swgl" || got[0].Kind != "shared" {
		t.Fatalf("unexpected target: %#v", got)
	}
}

func TestParseLegacyTargetInfersKindFromTargetFile(t *testing.T) {
	got, err := ParseLegacyTarget("swgl", []byte("The information of target(swgl):\n    targetfile: build\\windows\\x64\\swgl.dll\n    files:\n      -> src/*.c\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "shared" || got.TargetFile != `build\windows\x64\swgl.dll` {
		t.Fatalf("unexpected target: %#v", got)
	}
}

func TestIntrospectFallsBackToLegacyPerTargetProtocol(t *testing.T) {
	run := func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch command {
		case "show -l targets":
			return []byte("swgl\nvoxel\n"), nil
		case "show -t swgl":
			return []byte("kind: shared\nfiles:\n  -> src/*.c\n"), nil
		case "show -t voxel":
			return []byte("kind: binary\nfiles:\n  -> game/*.c\ndeps:\n  -> swgl\n"), nil
		default:
			return []byte("unsupported option"), errors.New("exit status 1")
		}
	}
	var messages []string
	got, err := introspect(context.Background(), "xmake", ".", func(message string) {
		messages = append(messages, message)
	}, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "swgl" || got[1].Name != "voxel" {
		t.Fatalf("unexpected targets: %#v", got)
	}
	if !reflect.DeepEqual(got[1].Deps, []string{"swgl"}) {
		t.Fatalf("dependency was not imported: %#v", got[1])
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "compatibility protocol") {
		t.Fatalf("unexpected progress: %#v", messages)
	}
}

func TestParseTargetNamesIgnoresHeading(t *testing.T) {
	got := parseTargetNames([]byte("The targets:\n    swgl\n    voxel\n"))
	if !reflect.DeepEqual(got, []string{"swgl", "voxel"}) {
		t.Fatalf("got %#v", got)
	}
}
