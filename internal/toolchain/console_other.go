//go:build !windows

package toolchain

import "strings"

func decodeNativeOutput(data []byte) string { return strings.ToValidUTF8(string(data), "�") }
