package condition

import (
	"strings"
	"testing"
)

func TestEvaluateReadOnlyFacts(t *testing.T) {
	facts := Facts{OS: "windows", Arch: "amd64", Mode: "wsl", Tool: func(name string) bool { return name == "clang++" }, WSL: func(name string) bool { return name == "Ubuntu" }, WSLTool: func(distribution, name string) bool { return distribution == "Ubuntu" && name == "clang++" }, Env: func(name string) string {
		if name == "SDK" {
			return "ready"
		}
		return ""
	}}
	for _, expression := range []string{
		`os == "windows" && arch == "amd64"`,
		`mode == "wsl" && wsl("Ubuntu") && tool("clang++")`,
		`wsl_tool("Ubuntu", "clang++")`,
		`env("SDK") == "ready" && !tool("missing")`,
	} {
		result, err := Evaluate(expression, facts)
		if err != nil || !result {
			t.Fatalf("%s: %v %v", expression, result, err)
		}
	}
}

func TestRejectsHiddenUnsafeExpression(t *testing.T) {
	for _, expression := range []string{`true || run("rm")`, `false && unknown`, `os + "x"`, `env("X")`, `tool(12)`} {
		if err := Validate(expression); err == nil {
			t.Errorf("accepted %q", expression)
		} else if strings.Contains(err.Error(), "panic") {
			t.Fatal(err)
		}
	}
}
