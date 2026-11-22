//go:build windows

package cmake

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"trestle/internal/processx"
)

func workspaceCommand(ctx context.Context, exe string, env, args []string) *exec.Cmd {
	direct := processx.Command(ctx, exe, args...)
	direct.Env = env
	direct.Env = append(direct.Environ(), "VSLANG=1033")
	// CMake probes /showIncludes using the console code page, and Ninja
	// matches the resulting prefix byte-for-byte. A private UTF-8 console
	// keeps both processes consistent even on localized MSVC installs
	// without the English resource files.
	quoted := []string{syscall.EscapeArg(exe)}
	for _, arg := range args {
		quoted = append(quoted, syscall.EscapeArg(arg))
	}
	line := strings.Join(quoted, " ")
	system, err := windows.GetSystemDirectory()
	if err != nil || len(line) > 7500 {
		return direct
	}

	command := processx.Command(ctx, filepath.Join(system, "cmd.exe"))
	// Delayed expansion passes the command after cmd has parsed its own
	// operators. Cache values containing %, !, or & remain literal.
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_CONSOLE,
		HideWindow:    true,
		CmdLine:       `cmd.exe /D /V:ON /S /C "!TRESTLE_CMAKE_CHCP! 65001 >NUL && !TRESTLE_CMAKE_COMMAND!"`,
	}
	command.Env = append(direct.Env,
		"TRESTLE_CMAKE_CHCP="+syscall.EscapeArg(filepath.Join(system, "chcp.com")),
		"TRESTLE_CMAKE_COMMAND="+line,
	)
	return command
}
