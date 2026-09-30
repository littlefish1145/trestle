//go:build windows

package app

import "trestle/internal/processx"

func decodeConsoleOutput(data []byte) string { return processx.DecodeOutput(data) }
