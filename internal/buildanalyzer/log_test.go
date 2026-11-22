package buildanalyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogSessionBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ninja_log")
	const old = "# ninja log v7\r\n0\t2\t123\told.obj\t123a\r\n"
	const added = "0\t2\t123\tC:\\中文 路径\\output.obj\t456b\r\n"
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(old)
	before, err := snapshotLog(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := newLogEntries(path, before)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty append: %+v, %v", entries, err)
	}
	write(old + added)
	entries, err = newLogEntries(path, before)
	if err != nil || len(entries) != 1 || entries[0].Output != `C:\中文 路径\output.obj` {
		t.Fatalf("append: %+v, %v", entries, err)
	}
	write(old + added + "0\t2\t")
	entries, err = newLogEntries(path, before)
	if err == nil || len(entries) != 1 {
		t.Fatalf("truncated append: %+v, %v", entries, err)
	}
	write("# ninja log v7\r\n" + added)
	entries, err = newLogEntries(path, before)
	if err == nil || len(entries) != 0 {
		t.Fatalf("compaction: %+v, %v", entries, err)
	}
}
