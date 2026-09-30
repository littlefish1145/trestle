//go:build !windows

package fsx

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(file *os.File, shared bool) error {
	flag := unix.LOCK_EX
	if shared {
		flag = unix.LOCK_SH
	}
	return unix.Flock(int(file.Fd()), flag|unix.LOCK_NB)
}
func unlockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }
func lockBusy(err error) bool        { return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) }
