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

type ScanKey struct {
	SourceHash          string `json:"source_hash"`
	CompilerFingerprint string `json:"compiler_fingerprint"`
	ScannerFingerprint  string `json:"scanner_fingerprint"`
	CompileSignature    string `json:"compile_signature"`
}

type FileStamp struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	MTime int64  `json:"mtime"`
}

type CacheEntry struct {
	Key         ScanKey        `json:"key"`
	ResultHash  string         `json:"result_hash"`
	Result      p1689.Document `json:"result"`
	HeaderDeps  []FileStamp    `json:"header_deps"`
	MissingDeps []string       `json:"missing_deps,omitempty"`
	Complete    bool           `json:"complete"`
}

type Cache struct {
	Path string
}

func NewCache(root string) Cache { return Cache{Path: filepath.Join(root, "modules", "cache.json")} }

func MakeScanKey(source, compiler, signature string) ScanKey {
	return ScanKey{SourceHash: HashFile(source), CompilerFingerprint: compiler, CompileSignature: signature}
}

func MakeScanKeyWithFingerprints(source, compiler, scanner, signature string) ScanKey {
	return ScanKey{
		SourceHash:          HashFile(source),
		CompilerFingerprint: compiler,
		ScannerFingerprint:  scanner,
		CompileSignature:    signature,
	}
}

func HashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func HashDocument(document p1689.Document) string {
	data, _ := json.Marshal(document)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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
