//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"trestle/internal/config"
	"trestle/internal/toolchain"
)

func TestInitPinsCompilerWithAmbiguousPATH(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"clang", "clang++", "gcc", "g++"} {
		// Model macOS, where g++ is also an Apple Clang driver. Detection
		// still sees two executable names and explicit auto stays ambiguous.
		script := "#!/bin/sh\ncase \"$1\" in\n-dumpmachine) echo arm64-apple-darwin ;;\n*) echo 'Apple clang version 21.0.0' ;;\nesac\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	if _, err := toolchain.NewDetector().Detect(t.Context(), "auto"); err == nil {
		t.Fatal("fixture must reproduce ambiguous auto detection")
	}
	for _, workspace := range []bool{false, true} {
		root := t.TempDir()
		choices := initChoices{Workspace: workspace}
		cfg, examples, err := buildInitConfig(root, "demo", choices, os.Stdout)
		if err != nil || !examples {
			t.Fatalf("scaffold: examples=%t, error=%v", examples, err)
		}
		if cfg.Toolchain.CXX != "clang++" || cfg.Toolchain.C != "clang" {
			t.Fatalf("init kept ambiguous or machine-local selectors: %+v", cfg.Toolchain)
		}
		if _, err := toolchain.NewDetector().Detect(t.Context(), cfg.Toolchain.CXX); err != nil {
			t.Fatalf("generated selector cannot be resolved: %v", err)
		}
		path := filepath.Join(root, config.DefaultFileName)
		if err := config.Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		if workspace {
			if err := initWorkspaceMember(root, StarterMember, choices, os.Stdout); err != nil {
				t.Fatal(err)
			}
			wc, err := config.ParseWorkspace(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg = *wc.Member(StarterMember)
		} else {
			cfg, err = config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
		}
		if cfg.Toolchain.CXX != "clang++" || cfg.Toolchain.C != "clang" {
			t.Fatalf("saved/inherited selectors changed: %+v", cfg.Toolchain)
		}
	}
}

func TestInitWithoutCompilerKeepsPortableDefaults(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg, _, err := buildInitConfig(t.TempDir(), "demo", initChoices{}, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.CXX != "auto" || cfg.Toolchain.C != "auto" {
		t.Fatalf("missing compiler produced unusable selectors: %+v", cfg.Toolchain)
	}
}
