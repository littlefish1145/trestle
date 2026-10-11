//go:build windows

package fsx

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// replaceWithRetry renames the prepared temporary over the destination.
//
// On Windows a rename over an existing file fails with a sharing violation
// while any other handle to that file is still open, which happens routinely:
// the lock owner writes its metadata, and a contending process reads it to
// report who holds the resource. Those handles are short-lived, so retrying
// briefly turns a spurious "Access is denied" into a successful write.
//
// The retry is deliberately bounded. A handle that outlives the deadline is a
// real problem — another process is genuinely holding the file open — and the
// error should surface rather than spin.
func replaceWithRetry(source, destination string) error {
	const attempts = 40
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = Replace(source, destination)
		if err == nil || !replaceBlocked(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Millisecond)
	}
	return err
}

// replaceBlocked reports whether the error is Windows refusing to replace a
// file that another handle currently has open.
func replaceBlocked(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
