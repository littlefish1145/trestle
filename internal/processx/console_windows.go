//go:build windows

package processx

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// looksLikeUTF16LE reports whether data starts with a UTF-16LE byte-order mark or
// with an ASCII character whose following byte is a NUL, the shape wsl.exe emits.
func looksLikeUTF16LE(data []byte) bool {
	return len(data) > 1 && (data[0] == 0xff && data[1] == 0xfe || data[1] == 0)
}

// decodeUTF16LE decodes the UTF-16LE prefix of data up to the first newline and
// recurses into any trailing bytes, so UTF-8 written after a UTF-16 warning by
// the same pipe is still decoded.
func decodeUTF16LE(data []byte) string {
	if data[0] == 0xff && data[1] == 0xfe {
		data = data[2:]
	}
	split := bytes.Index(data, []byte{'\n', 0})
	end := len(data)
	if split >= 0 {
		end = split + 2
	}
	segment := data[:end]
	if len(segment)%2 != 0 {
		segment = append(append([]byte{}, segment...), 0)
	}
	wide := make([]uint16, len(segment)/2)
	for i := range wide {
		wide[i] = binary.LittleEndian.Uint16(segment[2*i:])
	}
	text := string(utf16.Decode(wide))
	if end < len(data) {
		text += DecodeOutput(data[end:])
	}
	return text
}

// decodeConsoleText decodes non-UTF-16 data. It strips the NUL code-unit bytes
// wsl.exe may interleave, returns valid UTF-8 as-is, and otherwise decodes with
// the active console code page, falling back to UTF-8 sanitisation.
func decodeConsoleText(data []byte) string {
	data = bytes.ReplaceAll(data, []byte{0}, nil)
	if utf8.Valid(data) {
		return string(data)
	}
	if len(data) == 0 {
		return ""
	}
	codePage, err := windows.GetConsoleOutputCP()
	if err != nil || codePage == 0 {
		codePage = windows.GetACP()
	}
	size, err := windows.MultiByteToWideChar(codePage, 0, &data[0], int32(len(data)), nil, 0)
	if err != nil || size <= 0 {
		return strings.ToValidUTF8(string(data), "�")
	}
	wide := make([]uint16, size)
	written, err := windows.MultiByteToWideChar(codePage, 0, &data[0], int32(len(data)), &wide[0], size)
	if err != nil || written <= 0 {
		return strings.ToValidUTF8(string(data), "�")
	}
	return windows.UTF16ToString(wide[:written])
}

// DecodeOutput converts captured process output to UTF-8, detecting UTF-16LE
// written by wsl.exe and falling back to the active console code page.
func DecodeOutput(data []byte) string {
	if looksLikeUTF16LE(data) {
		return decodeUTF16LE(data)
	}
	return decodeConsoleText(data)
}
