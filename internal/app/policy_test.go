package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"trestle/internal/config"
)

func TestTaskEditsOnlyAfterSuccessfulCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	cfg := config.Default("demo")
	cfg.Tasks = map[string]config.Task{
		"ok":   {Command: []string{"go", "version"}, Set: map[string]string{"build.profile": "release"}},
		"fail": {Command: []string{"trestle-command-that-does-not-exist"}, Set: map[string]string{"build.profile": "release"}},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteTaskExpected(context.Background(), path, "ok", config.Task{Command: []string{"different"}}, nil); err == nil {
		t.Fatal("changed task was executed without renewed preview")
	}
	if err := ExecuteTask(context.Background(), path, "fail", nil); err == nil {
		t.Fatal("expected command failure")
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Build.Profile != "debug" {
		t.Fatal("failed command changed config")
	}
	if err := ExecuteTask(context.Background(), path, "ok", nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Build.Profile != "release" {
		t.Fatal("successful task did not persist config")
	}
}

func TestTaskPreviewRejectsChangedProjectSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	cfg := config.Default("demo")
	cfg.Tasks = map[string]config.Task{"prepare": {Set: map[string]string{"build.profile": "release"}}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewTask(path, "prepare")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Before != "debug" || preview.Changes[0].After != "release" {
		t.Fatalf("incorrect preview: %+v", preview)
	}
	cfg.Project.Name = "changed"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteTaskPreview(context.Background(), path, "prepare", preview, nil); err == nil || !strings.Contains(err.Error(), "changed after preview") {
		t.Fatalf("stale preview was accepted: %v", err)
	}
}

func TestTaskPreviewRejectsWSLModeWithWindowsCompiler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	cfg := config.Default("demo")
	cfg.Toolchain.CXX = `C:\Tools\clang++.exe`
	cfg.Tasks = map[string]config.Task{"switch": {Set: map[string]string{"toolchain.mode": "wsl"}}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewTask(path, "switch"); err == nil || !strings.Contains(err.Error(), "Windows C++ compiler") {
		t.Fatalf("expected incompatible toolchain diagnostic, got %v", err)
	}
}

func TestTaskHelperProcess(t *testing.T) {
	switch os.Getenv("TRESTLE_TASK_TEST_HELPER") {
	case "1":
		time.Sleep(5 * time.Second)
	case "pwd":
		workingDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("cwd=" + workingDir)
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=^TestTaskHelperProcess$")
		child.Env = append(os.Environ(), "TRESTLE_TASK_TEST_HELPER=child")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Println("child spawned")
		time.Sleep(5 * time.Second)
	case "child":
		time.Sleep(time.Second)
		if err := os.WriteFile(os.Getenv("TRESTLE_TASK_CHILD_MARKER"), []byte("survived"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskCancellationStopsChildProcesses(t *testing.T) {
	t.Setenv("TRESTLE_TASK_TEST_HELPER", "spawn")
	root := t.TempDir()
	marker := filepath.Join(root, "child-survived")
	t.Setenv("TRESTLE_TASK_CHILD_MARKER", marker)
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	cfg.Tasks = map[string]config.Task{"spawn": {
		Command: []string{os.Args[0], "-test.run=^TestTaskHelperProcess$"},
		Set:     map[string]string{"build.profile": "release"},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewTask(path, "spawn")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawned := false
	err = ExecuteTaskPreview(ctx, path, "spawn", preview, func(line string) {
		if strings.Contains(line, "child spawned") {
			spawned = true
			cancel()
		}
	})
	if !spawned || !errors.Is(err, context.Canceled) {
		t.Fatalf("task did not cancel after spawning child: spawned=%v err=%v", spawned, err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("task child survived cancellation: %v", err)
	}
}

func TestTaskRunsInPreviewedDirectoryAndReportsSavedChanges(t *testing.T) {
	t.Setenv("TRESTLE_TASK_TEST_HELPER", "pwd")
	root := t.TempDir()
	workingDir := filepath.Join(root, "tools")
	if err := os.Mkdir(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "trestle.toml")
	cfg := config.Default("demo")
	cfg.Tasks = map[string]config.Task{"prepare": {
		Command:    []string{os.Args[0], "-test.run=^TestTaskHelperProcess$"},
		WorkingDir: "tools", Set: map[string]string{"build.profile": "release"},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewTask(path, "prepare")
	if err != nil {
		t.Fatal(err)
	}
	var output []string
	if err := ExecuteTaskPreview(context.Background(), path, "prepare", preview, func(line string) { output = append(output, line) }); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(output, "\n")
	if !strings.Contains(joined, "cwd="+workingDir) || !strings.Contains(joined, "Applied build.profile: debug → release") {
		t.Fatalf("task output omitted working directory or saved change: %s", joined)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Build.Profile != "release" {
		t.Fatalf("task setting was not saved: %v %+v", err, loaded.Build)
	}
}

func TestTaskTimeoutAndCancellationKeepConfigUnchanged(t *testing.T) {
	t.Setenv("TRESTLE_TASK_TEST_HELPER", "1")
	for _, tc := range []struct {
		name    string
		timeout string
		cancel  bool
		want    error
	}{
		{"timeout", "150ms", false, context.DeadlineExceeded},
		{"cancel", "", true, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			workingDir := filepath.Join(root, "tools")
			if err := os.Mkdir(workingDir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "trestle.toml")
			cfg := config.Default("demo")
			cfg.Tasks = map[string]config.Task{"prepare": {
				Command:    []string{os.Args[0], "-test.run=^TestTaskHelperProcess$"},
				WorkingDir: "tools", Timeout: tc.timeout,
				Set: map[string]string{"build.profile": "release"},
			}}
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			preview, err := PreviewTask(path, "prepare")
			if err != nil {
				t.Fatal(err)
			}
			if preview.WorkingDir != workingDir {
				t.Fatalf("working directory %q, want %q", preview.WorkingDir, workingDir)
			}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				time.AfterFunc(150*time.Millisecond, cancel)
			}
			if err := ExecuteTaskPreview(ctx, path, "prepare", preview, nil); !errors.Is(err, tc.want) {
				t.Fatalf("task error = %v, want %v", err, tc.want)
			}
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Build.Profile != "debug" {
				t.Fatal("cancelled task saved configuration")
			}
		})
	}
}
