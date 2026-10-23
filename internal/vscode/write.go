package vscode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"trestle/internal/fsx"
)

// ErrExists reports that tasks.json already exists while the caller asked not
// to touch it.
var ErrExists = errors.New("E_VSCODE_TASKS_EXISTS")

// Mode selects how Write treats an existing tasks.json.
type Mode int

const (
	// ModeCreate writes the file only when it is missing and otherwise leaves
	// it exactly as the user left it.
	ModeCreate Mode = iota
	// ModeRefuse fails when the file already exists.
	ModeRefuse
	// ModeMerge replaces the tasks and inputs trestle generates, keeping every
	// other task, input, and top-level setting.
	ModeMerge
	// ModeReplace overwrites an existing file completely.
	ModeReplace
)

// Action reports what Write did to the file.
type Action string

const (
	ActionCreated  Action = "created"
	ActionUpdated  Action = "updated"
	ActionReplaced Action = "replaced"
	ActionSkipped  Action = "skipped"
)

// WriteResult describes one Write call.
type WriteResult struct {
	Path   string
	Action Action
	Tasks  int
}

// Encode renders the document as the indented JSON VS Code expects. The
// document only carries strings, slices, and pointers, so encoding cannot fail.
func Encode(document Document) []byte {
	data, _ := json.MarshalIndent(document, "", "  ")
	return append(data, '\n')
}

// Merge folds the generated tasks and inputs into an existing tasks.json
// document. Entries trestle owns are replaced; user tasks, user inputs, and
// unknown top-level settings are preserved.
func Merge(existing []byte, document Document) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(existing, &fields); err != nil {
		return nil, fmt.Errorf("E_VSCODE_TASKS_INVALID: tasks.json is not plain JSON, so it cannot be merged: %w", err)
	}
	tasks, err := keepUserEntries(fields["tasks"], ownedTask)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, mustEncodeEntries(document.Tasks)...)
	fields["tasks"] = encodeEntries(tasks)

	inputs, err := keepUserEntries(fields["inputs"], ownedInput)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, mustEncodeEntries(document.Inputs)...)
	if len(inputs) > 0 {
		fields["inputs"] = encodeEntries(inputs)
	} else {
		delete(fields, "inputs")
	}
	fields["version"] = json.RawMessage(strconv.Quote(Version))

	// Every value is already valid JSON, so the final render cannot fail.
	data, _ := json.MarshalIndent(fields, "", "  ")
	return append(data, '\n'), nil
}

// keepUserEntries decodes a JSON array of entries and drops the entries owned
// according to owned. Missing or null arrays yield no entries.
func keepUserEntries(raw json.RawMessage, owned func(json.RawMessage) bool) ([]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("E_VSCODE_TASKS_INVALID: tasks.json is not plain JSON, so it cannot be merged: %w", err)
	}
	kept := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		if !owned(entry) {
			kept = append(kept, entry)
		}
	}
	return kept, nil
}

// ownedTask reports whether an entry is a task trestle generated.
func ownedTask(raw json.RawMessage) bool {
	var task struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		return false
	}
	return strings.HasPrefix(task.Label, taskLabelPrefix)
}

// ownedInput reports whether an entry is the input trestle generated.
func ownedInput(raw json.RawMessage) bool {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return false
	}
	return input.ID == targetInputID
}

// mustEncodeEntries marshals generated entries. The document types only carry
// strings, slices, and pointers, so encoding cannot fail.
func mustEncodeEntries[T any](values []T) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		data, _ := json.Marshal(value)
		result = append(result, data)
	}
	return result
}

// encodeEntries marshals a JSON array of already encoded entries, which are
// valid by construction.
func encodeEntries(entries []json.RawMessage) json.RawMessage {
	data, _ := json.Marshal(entries)
	return data
}

// Write emits the document to path following the requested mode. The atomic
// write keeps readers from ever observing a partial file, and Create reports a
// skipped action when an existing file must not be touched.
func Write(path string, document Document, mode Mode) (WriteResult, error) {
	existing, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return WriteResult{}, readErr
	}
	result := WriteResult{Path: path, Tasks: len(document.Tasks)}
	switch {
	case exists && mode == ModeCreate:
		result.Action = ActionSkipped
		return result, nil
	case exists && mode == ModeRefuse:
		return WriteResult{}, fmt.Errorf("%w: %s", ErrExists, path)
	}
	var data []byte
	var err error
	switch {
	case !exists:
		data = Encode(document)
		result.Action = ActionCreated
	case mode == ModeMerge:
		data, err = Merge(existing, document)
		result.Action = ActionUpdated
	default:
		data = Encode(document)
		result.Action = ActionReplaced
	}
	if err != nil {
		return WriteResult{}, err
	}
	if err := fsx.AtomicWrite(path, data); err != nil {
		return WriteResult{}, err
	}
	return result, nil
}
