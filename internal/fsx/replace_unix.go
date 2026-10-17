//go:build !windows

package fsx

import "os"

// Replace renames source onto destination, atomically replacing any existing
// destination.
func Replace(source, destination string) error { return os.Rename(source, destination) }
