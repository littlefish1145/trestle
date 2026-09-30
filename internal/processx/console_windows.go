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

func DecodeOutput(data []byte) string {
	if len(data) > 1 && (data[0] == 0xff && data[1] == 0xfe || data[1] == 0) {
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
	// wsl.exe may write startup diagnostics as UTF-16LE while the child process
	// writes UTF-8 to the same pipe. Removing NUL code-unit bytes preserves the
	// ASCII prefix used to identify the WSL warning and avoids terminal NULs.
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
