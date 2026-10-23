package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/vscode"
)

// captureStdout runs action with standard output redirected and returns what it
// printed along with the action's error.
func captureStdout(t *testing.T, action func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	actionErr := action()
	os.Stdout = original
	writer.Close()
	data, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), actionErr
}

// taskLabels extracts the labels of the tasks.json written below dir.
func taskLabels(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".vscode", "tasks.json"))
	if err != nil {
		t.Fatalf("reading .vscode/tasks.json: %v", err)
	}
	var document struct {
		Version string `json:"version"`
		Tasks   []struct {
			Label string `json:"label"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("tasks.json is not valid JSON: %v\n%s", err, data)
	}
	if document.Version != vscode.Version {
		t.Fatalf("version = %q, want %q", document.Version, vscode.Version)
	}
	labels := make([]string, 0, len(document.Tasks))
	for _, task := range document.Tasks {
		labels = append(labels, task.Label)
	}
	return labels
}

func TestRunInitGeneratesEditorConfig(t *testing.T) {
	dir := t.TempDir()
	if err := run("init", []string{"-C", dir, "-name", "demo"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(taskLabels(t, dir), ",")
	for _, want := range []string{"Trestle: Build,", "Trestle: Test,", "Trestle: Run app"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestRunEditorConfigCreatesWithoutDebugHint(t *testing.T) {
	dir := t.TempDir()
	if err := run("init", []string{"-C", dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".vscode", "tasks.json")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	output, err := captureStdout(t, func() error {
		return run("editor-config", []string{"-C", dir})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Generated") || !strings.Contains(output, "tasks.json") {
		t.Fatalf("unexpected output: %q", output)
	}
	if strings.Contains(output, "preLaunchTask") {
		t.Fatalf("without a launch.json the hint must stay silent: %q", output)
	}
}

func TestRunEditorConfigMergesAndSuggestsDebugging(t *testing.T) {
	dir := t.TempDir()
	if err := run("init", []string{"-C", dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vscode", "launch.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	output, err := captureStdout(t, func() error {
		return run("editor-config", []string{"-C", dir, "-merge"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Updated") {
		t.Fatalf("unexpected output: %q", output)
	}
	if !strings.Contains(output, `"preLaunchTask": "Trestle: Build"`) {
		t.Fatalf("missing debug hint: %q", output)
	}
}

func TestRunEditorConfigRefusesExistingAndForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := run("init", []string{"-C", dir}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	_, err := captureStdout(t, func() error {
		return run("editor-config", []string{"-C", dir})
	})
	if !errors.Is(err, vscode.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if !strings.Contains(err.Error(), "-merge") {
		t.Fatalf("the error must explain the options: %v", err)
	}
	output, err := captureStdout(t, func() error {
		return run("editor-config", []string{"-C", dir, "-force"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Overwrote") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunEditorConfigRejectsForceWithMerge(t *testing.T) {
	dir := t.TempDir()
	if err := run("init", []string{"-C", dir}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	_, err := captureStdout(t, func() error {
		return run("editor-config", []string{"-C", dir, "-force", "-merge"})
	})
	if err == nil || !strings.Contains(err.Error(), "E_EDITOR_CONFIG_FLAGS") {
		t.Fatalf("err = %v", err)
	}
}
