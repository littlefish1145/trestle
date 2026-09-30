package runlog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"trestle/internal/config"
)

func TestCompleteHistoryAndIncrementalPaging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	run, err := Start(path, "long build")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&text, "line %06d 中文\n", i)
	}
	text.WriteString("\n\x1b[31m" + strings.Repeat("路径", 1000) + "TAIL\x1b[0m")
	if _, err := run.Write([]byte(text.String())); err != nil {
		t.Fatal(err)
	}
	reader := Reader{Path: run.Record.LogPath}
	lines, err := reader.Page(99999, 3)
	if err != nil || len(lines) != 3 || !strings.Contains(lines[2], "TAIL") {
		t.Fatalf("tail lost: %d %v", len(lines), err)
	}
	if reader.Count() != 100002 {
		t.Fatalf("incorrect count %d", reader.Count())
	}
	if _, err := run.Write([]byte("-CONTINUED\nlast\n")); err != nil {
		t.Fatal(err)
	}
	lines, err = reader.Page(100001, 2)
	if err != nil || len(lines) != 2 || !strings.Contains(lines[0], "CONTINUED") || lines[1] != "last" {
		t.Fatalf("incremental index lost output: %v %v", lines, err)
	}
	if err := run.Finish(context.Canceled); err != nil {
		t.Fatal(err)
	}
	records, err := List(path)
	if err != nil || len(records) != 1 || records[0].Status != "cancelled" {
		t.Fatalf("wrong history: %v %v", records, err)
	}
	actual, err := os.ReadFile(run.Record.LogPath)
	if err != nil || string(actual) != text.String()+"-CONTINUED\nlast\n" {
		t.Fatal("stored output changed")
	}
}

func TestLogFailureReportedAndInterruptedRunRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	if err := config.Save(path, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	run, err := Start(path, "task")
	if err != nil {
		t.Fatal(err)
	}
	if records, _ := List(path); records[0].Status != "running" {
		t.Fatal("live operation marked interrupted")
	}
	run.file.Close()
	run.lock.Close()
	records, err := List(path)
	if err != nil || records[0].Status != "interrupted" {
		t.Fatalf("stale state not recovered: %v %v", records, err)
	}
	if _, err := run.Write([]byte("cannot write")); err == nil {
		t.Fatal("missing write failure")
	}
	if err := run.Finish(nil); err == nil {
		t.Fatal("finish hid disk failure")
	}
}
