// Package diag defines Trestle's structured error type along with the rendering
// and enrichment helpers that turn raw failures into actionable diagnostics.
package diag

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Stage names the pipeline phase an error belongs to, so diagnostics can point
// at the right subsystem.
type Stage string

// The pipeline stages an error can be attributed to.
const (
	StageConfig     Stage = "config"
	StageToolchain  Stage = "toolchain"
	StageDependency Stage = "dependency"
	StageModuleScan Stage = "module-scan"
	StageGenerate   Stage = "generate"
	StageBuild      Stage = "build"
)

// Error is a structured diagnostic. Fields are filled in as the error
// propagates, and Render prints whichever are set.
type Error struct {
	Code    string
	Stage   Stage
	Summary string
	Detail  string
	Hints   []string
	Command []string
	LogPath string
	Cause   error
}

// Error renders the code and summary as the single-line form required by the
// error interface, falling back to the cause when no summary was set.
func (e *Error) Error() string {
	message := e.Summary
	if message == "" && e.Cause != nil {
		message = e.Cause.Error()
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, message)
	}
	return message
}

// Unwrap exposes the underlying cause so errors.Is and errors.As can reach it.
func (e *Error) Unwrap() error { return e.Cause }

// New builds a structured error from a code, stage, summary and cause.
func New(code string, stage Stage, summary string, cause error) *Error {
	return &Error{Code: code, Stage: stage, Summary: summary, Cause: cause}
}

// Render writes a human-readable diagnostic block to writer. A nil error emits
// nothing, and a plain error is printed on a single line.
func Render(writer io.Writer, err error) {
	if err == nil {
		return
	}
	var structured *Error
	if !errors.As(err, &structured) {
		fmt.Fprintf(writer, "error: %v\n", err)
		return
	}
	fmt.Fprintf(writer, "error[%s]: %s\n", structured.Code, structured.Summary)
	if structured.Stage != "" {
		fmt.Fprintf(writer, "stage: %s\n", structured.Stage)
	}
	if structured.Detail != "" {
		fmt.Fprintf(writer, "detail:\n%s\n", strings.TrimRight(structured.Detail, "\n"))
	}
	if len(structured.Command) > 0 {
		fmt.Fprintf(writer, "command: %s\n", strings.Join(structured.Command, " "))
	}
	for _, hint := range structured.Hints {
		fmt.Fprintf(writer, "hint: %s\n", hint)
	}
	if structured.LogPath != "" {
		fmt.Fprintf(writer, "log: %s\n", structured.LogPath)
	}
	if structured.Cause != nil {
		fmt.Fprintf(writer, "cause: %v\n", structured.Cause)
	}
}
