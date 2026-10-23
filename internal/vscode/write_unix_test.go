//go:build unix

package vscode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReportsUnwritableDirectoryErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := filepath.Join(t.TempDir(), ".vscode")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := Write(filepath.Join(dir, "tasks.json"), Generate(Project{}), ModeReplace); err == nil {
		t.Fatal("an unwritable directory must fail the atomic write")
	}
}
