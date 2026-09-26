package toolchain

import "testing"

func TestParseWSLPathIgnoresUTF16StartupWarning(t *testing.T) {
	output := []byte("w\x00s\x00l\x00:\x00 \x00warning\x00\r\x00\n\x00/mnt/c/Users/fish_/Desktop/swgl/build/debug\r\n")
	got := parseWSLPath(output)
	want := "/mnt/c/Users/fish_/Desktop/swgl/build/debug"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseWSLPathRejectsWarningOnly(t *testing.T) {
	if got := parseWSLPath([]byte("wsl: warning only\n")); got != "" {
		t.Fatalf("got %q", got)
	}
}
