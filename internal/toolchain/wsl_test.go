package toolchain

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

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

func TestDistributionLinesUTF16AndForeignPaths(t *testing.T) {
	units := utf16.Encode([]rune("开发 环境\r\nUbuntu-24.04\r\n"))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	if got := WSLDistributions(data); !reflect.DeepEqual(got, []string{"开发 环境", "Ubuntu-24.04"}) {
		t.Fatalf("distribution names corrupted: %q", got)
	}
	if err := ValidateWSLPath("Ubuntu-24.04", `\\wsl.localhost\Other\home\user\src`); err == nil {
		t.Fatal("foreign distribution path accepted")
	}
	if err := ValidateWSLPath("Ubuntu-24.04", `\\wsl$\Ubuntu-24.04\home\user\src`); err != nil {
		t.Fatal(err)
	}
}
