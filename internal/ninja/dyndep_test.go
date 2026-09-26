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
