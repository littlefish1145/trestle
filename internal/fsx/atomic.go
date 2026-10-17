// Package fsx provides the small filesystem helpers used across Trestle:
// atomic writes, content hashing, glob expansion, path canonicalization, and
// cross-process file locks that survive concurrent runs.
package fsx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path so readers observe either the old contents or
// the complete new contents, never a partial file. It skips the write when the
// contents already match and creates the parent directory when needed.
func AtomicWrite(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".trestle-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err = temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return replaceWithRetry(name, path)
}

// Hash returns a stable hex SHA-256 digest of the value's Go representation,
// used to derive object and action names that change when the input does.
func Hash(value any) string {
	data := []byte(fmt.Sprintf("%#v", value))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
