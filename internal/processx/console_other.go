//go:build !windows

package processx

// DecodeOutput returns process output unchanged, since non-Windows tools emit
// UTF-8.
func DecodeOutput(data []byte) string { return string(data) }
