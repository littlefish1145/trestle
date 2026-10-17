package conan

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"trestle/internal/processx"
)

// installOutput collects the tail of a conan install run and forwards each
// decoded line to the progress callback. The stdout and stderr readers share one
// instance, so its lock serialises both line capture and callback delivery.
type installOutput struct {
	lock       sync.Mutex
	lines      []string
	progress   func(string)
	readErrors chan error
}

// record appends a decoded line, keeping only the most recent 128, and forwards
// it to the progress callback while holding the lock.
func (output *installOutput) record(line string) {
	output.lock.Lock()
	defer output.lock.Unlock()
	output.lines = append(output.lines, line)
	if len(output.lines) > 128 {
		output.lines = output.lines[len(output.lines)-128:]
	}
	if output.progress != nil {
		output.progress(line)
	}
}

// detail joins the buffered lines for an error summary.
func (output *installOutput) detail() string {
	output.lock.Lock()
	defer output.lock.Unlock()
	return strings.Join(output.lines, "\n")
}

// read consumes reader line by line until it is drained, recording each decoded
// line and reporting any non-EOF read error to the shared error channel.
func (output *installOutput) read(reader *bufio.Reader) {
	for {
		text, readErr := reader.ReadString('\n')
		line := strings.TrimRight(processx.DecodeOutput([]byte(text)), "\r\n")
		if text == "" {
			if readErr != io.EOF && readErr != nil {
				output.readErrors <- readErr
			}
			return
		}
		output.record(line)
		if readErr != nil {
			if readErr != io.EOF {
				output.readErrors <- readErr
			}
			return
		}
	}
}

// startInstall builds and starts the conan install command, returning the
// running process together with its stdout and stderr pipes.
func (client Client) startInstall(ctx context.Context, reference string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	command := processx.Command(ctx, client.Executable, client.installArgs(reference)...)
	command.Env = client.environment()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := command.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("E_CONAN_INSTALL: %w", err)
	}
	return command, stdout, stderr, nil
}

// installReaders returns the readers the install output is consumed from,
// tee-ing both streams to the process-wide raw output sink and swapping in a
// ctx-aware progress notifier when the caller requested raw capture.
func installReaders(ctx context.Context, stdout, stderr io.Reader, progress func(string)) (io.Reader, io.Reader, func(string)) {
	var outReader, errReader io.Reader = stdout, stderr
	if raw := processx.Output(ctx); raw != nil {
		tee := processx.Sync(raw)
		outReader, errReader = io.TeeReader(stdout, tee), io.TeeReader(stderr, tee)
		progress = processx.Notify(ctx, progress)
	}
	return outReader, errReader, progress
}
