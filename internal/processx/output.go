package processx

import (
	"bytes"
	"context"
	"io"
	"os/exec"
)

type outputKey struct{}
type output struct {
	writer io.Writer
	notify func(string)
}

func WithOutput(ctx context.Context, writer io.Writer, notify func(string)) context.Context {
	return context.WithValue(ctx, outputKey{}, output{writer, notify})
}
func Output(ctx context.Context) io.Writer {
	value, _ := ctx.Value(outputKey{}).(output)
	return value.writer
}
func Notify(ctx context.Context, fallback func(string)) func(string) {
	if value, ok := ctx.Value(outputKey{}).(output); ok {
		if value.notify == nil {
			return func(string) {}
		}
		return value.notify
	}
	return fallback
}

// Capture preserves protocol output for parsers while persisting every chunk
// before displaying it, including a final fragment without a newline.
func Capture(ctx context.Context, command *exec.Cmd) ([]byte, error) {
	var buffer bytes.Buffer
	var writer io.Writer = &buffer
	if sink := Output(ctx); sink != nil {
		writer = &captureWriter{sink: io.MultiWriter(sink, &buffer), notify: Notify(ctx, nil)}
	}
	command.Stdout, command.Stderr = writer, writer
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return buffer.Bytes(), err
}

type captureWriter struct {
	sink   io.Writer
	notify func(string)
}

func (writer *captureWriter) Write(data []byte) (int, error) {
	n, err := writer.sink.Write(data)
	if err == nil && writer.notify != nil {
		writer.notify(DecodeOutput(data))
	}
	return n, err
}
