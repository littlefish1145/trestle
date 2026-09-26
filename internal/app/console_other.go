//go:build !windows

package app

import "strings"

func decodeConsoleOutput(data []byte) string {
	return strings.ToValidUTF8(string(data), "�")
}
