package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"trestle/internal/config"
)

func TestRecoveryCommandProcess(t *testing.T) {
	gate := os.Getenv("TRESTLE_TASK_GATE")
	if gate == "" {
		return
	}
	if err := os.WriteFile(gate+".ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Print("中文\n\n" + strings.Repeat("long-path/", 10000) + "TAIL")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(gate + ".release"); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("release timeout")
}

func TestRecoveryTaskProcess(t *testing.T) {
	path := os.Getenv("TRESTLE_RECOVERY_TASK")
	if path == "" {
		return
	}
	err := ExecuteTask(context.Background(), path, "prepare", nil)
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("task overwrote concurrent settings: %v", err)
	}
}

func TestSameTaskConflictAndCommitAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	gate := filepath.Join(root, "gate")
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("original")
	cfg.Tasks = map[string]config.Task{"prepare": {Command: []string{os.Args[0], "-test.run=^TestRecoveryCommandProcess$"}, Set: map[string]string{"build.profile": "release"}}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryTaskProcess$")
	cmd.Env = append(os.Environ(), "TRESTLE_RECOVERY_TASK="+path, "TRESTLE_TASK_GATE="+gate)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(gate + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := ExecuteTask(context.Background(), path, "prepare", nil); err == nil || !strings.Contains(err.Error(), "E_RESOURCE_BUSY") {
		t.Fatalf("duplicate task started: %v", err)
	}
	// This must succeed while the command is running: no configuration lock is held.
	if err := SetProjectSetting(path, "project.name", "external change"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate+".release", []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	latest, err := config.Load(path)
	if err != nil || latest.Project.Name != "external change" || latest.Build.Profile != "debug" {
		t.Fatalf("lost change: %+v %v", latest, err)
	}
	preview, err := PreviewTask(path, "prepare")
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecuteTaskPreview(context.Background(), path, "prepare", preview, nil); err != nil {
		t.Fatal(err)
	}
}
