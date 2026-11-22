package buildanalyzer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// LogEntry is one successfully recorded output, with Ninja-relative times.
type LogEntry struct {
	Output      string `json:"output"`
	StartMS     int64  `json:"start_ms"`
	EndMS       int64  `json:"end_ms"`
	MTime       int64  `json:"mtime"`
	CommandHash string `json:"command_hash"`
}

type logSnapshot struct {
	size int64
	hash []byte
}

func snapshotLog(path string) (logSnapshot, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return logSnapshot{}, nil
	}
	if err != nil {
		return logSnapshot{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return logSnapshot{size: size, hash: hash.Sum(nil)}, err
}

func newLogEntries(path string, before logSnapshot) ([]LogEntry, error) {
	entries := []LogEntry{}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && before.size == 0 {
		return entries, nil
	}
	if err != nil {
		return entries, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	header, err := reader.ReadString('\n')
	if err == io.EOF && header == "" && before.size == 0 {
		return entries, nil
	}
	switch strings.TrimSpace(header) {
	case "# ninja log v5", "# ninja log v6", "# ninja log v7":
	default:
		return entries, fmt.Errorf("unsupported Ninja log header %q", header)
	}
	if before.size > 0 {
		hash := sha256.New()
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return entries, err
		}
		if _, err := io.CopyN(hash, file, before.size); err != nil || !bytes.Equal(before.hash, hash.Sum(nil)) {
			return entries, errors.New("Ninja log prefix changed during the invocation")
		}
		reader.Reset(file)
	}
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF && line == "" {
			return entries, nil
		}
		if err != nil {
			return entries, fmt.Errorf("incomplete Ninja log record: %w", err)
		}
		entry, err := parseLogEntry(strings.TrimRight(line, "\r\n"))
		if err != nil {
			return entries, err
		}
		entries = append(entries, entry)
	}
}

func parseLogEntry(line string) (LogEntry, error) {
	field := strings.Split(line, "\t")
	if len(field) != 5 || field[3] == "" {
		return LogEntry{}, errors.New("malformed Ninja log record")
	}
	start, startErr := strconv.ParseInt(field[0], 10, 64)
	end, endErr := strconv.ParseInt(field[1], 10, 64)
	mtime, mtimeErr := strconv.ParseInt(field[2], 10, 64)
	_, hashErr := strconv.ParseUint(field[4], 16, 64)
	if startErr != nil || endErr != nil || mtimeErr != nil || hashErr != nil || start < 0 || end < start {
		return LogEntry{}, errors.New("invalid Ninja log timing, timestamp or command hash")
	}
	return LogEntry{Output: field[3], StartMS: start, EndMS: end, MTime: mtime, CommandHash: field[4]}, nil
}
