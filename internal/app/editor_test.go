package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/vscode"
)

// readEditorConfig loads the generated tasks.json and fails the test when it is
// missing or malformed.
func readEditorConfig(t *testing.T, root string) vscode.Document {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".vscode", "tasks.json"))
	if err != nil {
		t.Fatalf("reading .vscode/tasks.json: %v", err)
	}
	var document vscode.Document
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("tasks.json is not valid JSON: %v\n%s", err, data)
	}
	return document
}

// findEditorTask returns the task with the given label.
func findEditorTask(t *testing.T, document vscode.Document, label string) vscode.Task {
	t.Helper()
	for _, task := range document.Tasks {
		if task.Label == label {
			return task
		}
	}
	t.Fatalf("missing task %q in %#v", label, document.Tasks)
	return vscode.Task{}
}

func TestInitGeneratesEditorConfig(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "hello"); err != nil {
		t.Fatal(err)
	}
	document := readEditorConfig(t, root)
	for _, label := range []string{"Trestle: Build", "Trestle: Build All", "Trestle: Build Current Target", "Trestle: Test", "Trestle: Release", "Trestle: Run app"} {
		findEditorTask(t, document, label)
	}
	run := findEditorTask(t, document, "Trestle: Run app")
	if run.Command != "${workspaceFolder}/build/debug/bin/hello" {
		t.Fatalf("run command = %q", run.Command)
	}
	if run.Windows == nil || run.Windows.Command != run.Command+".exe" {
		t.Fatalf("native run tasks need the Windows suffix: %#v", run.Windows)
	}
	if run.DependsOn != vscode.BuildTaskLabel {
		t.Fatalf("run tasks must depend on build: %#v", run)
	}
	if document.Version != vscode.Version {
		t.Fatalf("version = %q", document.Version)
	}
}

func TestInitLeavesAnExistingEditorConfigAlone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".vscode", "tasks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	user := []byte(`{"version": "2.0.0", "tasks": [{"label": "Mine", "type": "shell", "command": "echo"}]}`)
	if err := os.WriteFile(path, user, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(root, "hello"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(user) {
		t.Fatalf("init must not touch existing tasks.json:\n%s", data)
	}
}

func TestEditorConfigCreatesRefusesMergesAndReplaces(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "hello"); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(root, ".vscode", "tasks.json")
	if err := os.Remove(tasksPath); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, config.DefaultFileName)

	result, err := EditorConfig(path, EditorConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != vscode.ActionCreated || result.Tasks == 0 {
		t.Fatalf("result = %#v, want a created document", result)
	}

	_, err = EditorConfig(path, EditorConfigOptions{})
	if !errors.Is(err, vscode.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if !strings.Contains(err.Error(), "-merge") || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("the error must explain the options: %v", err)
	}

	document := readEditorConfig(t, root)
	document.Tasks = append(document.Tasks, vscode.Task{Label: "My Task", Type: "shell", Command: "echo user"})
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasksPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = EditorConfig(path, EditorConfigOptions{Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != vscode.ActionUpdated {
		t.Fatalf("action = %q, want updated", result.Action)
	}
	merged := readEditorConfig(t, root)
	findEditorTask(t, merged, "My Task")
	findEditorTask(t, merged, "Trestle: Build")

	result, err = EditorConfig(path, EditorConfigOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != vscode.ActionReplaced {
		t.Fatalf("action = %q, want replaced", result.Action)
	}
	replaced := readEditorConfig(t, root)
	for _, task := range replaced.Tasks {
		if task.Label == "My Task" {
			t.Fatal("force must produce a fresh document")
		}
	}
}

func TestEditorConfigRejectsConflictingOptions(t *testing.T) {
	_, err := EditorConfig(filepath.Join(t.TempDir(), config.DefaultFileName), EditorConfigOptions{Force: true, Merge: true})
	if err == nil || !strings.Contains(err.Error(), "E_EDITOR_CONFIG_FLAGS") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditorConfigRequiresAProject(t *testing.T) {
	_, err := EditorConfig(filepath.Join(t.TempDir(), config.DefaultFileName), EditorConfigOptions{})
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEditorConfigReportsWriteFailures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := config.Save(path, config.Default("hello")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".vscode"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EditorConfig(path, EditorConfigOptions{}); err == nil {
		t.Fatal("write failures must reach the caller")
	}
}

func TestEditorConfigWSLBuildsHaveNoWindowsSuffix(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("hello")
	cfg.Toolchain.Mode = "wsl"
	cfg.Toolchain.WSLDistribution = "Ubuntu"
	cfg.Toolchain.C = "gcc"
	cfg.Toolchain.CXX = "g++"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := EditorConfig(path, EditorConfigOptions{}); err != nil {
		t.Fatal(err)
	}
	run := findEditorTask(t, readEditorConfig(t, root), "Trestle: Run app")
	if run.Windows != nil {
		t.Fatalf("WSL builds must not append .exe: %#v", run.Windows)
	}
	if !strings.HasSuffix(run.Command, "/bin/hello") {
		t.Fatalf("command = %q", run.Command)
	}
}
