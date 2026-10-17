package diag

import (
	"bytes"
	"errors"
	"strings"
)

// Text renders an error to a string with no trailing newline, for callers that
// manage their own spacing.
func Text(err error) string {
	var buffer bytes.Buffer
	Render(&buffer, err)
	return strings.TrimRight(buffer.String(), "\n")
}

// Enrich keeps existing structured errors and makes legacy diagnostics actionable.
func Enrich(err error, stage Stage) *Error {
	if err == nil {
		return nil
	}
	if existing, ok := structuredError(err); ok {
		return existing
	}
	message := err.Error()
	code := errorCode(message)
	e := New(code, stage, message, err)
	e.Summary = strings.TrimPrefix(message, code+": ")
	enrichHints(e, code, stage)
	return e
}

// structuredError returns a copy of the structured *Error already carried by
// err, so Enrich never aliases a caller's error value.
func structuredError(err error) (*Error, bool) {
	var existing *Error
	if errors.As(err, &existing) {
		copy := *existing
		return &copy, true
	}
	return nil, false
}

// errorCode reads the E_ code prefixed onto a legacy error message, falling
// back to a generic code when the message carries none.
func errorCode(message string) string {
	code := "E_OPERATION_FAILED"
	if start := strings.Index(message, "E_"); start >= 0 {
		end := strings.IndexAny(message[start:], ": \n")
		if end > 0 {
			code = message[start : start+end]
		}
	}
	return code
}

// enrichHints attributes the diagnostic stage and a matching hint set from the
// error code so legacy failures point callers at the right fix.
func enrichHints(e *Error, code string, stage Stage) {
	switch {
	case strings.Contains(code, "CONFIG") || stage == StageConfig:
		e.Stage = StageConfig
		e.Hints = []string{"Inspect trestle.toml at the reported field. Restore its schema backup if needed, then refresh the project. Newer schemas require a compatible Trestle version."}
	case strings.Contains(code, "WSL"):
		e.Stage = StageToolchain
		e.Hints = []string{"Run wsl --list --verbose and verify the selected distribution. Check the configured compiler inside that distribution and that the reported path is accessible there."}
	case strings.Contains(code, "TOOLCHAIN") || strings.Contains(code, "MSVC"):
		e.Stage = StageToolchain
		e.Hints = []string{"Run trestle toolchain, then trestle configure -toolchain <compiler-path>. For MSVC ABI compilers, install Visual Studio C++ tools and configure -setup <vcvars64.bat>."}
	case strings.Contains(code, "PACKAGE") || strings.Contains(code, "VCPKG"):
		e.Stage = StageDependency
		e.Hints = []string{"Check [vcpkg].root and the package triplet against the target OS/architecture. Install the declared port for that triplet; unsupported metadata needs an explicit package override."}
	case strings.Contains(code, "NINJA_NOT_FOUND"):
		e.Hints = []string{"Install Ninja and place it on PATH, then run trestle doctor."}
	case strings.Contains(code, "TARGET_UNAVAILABLE"):
		e.Hints = []string{"Review the target's readiness reasons in Doctor or the TUI, fix missing tools/packages, and retry. --force only bypasses availability checks."}
	default:
		e.Hints = []string{"Inspect the complete operation log, fix the first reported error, then retry the same command. Tasks require a fresh preview; files changed by task commands are not rolled back."}
	}
}
