package config

import (
	"strings"
	"testing"
)

// TestValidateChecksSectionsInOrder pins the order in which Validate reports
// problems: a fault in an earlier section must hide a fault in a later one, so
// splitting Validate into per-section helpers cannot silently reorder checks.
func TestValidateChecksSectionsInOrder(t *testing.T) {
	cyclicTargets := func() map[string]Target {
		return map[string]Target{
			"a": {Type: "executable", Sources: []string{"a.cpp"}, Dependencies: []Dependency{{Target: "b", Scope: "private"}}},
			"b": {Type: "static", Sources: []string{"b.cpp"}, Dependencies: []Dependency{{Target: "a", Scope: "private"}}},
		}
	}

	tests := []struct {
		name string
		want string
		cfg  func() Config
	}{
		{
			name: "newer schema beats a missing project name",
			want: "E_CONFIG_NEWER_SCHEMA",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.SchemaVersion = CurrentSchemaVersion + 1
				cfg.Project.Name = ""
				cfg.Targets = nil
				return cfg
			},
		},
		{
			name: "older schema is reported as old",
			want: "E_CONFIG_OLD_SCHEMA",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.SchemaVersion = CurrentSchemaVersion - 1
				return cfg
			},
		},
		{
			name: "missing project name beats an empty target table",
			want: "project.name is required",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Project.Name = ""
				cfg.Targets = nil
				return cfg
			},
		},
		{
			name: "empty target table beats an unsupported toolchain mode",
			want: "at least one target is required",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Targets = nil
				cfg.Toolchain.Mode = "bogus"
				return cfg
			},
		},
		{
			name: "toolchain mode beats an unknown default target",
			want: "toolchain.mode",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Toolchain.Mode = "bogus"
				cfg.Build.DefaultTargets = []string{"ghost"}
				return cfg
			},
		},
		{
			name: "unknown default target beats a bad compiler preset",
			want: "build.default_targets references unknown target",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Build.DefaultTargets = []string{"ghost"}
				cfg.CompilerPresets["p"] = CompilerPreset{Mode: "bogus"}
				return cfg
			},
		},
		{
			name: "bad compiler preset beats a bad target type",
			want: "compiler preset",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.CompilerPresets["p"] = CompilerPreset{Mode: "bogus"}
				cfg.Targets = map[string]Target{"a": {Type: "bogus", Sources: []string{"a.cpp"}}}
				cfg.Build.DefaultTargets = nil
				return cfg
			},
		},
		{
			name: "bad target beats a malformed rule",
			want: `target "a" has unsupported type`,
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Targets = map[string]Target{"a": {Type: "bogus", Sources: []string{"a.cpp"}}}
				cfg.Build.DefaultTargets = nil
				cfg.Rules = []Rule{{When: ""}}
				return cfg
			},
		},
		{
			name: "malformed rule beats an unsupported package provider",
			want: "rule 1 requires when",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Rules = []Rule{{When: ""}}
				cfg.Packages["fmt"] = Package{Provider: "brew", Port: "fmt"}
				return cfg
			},
		},
		{
			name: "unsupported package provider beats an empty task command",
			want: "unsupported provider",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Packages["fmt"] = Package{Provider: "brew", Port: "fmt"}
				cfg.Tasks = map[string]Task{"t": {Command: []string{"  "}}}
				return cfg
			},
		},
		{
			name: "empty task command beats a dependency cycle",
			want: `task "t" has an empty command`,
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Targets = cyclicTargets()
				cfg.Build.DefaultTargets = nil
				cfg.Tasks = map[string]Task{"t": {Command: []string{"  "}}}
				return cfg
			},
		},
		{
			name: "dependency cycle beats an unsupported package format",
			want: "E_DEP_CYCLE",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Targets = cyclicTargets()
				cfg.Build.DefaultTargets = nil
				cfg.Package = PackageOutput{Format: "tar", Optimization: "balanced"}
				return cfg
			},
		},
		{
			name: "package format is the last section checked",
			want: "package.format",
			cfg: func() Config {
				cfg := Default("demo")
				cfg.Package = PackageOutput{Format: "tar", Optimization: "balanced"}
				return cfg
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.cfg())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

// TestValidateAcceptsDefaultConfig guards the happy path so the section helpers
// keep returning nil for a well-formed configuration.
func TestValidateAcceptsDefaultConfig(t *testing.T) {
	if err := Validate(Default("demo")); err != nil {
		t.Fatalf("Validate(Default) = %v, want nil", err)
	}
}
