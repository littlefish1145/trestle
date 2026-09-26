//go:build windows

package toolchain

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

func decodeNativeOutput(data []byte) string {
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
