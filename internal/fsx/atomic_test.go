package fsx

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent replacement of one destination is normal in trestle: every process
// taking a lock writes its owner metadata to the same file while contenders read
// it to report who holds the resource. On Windows those short-lived read handles
// make a plain rename fail with a sharing violation, which used to surface as a
// spurious "Access is denied" during concurrent module cache updates.
func TestAtomicWriteToleratesConcurrentReplacement(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		path := filepath.Join(t.TempDir(), "owner.json")
		if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		var mu sync.Mutex
		failures := map[string]int{}
		for i := 0; i < 12; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				if err := AtomicWrite(path, []byte(`{"pid":1}`)); err != nil {
					mu.Lock()
					failures[err.Error()]++
					mu.Unlock()
				}
			}()
		}
		group.Wait()
		if len(failures) > 0 {
			t.Fatalf("attempt %d: concurrent AtomicWrite failed: %v", attempt, failures)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"pid":1}` {
			t.Fatalf("destination holds %q", data)
		}
	}
}

// A reader holding the destination open must not stop a later write, which is
// exactly the lock-metadata case: the reader is the contending process asking
// who owns the resource.
func TestAtomicWriteWhileDestinationIsRead(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		path := filepath.Join(t.TempDir(), "owner.json")
		if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			// Hold a read handle open while the writer replaces the file.
			for i := 0; i < 50; i++ {
				file, err := os.Open(path)
				if err == nil {
					_, _ = file.Read(make([]byte, 8))
					_ = file.Close()
				}
			}
		}()
		go func() {
			defer group.Done()
			for i := 0; i < 50; i++ {
				if err := AtomicWrite(path, []byte("second")); err != nil {
					t.Errorf("write during concurrent reads: %v", err)
					return
				}
			}
		}()
		group.Wait()
	}
}
