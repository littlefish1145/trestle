package ninja

import (
	"reflect"
	"strings"
	"testing"
	"trestle/internal/plan"
)

func TestMultiInputCommandPreservesEachArgumentExactlyOnce(t *testing.T) {
	inputs := []string{"obj/a.o", "obj/b.o", "obj/c.o"}
	args := append([]string{"rcs", "lib/out.a"}, inputs...)
	action := plan.Action{ID: "archive", Rule: "archive", Command: plan.Command{Exe: "ar", Args: args}, Inputs: inputs, Outputs: []string{"lib/out.a"}}
	before := append([]string(nil), args...)
	command := command(action)
	if strings.Contains(command, "$in") {
		t.Fatalf("multi-input expansion duplicates inputs: %s", command)
	}
	for _, input := range inputs {
		if strings.Count(command, input) != 1 {
			t.Fatalf("input %s duplicated or lost: %s", input, command)
		}
	}
	if !reflect.DeepEqual(args, before) {
		t.Fatal("emission mutated the plan")
	}
}
func TestReplacePathsDoesNotRewriteUnrelatedArgumentSubstrings(t *testing.T) {
	for _, arg := range []string{"-DHEADER=main.cpp", "main.cpp.backup", "prefix/main.cpp", "obj/a.o.d"} {
		if got := replacePaths(arg, []string{"main.cpp"}, []string{"obj/a.o"}); got != arg {
			t.Fatalf("%q became %q", arg, got)
		}
	}
}
