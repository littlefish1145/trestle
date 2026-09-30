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

type Progress struct {
	Finished int
	Total    int
	Percent  int
	Running  int
}

type EventSink interface {
	Progress(Progress)
	Output(stream, text string)
}

type StatusSink struct {
	Out io.Writer
}

func (sink StatusSink) Progress(value Progress) {
	if sink.Out == nil {
		return
	}
	fmt.Fprintf(sink.Out, "progress: %d/%d %d%% running=%d\n", value.Finished, value.Total, value.Percent, value.Running)
}

func (sink StatusSink) Output(stream, text string) {
	if sink.Out != nil && text != "" {
		fmt.Fprintf(sink.Out, "%s: %s", stream, text)
	}
}

func (runner Runner) RunWithEvents(ctx context.Context, executable string, args []string, options Options, sink EventSink) error {
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("E_NINJA_NOT_FOUND: %q is required but was not found: %w", executable, err)
	}
	command := processx.Command(ctx, path, args...)
	command.Dir = options.Dir
	environment := append(os.Environ(), "NINJA_STATUS=@@TRESTLE:%f:%t:%p:%r@@")
	command.Env = append(environment, options.Env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
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
		return err
	}
	var source io.Reader = stdout
	if options.RawOutput != nil {
		source = io.TeeReader(stdout, options.RawOutput)
	}
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
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if progress, ok := ParseStatus(text); ok {
			if sink != nil {
				sink.Progress(progress)
			}
			if detail := StatusDetail(text); detail != "" && sink != nil {
				sink.Output("stdout", detail+"\n")
			}
			continue
		}
		if sink != nil {
			sink.Output("stdout", line)
		} else if options.Stdout != nil {
			_, _ = io.WriteString(options.Stdout, line)
		}
		if readErr == io.EOF {
			break
		}
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("E_NINJA_FAILED: %w", waitErr)
	}
	return nil
}

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
