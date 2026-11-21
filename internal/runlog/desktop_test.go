package runlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopLogLimitAndRetention(t *testing.T) {
	config := filepath.Join(t.TempDir(), "trestle.toml")
	old, err := start(config, "build", "desktop", 8)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("123456789")
	if n, err := old.Write(content); n != len(content) || err != nil {
		t.Fatalf("bounded log changed producer result: %d %v", n, err)
	}
	if err := old.Finish(nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(old.Record.LogPath)
	if err != nil || !old.Record.LogTruncated || !bytes.HasPrefix(body, []byte("12345678\n[trestle]")) {
		t.Fatalf("missing limit or marker: %q %v", body, err)
	}
	cli, err := Start(config, "build")
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Finish(nil); err != nil {
		t.Fatal(err)
	}
	active, err := StartDesktop(config, "build")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = active.Finish(nil) }()
	fresh, err := StartDesktop(config, "build")
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Finish(nil); err != nil {
		t.Fatal(err)
	}
	pruneDesktop(config, 1)
	if _, err := os.Stat(old.Record.LogPath); !os.IsNotExist(err) {
		t.Fatalf("old Desktop log survived: %v", err)
	}
	for _, run := range []*Run{cli, active, fresh} {
		if _, err := os.Stat(run.Record.LogPath); err != nil {
			t.Fatalf("expected retained log %s: %v", run.Record.ID, err)
		}
	}
}
