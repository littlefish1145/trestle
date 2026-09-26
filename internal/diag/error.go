package diag

import (
	"fmt"
	"io"
	"strings"
)

type Stage string

const (
	StageConfig     Stage = "config"
	StageToolchain  Stage = "toolchain"
	StageDependency Stage = "dependency"
	StageModuleScan Stage = "module-scan"
	StageGenerate   Stage = "generate"
	StageBuild      Stage = "build"
)

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

func (e *Error) Unwrap() error { return e.Cause }

func New(code string, stage Stage, summary string, cause error) *Error {
	return &Error{Code: code, Stage: stage, Summary: summary, Cause: cause}
}

func Render(writer io.Writer, err error) {
	if err == nil {
		return
	}
	structured, ok := err.(*Error)
	if !ok {
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
	if structured.Cause != nil && structured.Summary == "" {
		fmt.Fprintf(writer, "cause: %v\n", structured.Cause)
	}
}
