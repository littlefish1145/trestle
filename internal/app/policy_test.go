package app

import (
	"context"
	"path/filepath"
	"testing"
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
