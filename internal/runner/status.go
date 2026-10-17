package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"trestle/internal/processx"
)

// Progress is a snapshot of a running build: how many edges have finished out
// of the total, the percentage complete, and how many are running.
type Progress struct {
	Finished int
	Total    int
	Percent  int
	Running  int
}

// EventSink receives build progress and output as they happen, letting a caller
// render live status instead of waiting for the build to finish.
type EventSink interface {
	Progress(Progress)
	Output(stream, text string)
}

// StatusSink is an EventSink that writes each event to a writer in a plain
// line-oriented form.
type StatusSink struct {
	Out io.Writer
}

// Progress writes the build progress as a single status line.
func (sink StatusSink) Progress(value Progress) {
	if sink.Out == nil {
		return
	}
	fmt.Fprintf(sink.Out, "progress: %d/%d %d%% running=%d\n", value.Finished, value.Total, value.Percent, value.Running)
}

// Output writes a line of build output tagged with the stream it came from.
func (sink StatusSink) Output(stream, text string) {
	if sink.Out != nil && text != "" {
		fmt.Fprintf(sink.Out, "%s: %s", stream, text)
	}
}

// RunWithEvents runs executable with args and streams Ninja's progress to sink,
// decoding the NINJA_STATUS lines Trestle injects. Output not recognised as
// progress is forwarded to the sink, or to options.Stdout when no sink is set.
func (runner Runner) RunWithEvents(ctx context.Context, executable string, args []string, options Options, sink EventSink) error {
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("E_NINJA_NOT_FOUND: %q is required but was not found: %w", executable, err)
	}
	command, source, err := startStatusCommand(ctx, path, args, options)
	if err != nil {
		return err
	}
	if err := forwardStatus(command, source, options, sink); err != nil {
		return err
	}
	return waitForCommand(ctx, command)
}

// startStatusCommand launches the build command with the NINJA_STATUS marker
// injected into its environment and returns a reader over its standard output.
// Raw output is teed so the caller still sees the unparsed stream.
func startStatusCommand(ctx context.Context, path string, args []string, options Options) (*exec.Cmd, io.Reader, error) {
	command := processx.Command(ctx, path, args...)
	command.Dir = options.Dir
	environment := append(os.Environ(), "NINJA_STATUS=@@TRESTLE:%f:%t:%p:%r@@")
	command.Env = append(environment, options.Env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	command.Stderr = options.Stderr
	if options.RawOutput != nil {
		if command.Stderr == nil {
			command.Stderr = options.RawOutput
		} else {
			command.Stderr = io.MultiWriter(options.RawOutput, command.Stderr)
		}
	}
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	var source io.Reader = stdout
	if options.RawOutput != nil {
		source = io.TeeReader(stdout, options.RawOutput)
	}
	return command, source, nil
}

// forwardStatus reads the command output line by line, reporting recognised
// NINJA_STATUS lines as progress and forwarding the rest to the sink. A read
// failure waits for the command first so the process is still reaped.
func forwardStatus(command *exec.Cmd, source io.Reader, options Options, sink EventSink) error {
	reader := bufio.NewReader(source)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			_ = command.Wait()
			return readErr
		}
		if line == "" && readErr == io.EOF {
			break
		}
		if emitStatusLine(line, options, sink) {
			continue
		}
		if readErr == io.EOF {
			break
		}
	}
	return nil
}

// emitStatusLine decodes one output line: a NINJA_STATUS line becomes progress
// and any detail on sink, while any other text is written to sink or, when no
// sink is set, to options.Stdout. It reports whether the line carried progress.
func emitStatusLine(line string, options Options, sink EventSink) bool {
	text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if progress, ok := ParseStatus(text); ok {
		if sink != nil {
			sink.Progress(progress)
			if detail := StatusDetail(text); detail != "" {
				sink.Output("stdout", detail+"\n")
			}
		}
		return true
	}
	if sink != nil {
		sink.Output("stdout", line)
	} else if options.Stdout != nil {
		_, _ = io.WriteString(options.Stdout, line)
	}
	return false
}

// waitForCommand waits for the command to finish, preferring a cancelled context
// over the process exit and wrapping a non-zero exit as a build failure.
func waitForCommand(ctx context.Context, command *exec.Cmd) error {
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("E_NINJA_FAILED: %w", waitErr)
	}
	return nil
}

// StatusDetail strips the NINJA_STATUS marker from a line and returns the human
// readable remainder, such as the name of the file being built.
func StatusDetail(line string) string {
	start := strings.Index(line, "@@TRESTLE:")
	if start < 0 {
		return strings.TrimSpace(line)
	}
	payloadStart := start + len("@@TRESTLE:")
	end := strings.Index(line[payloadStart:], "@@")
	if end < 0 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(line[payloadStart+end+2:])
}

// ParseStatus extracts progress from a NINJA_STATUS line, reporting false when
// the line carries no marker or a malformed payload.
func ParseStatus(line string) (Progress, bool) {
	start := strings.Index(line, "@@TRESTLE:")
	if start < 0 {
		return Progress{}, false
	}
	payloadStart := start + len("@@TRESTLE:")
	end := strings.Index(line[payloadStart:], "@@")
	if end < 0 {
		return Progress{}, false
	}
	fields := strings.Split(line[payloadStart:payloadStart+end], ":")
	if len(fields) != 4 {
		return Progress{}, false
	}
	values := make([]int, 4)
	for i, field := range fields {
		field = strings.TrimSpace(strings.TrimSuffix(field, "%"))
		value, err := strconv.Atoi(field)
		if err != nil {
			return Progress{}, false
		}
		values[i] = value
	}
	return Progress{Finished: values[0], Total: values[1], Percent: values[2], Running: values[3]}, true
}
