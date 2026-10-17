package processx

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"sync"
)

type outputKey struct{}
type output struct {
	writer io.Writer
	notify func(string)
}

// Sync serializes writes to a sink consumed by more than one process stream,
// so streams never interleave bytes inside a writer that expects Write calls
// to be atomic.
func Sync(w io.Writer) io.Writer { return &syncWriter{w: w} }

type syncWriter struct {
	mutex sync.Mutex
	w     io.Writer
}

func (writer *syncWriter) Write(data []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.w.Write(data)
}

// WithOutput returns a context carrying the sink and per-chunk callback that
// Capture and other process helpers write through.
func WithOutput(ctx context.Context, writer io.Writer, notify func(string)) context.Context {
	return context.WithValue(ctx, outputKey{}, output{writer, notify})
}

// Output returns the writer stored in ctx by WithOutput, or nil when none is
// set.
func Output(ctx context.Context) io.Writer {
	value, _ := ctx.Value(outputKey{}).(output)
	return value.writer
}

// Notify returns the per-chunk callback stored in ctx by WithOutput, or
// fallback when the context carries no callback.
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
