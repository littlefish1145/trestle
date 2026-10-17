package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trestle/internal/diag"
)

// LockOwner records who holds a resource and what they are doing, so a
// contending process can report the blocking operation instead of a bare
// resource-busy message.
type LockOwner struct {
	Resource  string    `json:"resource"`
	Operation string    `json:"operation"`
	PID       int       `json:"pid"`
	Started   time.Time `json:"started"`
}

// Lock is a held file lock. Close releases it exactly once, so callers can defer
// Close without guarding against a double release.
type Lock struct {
	file       *os.File
	once       sync.Once
	reader     *Lock
	readerPath string
}

// Canonical resolves existing ancestors as well as not-yet-created outputs.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := filepath.Clean(abs)
	var tail []string
	for {
		resolved, e := filepath.EvalSymlinks(ancestor)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", e
		}
		tail = append(tail, filepath.Base(ancestor))
		ancestor = parent
	}
}

// TryLock takes an exclusive lock on resource and returns E_RESOURCE_BUSY
// immediately rather than waiting when another process already holds it.
func TryLock(ctx context.Context, resource, operation string) (*Lock, error) {
	return acquire(ctx, resource, operation, false)
}

// Shared readers protect installed dependencies from concurrent installation.
func TryReadLock(ctx context.Context, resource, operation string) (*Lock, error) {
	return acquire(ctx, resource, operation, true)
}

// WaitLock takes an exclusive lock on resource, polling until the current holder
// releases it or ctx is cancelled.
func WaitLock(ctx context.Context, resource, operation string) (*Lock, error) {
	for {
		lock, err := TryLock(ctx, resource, operation)
		if err == nil {
			return lock, nil
		}
		var structured *diag.Error
		if !errors.As(err, &structured) || structured.Code != "E_RESOURCE_BUSY" {
			return nil, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func acquire(ctx context.Context, resource, operation string, shared bool) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := Canonical(resource)
	if err != nil {
		return nil, err
	}
	file, err := createLockFile(canonical, shared)
	if err != nil {
		return nil, err
	}
	lock := &Lock{file: file}
	if err := recordLock(ctx, lock, canonical, operation, shared); err != nil {
		return nil, err
	}
	return lock, nil
}

// createLockFile creates the lock file for canonical and takes an OS lock on it,
// shared or exclusive. A busy lock is reported as an E_RESOURCE_BUSY diagnostic;
// other failures are returned unchanged.
func createLockFile(canonical string, shared bool) (*os.File, error) {
	path := canonical + ".trestle-lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(file, shared); err != nil {
		file.Close()
		if !lockBusy(err) {
			return nil, err
		}
		return nil, busyLockError(canonical, path, err)
	}
	return file, nil
}

// recordLock publishes the metadata a contending process reads to identify the
// holder: reader metadata for a shared lock, owner metadata for an exclusive
// one. It releases lock before returning on failure.
func recordLock(ctx context.Context, lock *Lock, canonical, operation string, shared bool) error {
	if shared {
		return registerReader(ctx, lock, canonical, operation)
	}
	return writeOwner(lock, canonical, operation)
}

// busyLockError builds the E_RESOURCE_BUSY diagnostic for resource, describing
// the blocking operation from owner metadata or, when the lock is held by
// readers, from the first active reader. cause is the underlying lock error.
func busyLockError(resource, lockPath string, cause error) error {
	var owner LockOwner
	data, _ := os.ReadFile(lockPath + ".json")
	_ = json.Unmarshal(data, &owner)
	if reader, ok := activeReaderOwner(resource); ok {
		owner = reader
	}
	detail := fmt.Sprintf("Resource: %s\nOperation: %s\nPID: %d\nStarted: %s", resource, owner.Operation, owner.PID, owner.Started.Format(time.RFC3339))
	e := diag.New("E_RESOURCE_BUSY", diag.StageBuild, "resource is in use", cause)
	e.Detail = strings.TrimSpace(detail)
	e.Hints = []string{"Wait for the owning operation to finish, or cancel it in its console, then retry. Lock files do not need to be deleted."}
	return e
}

// registerReader records a shared lock's reader metadata by taking a nested
// exclusive lock that excludes a concurrent writer. It releases lock before
// returning when the nested lock cannot be taken.
func registerReader(ctx context.Context, lock *Lock, canonical, operation string) error {
	readerPath := filepath.Join(canonical+".trestle-readers", fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()))
	reader, err := TryLock(ctx, readerPath, operation)
	if err != nil {
		lock.Close()
		return err
	}
	lock.reader, lock.readerPath = reader, readerPath
	return nil
}

// writeOwner records an exclusive lock's owner metadata so a contending process
// can report which operation holds the resource. It releases lock before
// returning when the metadata cannot be written.
func writeOwner(lock *Lock, canonical, operation string) error {
	path := canonical + ".trestle-lock"
	data, _ := json.Marshal(LockOwner{canonical, operation, os.Getpid(), time.Now().UTC()})
	if err := AtomicWrite(path+".json", data); err != nil {
		lock.Close()
		return fmt.Errorf("writing lock owner metadata: %w", err)
	}
	return nil
}

// Close releases the lock and removes any reader metadata it created. Calling
// it more than once is safe.
func (lock *Lock) Close() {
	if lock != nil {
		lock.once.Do(func() {
			if lock.reader != nil {
				lock.reader.Close()
				_ = os.Remove(lock.readerPath + ".trestle-lock.json")
				_ = os.Remove(lock.readerPath + ".trestle-lock")
			}
			_ = unlockFile(lock.file)
			_ = lock.file.Close()
		})
	}
}

// Validate reader metadata against its OS lock before reporting an owner.
func activeReaderOwner(resource string) (LockOwner, bool) {
	paths, _ := filepath.Glob(filepath.Join(resource+".trestle-readers", "*.trestle-lock.json"))
	for _, path := range paths {
		file, err := os.OpenFile(strings.TrimSuffix(path, ".json"), os.O_RDWR, 0600)
		if err != nil {
			continue
		}
		err = lockFile(file, false)
		if err == nil {
			_ = unlockFile(file)
		}
		_ = file.Close()
		if !lockBusy(err) {
			continue
		}
		var owner LockOwner
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &owner) == nil {
			owner.Resource = resource
			return owner, true
		}
	}
	return LockOwner{}, false
}
