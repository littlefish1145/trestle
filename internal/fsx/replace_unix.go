//go:build !windows

package fsx

import "os"

func Replace(source, destination string) error { return os.Rename(source, destination) }
