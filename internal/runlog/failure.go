package runlog

import (
	"errors"
	"strings"
	"trestle/internal/diag"
)

// Failure records diagnostics from operations that fail before a process starts.
func Failure(path, operation string, err error) error {
	if err == nil {
		return nil
	}
	var structured *diag.Error
	if errors.As(err, &structured) && structured.LogPath != "" {
		return err
	}
	run, startErr := Start(path, operation)
	if startErr != nil {
		return errors.Join(LogError(path, startErr), err)
	}
	stage := diag.StageBuild
	if operation == "configure" || strings.Contains(operation, "setting") {
		stage = diag.StageConfig
	}
	result := diag.Enrich(err, stage)
	result.LogPath = run.Record.LogPath
	_, writeErr := run.Write([]byte(diag.Text(result) + "\n"))
	finishErr := run.Finish(result)
	return errors.Join(result, writeErr, finishErr)
}
