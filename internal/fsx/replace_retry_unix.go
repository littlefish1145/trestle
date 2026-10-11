//go:build !windows

package fsx

// replaceWithRetry renames the temporary over the destination. On POSIX a
// rename replaces the destination atomically and never fails merely because
// another process has it open, so no retry is needed.
func replaceWithRetry(source, destination string) error { return Replace(source, destination) }
