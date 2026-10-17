package condition

import "testing"

func tableFacts() Facts {
	return Facts{
		OS:      "windows",
		Arch:    "amd64",
		Mode:    "native",
		Tool:    func(name string) bool { return name == "clang++" },
		WSL:     func(name string) bool { return name == "Ubuntu" },
		Package: func(name string) bool { return name == "fmt" },
		Path:    func(name string) bool { return name == "/usr/bin/gcc" },
		WSLTool: func(distribution, name string) bool {
			return distribution == "Ubuntu" && name == "clang++"
		},
		Env: func(name string) string {
			if name == "SDK" {
				return "ready"
			}
			return ""
		},
	}
}

func TestEvaluateTable(t *testing.T) {
	cases := []struct {
		expression string
		want       bool
	}{
		{`true`, true},
		{`false`, false},
		{`!false`, true},
		{`!true`, false},
		{`(true)`, true},
		{`os == "windows"`, true},
		{`os != "linux"`, true},
		{`arch == "amd64"`, true},
		{`mode == "native"`, true},
		{`tool("clang++")`, true},
		{`!tool("missing")`, true},
		{`wsl("Ubuntu")`, true},
		{`path("/usr/bin/gcc")`, true},
		{`env("SDK") == "ready"`, true},
		{`env("SDK") != "ready"`, false},
		{`wsl_tool("Ubuntu", "clang++")`, true},
		{`wsl_tool("Debian", "clang++")`, false},
		{`true && true`, true},
		{`true && false`, false},
		{`true || false`, true},
		{`os == "windows" && tool("clang++") && !wsl("Debian")`, true},
		{`false && tool("never-called")`, false},
		{`true || tool("never-called")`, true},
	}
	facts := tableFacts()
	for _, tc := range cases {
		got, err := Evaluate(tc.expression, facts)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.expression, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.expression, got, tc.want)
		}
	}
}

func TestEvaluateDefaultsMissingProbesToFalse(t *testing.T) {
	facts := Facts{}
	for _, expression := range []string{
		`tool("clang")`,
		`wsl("Ubuntu")`,
		`path("/bin/gcc")`,
		`wsl_tool("Ubuntu", "clang")`,
	} {
		got, err := Evaluate(expression, facts)
		if err != nil {
			t.Errorf("%s: unexpected error %v", expression, err)
			continue
		}
		if got {
			t.Errorf("%s: got true, want false", expression)
		}
	}
	if got, err := Evaluate(`env("MISSING") == ""`, facts); err != nil || !got {
		t.Errorf("env missing: got %v, err %v", got, err)
	}
}

func TestValidateAccepts(t *testing.T) {
	valid := []string{
		``,
		`true`,
		`os == "windows"`,
		`tool("clang++") && arch == "amd64"`,
		`wsl_tool("Ubuntu", "clang++")`,
		`!(tool("x"))`,
		`env("SDK") == "ready"`,
		`path("/bin/gcc") || wsl("Ubuntu")`,
		`(true)`,
	}
	for _, expression := range valid {
		if err := Validate(expression); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", expression, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	invalid := []string{
		`unknown`,
		`os + "x"`,
		`tool(12)`,
		`env("X")`,
		`tool("x") && os`,
		`wsl_tool("a")`,
		`1`,
		`run("rm")`,
		`true || run("rm")`,
		`os == 1`,
		`!"a"`,
		`before("x")`,
	}
	for _, expression := range invalid {
		if err := Validate(expression); err == nil {
			t.Errorf("Validate(%q) = nil, want error", expression)
		}
	}
}
