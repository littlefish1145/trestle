package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"

	"trestle/internal/config"
	"trestle/internal/policy"
)

func AssessTargets(ctx context.Context, path string) (config.Config, map[string]policy.Status, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return cfg, nil, err
	}
	cfg, err = policy.Apply(ctx, cfg)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, policy.Assess(ctx, cfg), nil
}

// ExecuteTask is called only after the TUI has shown the command and received
// explicit confirmation. Commands are argv arrays, never implicit shell text.
func ExecuteTask(ctx context.Context, path, name string, progress func(string)) error {
	return executeTask(ctx, path, name, nil, progress)
}

func ExecuteTaskExpected(ctx context.Context, path, name string, expected config.Task, progress func(string)) error {
	return executeTask(ctx, path, name, &expected, progress)
}

func executeTask(ctx context.Context, path, name string, expected *config.Task, progress func(string)) error {
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	task, ok := cfg.Tasks[name]
	if !ok {
		return fmt.Errorf("unknown task %q", name)
	}
	if expected != nil && !reflect.DeepEqual(task, *expected) {
		return fmt.Errorf("task %q changed after preview; refresh and review it again", name)
	}
	for key, value := range task.Set {
		if err := applyTaskSetting(&cfg, key, value); err != nil {
			return err
		}
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if len(task.Command) > 0 {
		command := exec.CommandContext(ctx, task.Command[0], task.Command[1:]...)
		command.Dir = filepath.Dir(path)
		writer := &taskWriter{progress: progress}
		command.Stdout, command.Stderr = writer, writer
		if progress != nil {
			progress("Running: " + fmt.Sprintf("%q", task.Command))
		}
		err = command.Run()
		writer.Flush()
		if err != nil {
			return fmt.Errorf("task %q command failed: %w", name, err)
		}
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, current) {
		return fmt.Errorf("task %q did not save settings: trestle.toml changed while the command ran", name)
	}
	if len(task.Set) > 0 {
		return config.Save(path, cfg)
	}
	return nil
}

func applyTaskSetting(cfg *config.Config, key, value string) error {
	switch key {
	case "build.profile":
		if value != "debug" && value != "release" {
			return fmt.Errorf("task build.profile must be debug or release")
		}
		cfg.Build.Profile = value
	case "toolchain.c":
		cfg.Toolchain.C = value
	case "toolchain.cxx":
		cfg.Toolchain.CXX = value
	case "toolchain.mode":
		if value != "native" && value != "wsl" {
			return fmt.Errorf("task toolchain.mode must be native or wsl")
		}
		cfg.Toolchain.Mode = value
	case "toolchain.wsl_distribution":
		cfg.Toolchain.WSLDistribution = value
	case "vcpkg.root":
		cfg.Vcpkg.Root = value
	case "vcpkg.triplet":
		cfg.Vcpkg.Triplet = value
	default:
		return fmt.Errorf("task cannot set %q", key)
	}
	return nil
}

type taskWriter struct {
	mu       sync.Mutex
	pending  bytes.Buffer
	progress func(string)
}

func (w *taskWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, part := range data {
		if part == '\n' {
			if w.progress != nil {
				w.progress(w.pending.String())
			}
			w.pending.Reset()
		} else {
			w.pending.WriteByte(part)
		}
	}
	return len(data), nil
}
func (w *taskWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() > 0 && w.progress != nil {
		w.progress(w.pending.String())
	}
	w.pending.Reset()
}
