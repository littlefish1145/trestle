package config

import (
	"strings"
	"testing"
)

func TestShaderConfigurationRejectsAmbiguousSourcesAndInvalidStage(t *testing.T) {
	for _, c := range []struct {
		stage   string
		sources []string
		want    string
	}{{"", []string{"shader.vert"}, "shader_stage is required"}, {"invalid", []string{"shader.vert"}, "unsupported shader_stage"}, {"vert", []string{"a.vert", "b.vert"}, "exactly one source pattern"}} {
		cfg := Default("shader")
		cfg.Targets = map[string]Target{"app": {Type: "shader", ShaderStage: c.stage, Sources: c.sources}}
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	for _, stage := range []string{"vert", "frag", "comp", "geom", "tesc", "tese", "task", "mesh", "rgen", "rint", "rahit", "rchit", "rmiss", "rcall"} {
		cfg := Default("shader")
		cfg.Targets = map[string]Target{"app": {Type: "shader", ShaderStage: stage, Sources: []string{"shader.glsl"}}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("valid stage %s: %v", stage, err)
		}
	}
}
