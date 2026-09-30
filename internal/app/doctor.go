package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"trestle/internal/config"
	"trestle/internal/policy"
	"trestle/internal/runlog"
)

func Doctor(path string) error {
	return DoctorWithContext(context.Background(), path, func(line string) { fmt.Println(line) })
}

func DoctorWithContext(ctx context.Context, path string, progress func(string)) error {
	return runOperation(ctx, path, "doctor", progress, func(ctx context.Context, emit func(string)) error {
		var failures []error
		check := func(label string, err error, success string) {
			if err != nil {
				emit(label + "\n  FAILED " + err.Error())
				failures = append(failures, err)
			} else {
				emit(label + "\n  OK " + success)
			}
		}
		cfg, configErr := config.Load(path)
		check("Configuration", configErr, path)
		ninja, ninjaErr := exec.LookPath("ninja")
		if ninjaErr != nil {
			ninjaErr = fmt.Errorf("E_NINJA_NOT_FOUND: install Ninja and add it to PATH: %w", ninjaErr)
		}
		check("Build runner", ninjaErr, ninja)
		if configErr == nil {
			probe, cancel := context.WithTimeout(ctx, 15*time.Second)
			tc, toolErr := detectConfigured(probe, cfg)
			cancel()
			check("Compiler and environment", toolErr, tc.CXX+"\n  Linker: "+tc.Linker+"\n  Setup: "+tc.Setup)
			if toolErr == nil {
				check("Dependency target", checkPackageTarget(cfg, tc), "triplets match compiler target")
			}
			statuses := policy.AssessAt(ctx, cfg, filepath.Dir(path))
			for _, name := range sortedTargetNames(cfg.Targets) {
				check("Target "+name, policy.CheckSelected(statuses, []string{name}), "ready")
			}
			dir := filepath.Join(cfg.Build.BuildDir, cfg.Build.Profile)
			if data, err := os.ReadFile(filepath.Join(dir, ".trestle", "incomplete")); err == nil {
				emit("Generation\n  INCOMPLETE " + strings.TrimSpace(string(data)))
			} else if _, err := os.Stat(filepath.Join(dir, "build.ninja")); err != nil {
				emit("Generation\n  run trestle build to generate build.ninja")
			} else {
				emit("Generation\n  OK build.ninja exists")
			}
		}
		records, historyErr := runlog.List(path)
		check("Operation history", historyErr, fmt.Sprintf("%d records; logs kept until manually removed", len(records)))
		for _, record := range records {
			if record.Status == "interrupted" {
				emit("Interrupted " + record.Operation + "\n  " + record.Error + "\n  Log: " + record.LogPath)
			}
		}
		return errors.Join(failures...)
	})
}

func libraryFound(libPath, name string) bool {
	for _, directory := range strings.Split(libPath, ";") {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	return false
}
