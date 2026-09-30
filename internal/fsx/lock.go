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

type LockOwner struct {
	Resource  string    `json:"resource"`
	Operation string    `json:"operation"`
	PID       int       `json:"pid"`
	Started   time.Time `json:"started"`
}

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

func TryLock(ctx context.Context, resource, operation string) (*Lock, error) {
	return acquire(ctx, resource, operation, false)
}

// Shared readers protect installed dependencies from concurrent installation.
func TryReadLock(ctx context.Context, resource, operation string) (*Lock, error) {
	return acquire(ctx, resource, operation, true)
}

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
		var owner LockOwner
		data, _ := os.ReadFile(path + ".json")
		_ = json.Unmarshal(data, &owner)
		if reader, ok := activeReaderOwner(canonical); ok {
			owner = reader
		}
		detail := fmt.Sprintf("Resource: %s\nOperation: %s\nPID: %d\nStarted: %s", canonical, owner.Operation, owner.PID, owner.Started.Format(time.RFC3339))
		e := diag.New("E_RESOURCE_BUSY", diag.StageBuild, "resource is in use", err)
		e.Detail = strings.TrimSpace(detail)
		e.Hints = []string{"Wait for the owning operation to finish, or cancel it in its console, then retry. Lock files do not need to be deleted."}
		return nil, e
	}
	lock := &Lock{file: file}
	if shared {
		readerPath := filepath.Join(canonical+".trestle-readers", fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()))
		reader, err := TryLock(ctx, readerPath, operation)
		if err != nil {
			lock.Close()
			return nil, err
		}
		lock.reader, lock.readerPath = reader, readerPath
	} else {
		data, _ := json.Marshal(LockOwner{canonical, operation, os.Getpid(), time.Now().UTC()})
		if err := AtomicWrite(path+".json", data); err != nil {
			lock.Close()
			return nil, err
		}
	}
	return lock, nil
}

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
