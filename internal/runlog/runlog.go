// Package runlog stores operation history independently of the terminal viewport.
package runlog

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"trestle/internal/config"
	"trestle/internal/diag"
	"trestle/internal/fsx"
	"trestle/internal/processx"
)

// Record is the persisted metadata of one operation run: what ran, when, its
// status, and where the full log lives.
type Record struct {
	ID          string     `json:"id"`
	Operation   string     `json:"operation"`
	Started     time.Time  `json:"started"`
	Finished    *time.Time `json:"finished,omitempty"`
	Status      string     `json:"status"`
	Fingerprint string     `json:"config_fingerprint"`
	LogPath     string     `json:"log_path"`
	Error       string     `json:"error,omitempty"`
}

// Run is one live operation: its record, the log file being written, and the
// lock that marks it active.
type Run struct {
	Record Record
	path   string
	file   *os.File
	lock   *fsx.Lock
	mu     sync.Mutex
	err    error
}

// Start begins a new operation run, creating its log directory and active lock
// under the project's .trestle/runs directory.
func Start(configPath, operation string) (*Run, error) {
	root, err := filepath.Abs(filepath.Join(filepath.Dir(configPath), ".trestle", "runs"))
	if err != nil {
		return nil, err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(nonce[:])
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lock, err := fsx.TryLock(context.Background(), filepath.Join(dir, "active"), operation)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		lock.Close()
		return nil, err
	}
	fingerprint, err := config.Snapshot(configPath)
	if err != nil {
		file.Close()
		lock.Close()
		return nil, err
	}
	run := &Run{Record: Record{ID: id, Operation: operation, Started: time.Now().UTC(), Status: "running", Fingerprint: fingerprint, LogPath: file.Name()}, path: filepath.Join(dir, "record.json"), file: file, lock: lock}
	if err := run.save(); err != nil {
		file.Close()
		lock.Close()
		return nil, err
	}
	return run, nil
}

func (run *Run) save() error {
	data, err := json.MarshalIndent(run.Record, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(run.path, append(data, '\n'))
}

// Write appends data to the run log, flushing it to disk and recording the
// first write error for Finish to report.
func (run *Run) Write(data []byte) (int, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.err != nil {
		return 0, run.err
	}
	n, err := run.file.Write(data)
	if err == nil {
		err = run.file.Sync()
	}
	if err != nil {
		run.err = err
	}
	return n, err
}

// Err returns the first write error the run encountered, if any.
func (run *Run) Err() error { run.mu.Lock(); defer run.mu.Unlock(); return run.err }

// Finish closes the run's log, records its terminal status derived from result,
// and releases the active lock.
func (run *Run) Finish(result error) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	defer run.lock.Close()
	if err := run.file.Sync(); run.err == nil {
		run.err = err
	}
	if err := run.file.Close(); run.err == nil {
		run.err = err
	}
	if run.err != nil {
		result = errors.Join(result, run.err)
	}
	now := time.Now().UTC()
	run.Record.Finished = &now
	switch {
	case run.err != nil:
		run.Record.Status = "failed"
	case errors.Is(result, context.DeadlineExceeded):
		run.Record.Status = "timed_out"
	case errors.Is(result, context.Canceled):
		run.Record.Status = "cancelled"
	case result != nil:
		run.Record.Status = "failed"
	default:
		run.Record.Status = "succeeded"
	}
	if result != nil {
		run.Record.Error = result.Error()
	}
	return errors.Join(run.err, run.save())
}

