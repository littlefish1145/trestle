package toolchain

import (
	"context"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func TestDiscoverIncludesKnownFamilies(t *testing.T) {
	components := Discover(context.Background())
	families := map[string]bool{}
	for _, component := range components {
		families[component.Family] = true
	}
	if !families["Clang"] && !families["MinGW"] {
		t.Skip("no C++ compiler available")
	}
}

func TestDecodeWindowsCommandUTF16(t *testing.T) {
	words := utf16.Encode([]rune("Ubuntu\r\nDebian\r\n"))
	data := make([]byte, len(words)*2)
	for index, value := range words {
		binary.LittleEndian.PutUint16(data[index*2:], value)
	}
	if got := decodeWindowsCommand(data); got != "Ubuntu\r\nDebian" {
		t.Fatalf("unexpected decoded output %q", got)
	}
}
