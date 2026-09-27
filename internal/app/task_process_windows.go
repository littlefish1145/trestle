//go:build windows

package app

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func prepareTaskProcess(_ *exec.Cmd) {}

func terminateTaskProcess(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	_ = command.Process.Kill()
}
