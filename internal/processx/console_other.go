//go:build !windows

package processx

func DecodeOutput(data []byte) string { return string(data) }
