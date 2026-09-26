//go:build windows

package app

import "testing"

func TestDecodeConsoleOutputRemovesWSLNULBytes(t *testing.T) {
	got := decodeConsoleOutput([]byte{'w', 0, 's', 0, 'l', 0, ':', 0, ' ', 0, 'x', 0})
	if got != "wsl: x" {
		t.Fatalf("got %q", got)
	}
}