// List returns every recorded run for the project, newest first, marking runs
// whose owning process has disappeared as interrupted.
func List(configPath string) ([]Record, error) {
	root := filepath.Join(filepath.Dir(configPath), ".trestle", "runs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if record, ok := readRecord(root, entry.Name()); ok {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Started.After(records[j].Started) })
	return records, nil
}

// readRecord loads one run directory's metadata, recovering an interrupted run
// when its owning process has disappeared. ok is false when the directory holds
// no readable record.
func readRecord(root, name string) (Record, bool) {
	path := filepath.Join(root, name, "record.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false
	}
	var record Record
	if json.Unmarshal(data, &record) != nil {
		return Record{}, false
	}
	if record.Status == "running" {
		recoverInterrupted(root, name, path, &record)
	}
	return record, true
}

// recoverInterrupted marks a still-running record as interrupted when its lock
// can be reclaimed, reloading under the lock to avoid racing a completion.
func recoverInterrupted(root, name, path string, record *Record) {
	lock, err := fsx.TryLock(context.Background(), filepath.Join(root, name, "active"), "recover interrupted operation")
	if err != nil {
		return
	}
	defer lock.Close()
	// Reload under the lock: completion may have raced the first read.
	data, readErr := os.ReadFile(path)
	if readErr != nil || json.Unmarshal(data, record) != nil || record.Status != "running" {
		return
	}
	record.Status = "interrupted"
	now := time.Now().UTC()
	record.Finished = &now
	record.Error = "Previous process stopped. Inspect the complete log, then retry; tasks require a fresh preview."
	data, _ = json.MarshalIndent(record, "", "  ")
	_ = fsx.AtomicWrite(path, data)
}

// Reader indexes byte offsets, keeping only the requested page in memory.
type Reader struct {
	Path     string
	offsets  []int64
	indexed  int64
	trailing bool
}

// Refresh rereads the log file and rebuilds the byte-offset index of its lines,
// reusing prior work when the file has only grown.
func (reader *Reader) Refresh() error {
	file, err := os.Open(reader.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < reader.indexed {
		reader.offsets = nil
		reader.indexed = 0
		reader.trailing = false
	}
	if len(reader.offsets) == 0 {
		reader.offsets = []int64{0}
	}
	if _, err := file.Seek(reader.indexed, io.SeekStart); err != nil {
		return err
	}
	if err := reader.indexNewContent(file); err != nil {
		return err
	}
	// An offset at EOF is a sentinel, not another empty line.
	reader.trailing = reader.offsets[len(reader.offsets)-1] == reader.indexed
	return nil
}

// indexNewContent scans the bytes after the current index, appending a line
// offset for every newline it finds.
func (reader *Reader) indexNewContent(file *os.File) error {
	buffer := make([]byte, 64*1024)
	for {
		n, err := file.Read(buffer)
		for i, b := range buffer[:n] {
			if b == '\n' {
				reader.offsets = append(reader.offsets, reader.indexed+int64(i)+1)
			}
		}
		reader.indexed += int64(n)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Count returns the number of complete lines currently in the log.
func (reader *Reader) Count() int {
	count := len(reader.offsets)
	if reader.trailing {
		count--
	}
	return count
}

// Page returns up to count lines starting at the zero-based line index start,
// refreshing the index from the file first.
func (reader *Reader) Page(start, count int) ([]string, error) {
	if err := reader.Refresh(); err != nil {
		return nil, err
	}
	if start < 0 {
		start = 0
	}
	if start >= reader.Count() || count <= 0 {
		return nil, nil
	}
	file, err := os.Open(reader.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(reader.offsets[start], io.SeekStart); err != nil {
		return nil, err
	}
	return readLines(bufio.NewReader(file), count)
}

// readLines decodes and trims up to count lines from buffer.
func readLines(buffer *bufio.Reader, count int) ([]string, error) {
	var lines []string
	for len(lines) < count {
		line, err := buffer.ReadString('\n')
		if line != "" {
			lines = append(lines, strings.TrimSuffix(strings.TrimSuffix(processx.DecodeOutput([]byte(line)), "\n"), "\r"))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return lines, nil
}

// LogError wraps a log-writing failure as a structured diagnostic that points
// at the affected log path.
func LogError(path string, cause error) error {
	e := diag.New("E_LOG_WRITE", diag.StageBuild, "cannot save complete operation log", cause)
	e.LogPath = path
	e.Hints = []string{"Check disk space and directory permissions, then retry. Existing log content was preserved."}
	return e
}

// Find returns the index of the first line at or after start containing query
// case-insensitively, or -1 when there is no match.
func (reader *Reader) Find(query string, start int) (int, error) {
	if err := reader.Refresh(); err != nil {
		return -1, err
	}
	for index := start; index < reader.Count(); index += 256 {
		lines, err := reader.Page(index, 256)
		if err != nil {
			return -1, err
		}
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
				return index + i, nil
			}
		}
	}
	return -1, nil
}

var _ io.Writer = (*Run)(nil)
