package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"trestle/internal/fsx"
	"trestle/internal/modules/p1689"
)

// ScanKey identifies a module scan by every input that can change its result:
// the source contents, the compiler and scanner fingerprints, and the compile
// flags.
type ScanKey struct {
	SourceHash          string `json:"source_hash"`
	CompilerFingerprint string `json:"compiler_fingerprint"`
	ScannerFingerprint  string `json:"scanner_fingerprint"`
	CompileSignature    string `json:"compile_signature"`
}

// FileStamp records the identity of a header dependency at scan time so a
// cached result can be invalidated if the file later changes.
type FileStamp struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	MTime int64  `json:"mtime"`
}

// CacheEntry is one cached module scan: the key it was produced under, the
// decoded p1689 document, and the header stamps needed to trust the result.
type CacheEntry struct {
	Key         ScanKey        `json:"key"`
	ResultHash  string         `json:"result_hash"`
	Result      p1689.Document `json:"result"`
	HeaderDeps  []FileStamp    `json:"header_deps"`
	MissingDeps []string       `json:"missing_deps,omitempty"`
	Complete    bool           `json:"complete"`
}

// Cache is a JSON-backed store of module scan results under a trestle root.
type Cache struct {
	Path string
}

// NewCache returns the cache that lives inside root's trestle state directory.
func NewCache(root string) Cache { return Cache{Path: filepath.Join(root, "modules", "cache.json")} }

// MakeScanKey builds a scan key from the source, compiler fingerprint, and
// compile signature, leaving the scanner fingerprint unset.
func MakeScanKey(source, compiler, signature string) ScanKey {
	return ScanKey{SourceHash: HashFile(source), CompilerFingerprint: compiler, CompileSignature: signature}
}

// MakeScanKeyWithFingerprints builds a scan key that also includes the
// scanner's own fingerprint, so swapping clang-scan-deps invalidates cached
// scans.
func MakeScanKeyWithFingerprints(source, compiler, scanner, signature string) ScanKey {
	return ScanKey{
		SourceHash:          HashFile(source),
		CompilerFingerprint: compiler,
		ScannerFingerprint:  scanner,
		CompileSignature:    signature,
	}
}

// HashFile returns the hex SHA-256 of a file's contents, or an empty string
// when the file cannot be read.
func HashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashDocument returns the hex SHA-256 of a p1689 document's JSON encoding,
// used to detect a corrupted cache entry.
func HashDocument(document p1689.Document) string {
	data, _ := json.Marshal(document)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Load reads the cache file, returning an empty cache when the file is absent
// or unreadable so a stale cache never blocks a scan.
func (cache Cache) Load() (map[string]CacheEntry, error) {
	data, err := os.ReadFile(cache.Path)
	if os.IsNotExist(err) {
		return map[string]CacheEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := map[string]CacheEntry{}
	if err := json.Unmarshal(data, &result); err != nil {
		// The cache is disposable. Recover from a stale or interrupted cache
		// rather than making an otherwise valid module scan fail permanently.
		return map[string]CacheEntry{}, nil
	}
	if result == nil {
		return map[string]CacheEntry{}, nil
	}
	return result, nil
}

// Save writes the cache atomically so an interrupted run cannot leave a
// truncated file behind.
func (cache Cache) Save(entries map[string]CacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(cache.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(cache.Path, append(data, '\n'))
}

// Update holds an OS file lock across the read/modify/write transaction, so
// separate trestle processes cannot overwrite one another's scan results.
func (cache Cache) Update(ctx context.Context, source string, entry CacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(cache.Path), 0o755); err != nil {
		return err
	}
	unlock, err := lockScanCache(ctx, cache.Path+".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := cache.Load()
	if err != nil {
		return err
	}
	entries[source] = entry
	return cache.Save(entries)
}

// Valid reports whether a cached entry still matches key and every tracked
// header and missing dependency is unchanged.
func (entry CacheEntry) Valid(key ScanKey) bool {
	if !entry.Complete || entry.Result.Version != 1 || entry.Key != key || entry.ResultHash != HashDocument(entry.Result) {
		return false
	}
	for _, dep := range entry.HeaderDeps {
		info, err := os.Stat(dep.Path)
		if err != nil || info.ModTime().UnixNano() != dep.MTime || HashFile(dep.Path) != dep.Hash {
			return false
		}
	}
	for _, path := range entry.MissingDeps {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// NewEntry captures the current identity of each header dependency and marks
// the entry incomplete when any dependency cannot be stamped.
func NewEntry(key ScanKey, result p1689.Document, headerDeps []string) CacheEntry {
	stamps := make([]FileStamp, 0, len(headerDeps))
	complete := true
	for _, path := range headerDeps {
		info, err := os.Stat(path)
		if err != nil {
			complete = false
			continue
		}
		hash := HashFile(path)
		if hash == "" {
			complete = false
			continue
		}
		stamps = append(stamps, FileStamp{Path: path, Hash: hash, MTime: info.ModTime().UnixNano()})
	}
	return CacheEntry{Key: key, ResultHash: HashDocument(result), Result: result, HeaderDeps: stamps, Complete: complete}
}
