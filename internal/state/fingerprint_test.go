package state

import (
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
	"trestle/internal/toolchain"
)

func TestGenerationKeyChangesWithSourceList(t *testing.T) {
	cfg := config.Config{SchemaVersion: 3, Build: config.Build{Profile: "debug"}, Project: config.Project{Name: "demo"}}
	first := BuildKey(cfg, model.ResolvedProject{Targets: []model.ResolvedTarget{{Target: model.Target{Sources: []string{"a.cpp"}}}}}, toolchain.Toolchain{Kind: toolchain.Clang}, "", "", "")
	second := BuildKey(cfg, model.ResolvedProject{Targets: []model.ResolvedTarget{{Target: model.Target{Sources: []string{"b.cpp"}}}}}, toolchain.Toolchain{Kind: toolchain.Clang}, "", "", "")
	if first.Hash() == second.Hash() {
		t.Fatal("source list did not change generation key")
	}
}
