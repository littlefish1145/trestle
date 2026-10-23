package vscode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeRendersIndentedJSONWithTrailingNewline(t *testing.T) {
	data := Encode(Document{Version: "2.0.0", Tasks: []Task{}})
	want := "{\n  \"version\": \"2.0.0\",\n  \"tasks\": []\n}\n"
	if string(data) != want {
		t.Fatalf("Encode = %q, want %q", data, want)
	}
}

func TestMergeKeepsUserEntriesAndReplacesGenerated(t *testing.T) {
	existing := []byte(`{
  "version": "2.0.0",
  "tasks": [
    {"label": "Trestle: Build", "type": "shell", "command": "old-trestle", "args": ["build"]},
    {"label": "My Task", "type": "shell", "command": "echo user"}
  ],
  "inputs": [
    {"id": "trestleTarget", "type": "pickString", "options": ["stale"]},
    {"id": "userInput", "type": "promptString"}
  ],
  "custom": {"keep": true}
}`)
	document := Generate(Project{
		BinDir:  "build/debug/bin",
		Targets: []Target{{Name: "app", OutputName: "app", Executable: true}},
		Native:  true,
	})
	merged, err := Merge(existing, document)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Version string `json:"version"`
		Custom  struct {
			Keep bool `json:"keep"`
		} `json:"custom"`
		Tasks []struct {
			Label   string   `json:"label"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"tasks"`
		Inputs []struct {
			ID      string   `json:"id"`
			Options []string `json:"options"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(merged, &fields); err != nil {
		t.Fatalf("merged document is not valid JSON: %v\n%s", err, merged)
	}
	if fields.Version != Version || !fields.Custom.Keep {
		t.Fatalf("top-level settings must survive: %#v", fields)
	}
	wantTasks := []string{"My Task", "Trestle: Build", "Trestle: Build All", "Trestle: Build Current Target", "Trestle: Run app", "Trestle: Test", "Trestle: Release"}
	for i, label := range wantTasks {
		if fields.Tasks[i].Label != label {
			t.Fatalf("task %d = %q, want %q", i, fields.Tasks[i].Label, label)
		}
	}
	for _, task := range fields.Tasks {
		if task.Command == "old-trestle" {
			t.Fatalf("stale generated task survived the merge: %#v", task)
		}
	}
	if len(fields.Inputs) != 2 || fields.Inputs[0].ID != "userInput" || fields.Inputs[1].ID != "trestleTarget" {
		t.Fatalf("inputs must keep user entries and refresh ours: %#v", fields.Inputs)
	}
	if strings.Join(fields.Inputs[0].Options, ",") != "" {
		t.Fatalf("user input changed: %#v", fields.Inputs[0])
	}
	if strings.Join(fields.Inputs[1].Options, ",") != "app" {
		t.Fatalf("generated input must be refreshed: %#v", fields.Inputs[1])
	}
}

func TestMergeRejectsJSONC(t *testing.T) {
	existing := []byte("{\n  // a user comment\n  \"version\": \"2.0.0\",\n  \"tasks\": []\n}\n")
	_, err := Merge(existing, Generate(Project{}))
	if err == nil || !strings.Contains(err.Error(), "E_VSCODE_TASKS_INVALID") {
		t.Fatalf("JSONC must be rejected with a merge error, got %v", err)
	}
}

func TestMergeRejectsMalformedTasksField(t *testing.T) {
	_, err := Merge([]byte(`{"tasks": {"label": "x"}}`), Generate(Project{}))
	if err == nil || !strings.Contains(err.Error(), "E_VSCODE_TASKS_INVALID") {
		t.Fatalf("a non-array tasks field must be rejected, got %v", err)
	}
}

func TestMergeRejectsMalformedInputsField(t *testing.T) {
	_, err := Merge([]byte(`{"tasks": [], "inputs": {"id": "x"}}`), Generate(Project{}))
	if err == nil || !strings.Contains(err.Error(), "E_VSCODE_TASKS_INVALID") {
		t.Fatalf("a non-array inputs field must be rejected, got %v", err)
	}
}

func TestMergeHandlesMissingAndNullCollections(t *testing.T) {
	merged, err := Merge([]byte(`{"tasks": null, "inputs": null}`), Generate(Project{}))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(merged, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["inputs"]; ok {
		t.Fatalf("a targetless project must not keep an inputs array: %s", merged)
	}
	var tasks []json.RawMessage
	if err := json.Unmarshal(fields["tasks"], &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("tasks = %d, want the four targetless tasks", len(tasks))
	}
}

func TestMergeKeepsEntriesItCannotInspect(t *testing.T) {
	merged, err := Merge([]byte(`{"tasks": [42, {"label": "User"}], "inputs": [42]}`), Generate(Project{}))
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Tasks  []json.RawMessage `json:"tasks"`
		Inputs []json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal(merged, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields.Tasks) != 6 || string(fields.Tasks[0]) != "42" || len(fields.Inputs) != 1 {
		t.Fatalf("entries without a label or id must survive untouched: %s", merged)
	}
}

func TestWriteCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".vscode", "tasks.json")
	result, err := Write(path, Generate(Project{BinDir: "build/debug/bin", Targets: []Target{{Name: "app", OutputName: "app", Executable: true}}, Native: true}), ModeRefuse)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionCreated || result.Tasks != 6 || result.Path != path {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") || !strings.Contains(string(data), "\"Trestle: Run app\"") {
		t.Fatalf("unexpected file contents:\n%s", data)
	}
	var document Document
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != Version {
		t.Fatalf("version = %q", document.Version)
	}
}

func TestWriteCreateSkipsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Write(path, Generate(Project{}), ModeCreate)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionSkipped {
		t.Fatalf("action = %q, want skipped", result.Action)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user content" {
		t.Fatalf("Create must not touch existing files: %s", data)
	}
}

func TestWriteRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Write(path, Generate(Project{}), ModeRefuse)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("the error must name the file: %v", err)
	}
}

func TestWriteMergesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte(`{"tasks": [{"label": "User Task", "type": "shell", "command": "echo"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Write(path, Generate(Project{}), ModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionUpdated {
		t.Fatalf("action = %q, want updated", result.Action)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "User Task") || !strings.Contains(string(data), "Trestle: Build") {
		t.Fatalf("merge lost entries:\n%s", data)
	}
}

func TestWriteMergePropagatesMergeErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte(`{"tasks": 12}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Write(path, Generate(Project{}), ModeMerge)
	if err == nil || !strings.Contains(err.Error(), "E_VSCODE_TASKS_INVALID") {
		t.Fatalf("merge errors must reach the caller, got %v", err)
	}
}

func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := Generate(Project{})
	result, err := Write(path, document, ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionReplaced {
		t.Fatalf("action = %q, want replaced", result.Action)
	}
	want := Encode(document)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("replaced file differs from the encoded document:\n%s", data)
	}
}

func TestWritePropagatesReadErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, Generate(Project{}), ModeReplace); err == nil {
		t.Fatal("reading a directory must surface the platform error")
	}
}

func TestWriteReportsDirectoryCreationErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".vscode"), []byte("a regular file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(filepath.Join(dir, ".vscode", "tasks.json"), Generate(Project{}), ModeReplace); err == nil {
		t.Fatal("writing below a regular file must fail")
	}
}
