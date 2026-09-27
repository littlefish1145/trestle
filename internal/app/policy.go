package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"trestle/internal/config"
	"trestle/internal/policy"
)

func AssessTargets(ctx context.Context, path string) (config.Config, map[string]policy.Status, error) {
	cfg, statuses, _, err := AssessTargetsWithReport(ctx, path)
	return cfg, statuses, err
}

func AssessTargetsWithReport(ctx context.Context, path string) (config.Config, map[string]policy.Status, policy.Report, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return cfg, nil, policy.Report{}, err
	}
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return cfg, nil, policy.Report{}, err
	}
	cfg, report, err := policy.ApplyWithReportAt(ctx, cfg, root)
	if err != nil {
		return cfg, nil, report, err
	}
	statuses, selected, selectionErr := policy.AssessWithToolchainAt(ctx, cfg, root)
	report.Toolchain = selected
	if selectionErr != nil {
		report.ToolchainError = selectionErr.Error()
	}
	return cfg, statuses, report, nil
}

// ExecuteTask runs a named argv task. The TUI uses PreviewTask and
// ExecuteTaskPreview so the confirmed command and settings are pinned.
func ExecuteTask(ctx context.Context, path, name string, progress func(string)) error {
	preview, err := PreviewTask(path, name)
	if err != nil {
		return err
	}
	return ExecuteTaskPreview(ctx, path, name, preview, progress)
}

func ExecuteTaskExpected(ctx context.Context, path, name string, expected config.Task, progress func(string)) error {
	preview, err := PreviewTask(path, name)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(preview.Task, expected) {
		return fmt.Errorf("task %q changed after preview; refresh and review it again", name)
	}
	return ExecuteTaskPreview(ctx, path, name, preview, progress)
}

func PreviewTask(path, name string) (config.TaskPreview, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.TaskPreview{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.TaskPreview{}, err
	}
	task, ok := cfg.Tasks[name]
	if !ok {
		return config.TaskPreview{}, fmt.Errorf("unknown task %q", name)
	}
	directory, err := taskDirectory(path, task.WorkingDir)
	if err != nil {
		return config.TaskPreview{}, err
	}
	keys := make([]string, 0, len(task.Set))
	for key := range task.Set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]config.TaskSettingChange, 0, len(keys))
	for _, key := range keys {
		value := task.Set[key]
		before := taskSettingValue(cfg, key)
		if err := applyTaskSettingAt(&cfg, path, key, value); err != nil {
			return config.TaskPreview{}, err
		}
		after := taskSettingValue(cfg, key)
		if before != after {
			changes = append(changes, config.TaskSettingChange{Field: key, Before: before, After: after})
		}
	}
	if err := validateTaskResult(cfg); err != nil {
		return config.TaskPreview{}, err
	}
	return config.TaskPreview{Task: task, WorkingDir: directory, Timeout: task.Timeout, Changes: changes, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

func taskDirectory(configPath, workingDir string) (string, error) {
	root, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return "", err
	}
	directory := root
	if workingDir != "" {
		if filepath.IsAbs(workingDir) {
			directory = filepath.Clean(workingDir)
		} else {
			directory = filepath.Join(root, workingDir)
		}
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("task working directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("task working directory %q is not a directory", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("task working directory %q: %w", directory, err)
	}
	return filepath.Clean(resolved), nil
}

func taskSettingValue(cfg config.Config, key string) string {
	switch key {
	case "build.profile":
		return cfg.Build.Profile
	case "toolchain.c":
		return cfg.Toolchain.C
	case "toolchain.cxx":
		return cfg.Toolchain.CXX
	case "toolchain.mode":
		return cfg.Toolchain.Mode
	case "toolchain.wsl_distribution":
		return cfg.Toolchain.WSLDistribution
	case "vcpkg.root":
		return cfg.Vcpkg.Root
	case "vcpkg.triplet":
		return cfg.Vcpkg.Triplet
	}
	return ""
}

func ExecuteTaskPreview(ctx context.Context, path, name string, preview config.TaskPreview, progress func(string)) error {
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(before)) != preview.Fingerprint {
		return fmt.Errorf("task %q or project settings changed after preview; refresh and review it again", name)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	task, ok := cfg.Tasks[name]
	if !ok || !reflect.DeepEqual(task, preview.Task) {
		return fmt.Errorf("task %q changed after preview; refresh and review it again", name)
	}
	directory, err := taskDirectory(path, task.WorkingDir)
	if err != nil {
		return err
	}
	if directory != preview.WorkingDir {
		return fmt.Errorf("task %q working directory changed after preview", name)
	}
	for key, value := range task.Set {
		if err := applyTaskSettingAt(&cfg, path, key, value); err != nil {
			return err
		}
	}
	if err := validateTaskResult(cfg); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("task %q cancelled before execution: %w", name, err)
	}
	if len(task.Command) > 0 {
		commandCtx := ctx
		if task.Timeout != "" {
			duration, parseErr := time.ParseDuration(task.Timeout)
			if parseErr != nil {
				return parseErr
			}
			var cancel context.CancelFunc
			commandCtx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
		command := exec.Command(task.Command[0], task.Command[1:]...)
		command.Dir = directory
		command.WaitDelay = 2 * time.Second
		prepareTaskProcess(command)
		writer := &taskWriter{progress: progress}
		command.Stdout, command.Stderr = writer, writer
		if progress != nil {
			progress("Running in " + directory + ": " + fmt.Sprintf("%q", task.Command))
		}
		if err := commandCtx.Err(); err != nil {
			if err == context.DeadlineExceeded {
				return fmt.Errorf("task %q timed out before execution: %w", name, err)
			}
			return fmt.Errorf("task %q cancelled before execution: %w", name, err)
		}
		var stoppedByContext error
		if err = command.Start(); err == nil {
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err = <-done:
			case <-commandCtx.Done():
				terminateTaskProcess(command)
				<-done
				stoppedByContext = commandCtx.Err()
			}
		}
		writer.Flush()
		if stoppedByContext == context.DeadlineExceeded {
			return fmt.Errorf("task %q timed out after %s: %w", name, task.Timeout, context.DeadlineExceeded)
		}
		if stoppedByContext == context.Canceled {
			return fmt.Errorf("task %q cancelled: %w", name, context.Canceled)
		}
		if err != nil {
			return fmt.Errorf("task %q command failed: %w", name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("task %q cancelled before saving settings: %w", name, err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, current) {
		return fmt.Errorf("task %q did not save settings: trestle.toml changed while the command ran", name)
	}
	if len(task.Set) > 0 {
		if err := config.Save(path, cfg); err != nil {
			return err
		}
		if progress != nil {
			for _, change := range preview.Changes {
				progress("Applied " + change.Field + ": " + change.Before + " → " + change.After)
			}
		}
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

func applyTaskSettingAt(cfg *config.Config, path, key, value string) error {
	if key == "vcpkg.root" && value != "" && !filepath.IsAbs(value) {
		root, err := filepath.Abs(filepath.Dir(path))
		if err != nil {
			return err
		}
		value = filepath.Join(root, value)
	}
	return applyTaskSetting(cfg, key, value)
}

func validateTaskResult(cfg config.Config) error {
	if cfg.Toolchain.Mode == "wsl" && (filepath.VolumeName(cfg.Toolchain.CXX) != "" || strings.Contains(cfg.Toolchain.CXX, `\`)) {
		return fmt.Errorf("task cannot select WSL mode with Windows C++ compiler %q", cfg.Toolchain.CXX)
	}
	return config.Validate(cfg)
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
