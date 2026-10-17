package ninja

import (
	"strings"
	"testing"
)

func TestEmitDyndep(t *testing.T) {
	data, err := EmitDyndep(Dyndep{Version: 1, Build: "build.ninja", Inputs: []string{"a.o"}, Outputs: []string{"b.o"}, Binds: map[string][]string{"a.o": {"x.h"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ninja_dyndep_version = 1") || !strings.Contains(string(data), "bind = x.h") {
		t.Fatalf("unexpected dyndep: %s", data)
	}
}

func TestEmitDyndepFull(t *testing.T) {
	data, err := EmitDyndep(Dyndep{
		Version: 1,
		Build:   "build.ninja",
		Restat:  true,
		Inputs:  []string{"a o", "b"},
		Outputs: []string{"out"},
		Binds:   map[string][]string{"a.o": {"x.h", "y h"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "ninja_dyndep_version = 1\n" +
		"build = build.ninja\n" +
		"restat = 1\n" +
		"inputs = a$ o b\n" +
		"outputs = out\n" +
		"build a.o: dyndep\n" +
		"  bind = x.h y$ h\n"
	if string(data) != want {
		t.Fatalf("unexpected dyndep:\n got %q\nwant %q", data, want)
	}
}
