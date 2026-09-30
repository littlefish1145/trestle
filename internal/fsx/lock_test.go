package fsx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"trestle/internal/diag"
)

func TestLockChild(t *testing.T) {
	if os.Getenv("TRESTLE_LOCK_CHILD") == "" {
		return
	}
	lock, err := TryLock(context.Background(), os.Getenv("TRESTLE_LOCK_CHILD"), "child operation")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("locked")
	time.Sleep(30 * time.Second)
}

func TestLockAcrossProcessesAndCrashRelease(t *testing.T) {
	resource := filepath.Join(t.TempDir(), "output")
	command := exec.Command(os.Args[0], "-test.run=^TestLockChild$")
	command.Env = append(os.Environ(), "TRESTLE_LOCK_CHILD="+resource)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("child failed: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child lock timeout")
	}
	_, err = TryLock(context.Background(), resource, "second build")
	var structured *diag.Error
	if !errors.As(err, &structured) || structured.Code != "E_RESOURCE_BUSY" || !strings.Contains(structured.Detail, "child operation") {
		t.Fatalf("missing lock owner: %v", err)
	}
	other, err := TryLock(context.Background(), resource+"-other", "parallel output")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	_ = command.Process.Kill()
	_ = command.Wait()
	lock, err := TryLock(context.Background(), resource, "retry after crash")
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
}

func TestSharedReadersExcludeWriter(t *testing.T) {
	resource := filepath.Join(t.TempDir(), "installed")
	a, err := TryReadLock(context.Background(), resource, "build A")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := TryReadLock(context.Background(), resource, "build B")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := TryLock(context.Background(), resource, "install"); err == nil {
		t.Fatal("installer raced active readers")
	} else {
		var structured *diag.Error
		if !errors.As(err, &structured) || !strings.Contains(structured.Detail, "build A") || !strings.Contains(structured.Detail, fmt.Sprint(os.Getpid())) {
			t.Fatalf("reader owner missing: %v", err)
		}
	}
	a.Close()
	b.Close()
	writer, err := TryLock(context.Background(), resource, "install")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := TryReadLock(context.Background(), resource, "build"); err == nil {
		t.Fatal("reader raced install")
	}
}

func TestFailedReplacementPreservesDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path+"-missing", path); err == nil {
		t.Fatal("expected replacement failure")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("old file lost: %q %v", data, err)
	}
}
