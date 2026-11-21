package app

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trestle/internal/model"
	"trestle/internal/runner"
)

// BuildObserver receives measurements from an individual Ninja invocation.
// Callbacks must be safe for concurrent calls and return promptly.
type BuildObserver struct {
	Analysis  func(NinjaInvocation)
	Artifacts func(map[model.TargetID]string)
	Log       func(path string)
	Progress  func(member string, value runner.Progress)
	Timings   func(member string, origin time.Time, entries []NinjaTiming)
}

type NinjaTiming struct {
	Output  string         `json:"output"`
	Target  model.TargetID `json:"target,omitempty"`
	StartMS int64          `json:"start_ms"`
	EndMS   int64          `json:"end_ms"`
}

type buildObserverKey struct{}

func WithBuildObserver(ctx context.Context, observer BuildObserver) context.Context {
	return context.WithValue(ctx, buildObserverKey{}, observer)
}

// observeNinjaLog only publishes records demonstrably appended by this
// invocation. A replaced/compacted journal provides no reliable delta.
func observeNinjaLog(dir string) func() []NinjaTiming {
	path := filepath.Join(dir, ".ninja_log")
	before, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return func() []NinjaTiming { return nil }
	}
	var info os.FileInfo
	var tail []byte
	if before != nil {
		defer before.Close()
		info, err = before.Stat()
		if err != nil {
			return func() []NinjaTiming { return nil }
		}
		tail = make([]byte, min(4096, info.Size()))
		if _, err := before.ReadAt(tail, info.Size()-int64(len(tail))); err != nil {
			return func() []NinjaTiming { return nil }
		}
	}
	return func() []NinjaTiming {
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		reader := bufio.NewReader(file)
		header, err := reader.ReadString('\n')
		if err != nil || (header != "# ninja log v5\n" && header != "# ninja log v6\n" && header != "# ninja log v7\n") {
			return nil
		}
		if info != nil {
			after, err := file.Stat()
			if err != nil || !os.SameFile(info, after) || after.Size() <= info.Size() {
				return nil
			}
			actual := make([]byte, len(tail))
			if _, err := file.ReadAt(actual, info.Size()-int64(len(tail))); err != nil || !bytes.Equal(actual, tail) {
				return nil
			}
			if _, err := file.Seek(info.Size(), io.SeekStart); err != nil {
				return nil
			}
			reader.Reset(file)
		}
		var entries []NinjaTiming
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() && len(entries) < 10000 {
			fields := strings.Split(scanner.Text(), "\t")
			if len(fields) != 5 {
				continue
			}
			start, err1 := strconv.ParseInt(fields[0], 10, 64)
			end, err2 := strconv.ParseInt(fields[1], 10, 64)
			if err1 == nil && err2 == nil && start >= 0 && end >= start {
				entries = append(entries, NinjaTiming{Output: fields[3], StartMS: start, EndMS: end})
			}
		}
		return entries
	}
}
