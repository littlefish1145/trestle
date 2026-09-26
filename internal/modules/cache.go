package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"trestle/internal/modules/p1689"
)

type ScanKey struct {
	SourceHash          string `json:"source_hash"`
	CompilerFingerprint string `json:"compiler_fingerprint"`
	CompileSignature    string `json:"compile_signature"`
}

type FileStamp struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	MTime int64  `json:"mtime"`
}

type CacheEntry struct {
	Key        ScanKey        `json:"key"`
	ResultHash string         `json:"result_hash"`
	Result     p1689.Document `json:"result"`
	HeaderDeps []FileStamp    `json:"header_deps"`
}

type Cache struct {
	Path string
}

func NewCache(root string) Cache { return Cache{Path: filepath.Join(root, "modules", "cache.json")} }

func MakeScanKey(source, compiler, signature string) ScanKey {
	return ScanKey{SourceHash: HashFile(source), CompilerFingerprint: compiler, CompileSignature: signature}
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
		return nil, err
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
	return os.WriteFile(cache.Path, data, 0o644)
}

func (entry CacheEntry) Valid(key ScanKey) bool {
	if entry.Key != key || entry.ResultHash != HashDocument(entry.Result) {
		return false
	}
	for _, dep := range entry.HeaderDeps {
		info, err := os.Stat(dep.Path)
		if err != nil || info.ModTime().UnixNano() != dep.MTime || HashFile(dep.Path) != dep.Hash {
			return false
		}
	}
	return true
}

func NewEntry(key ScanKey, result p1689.Document, headerDeps []string) CacheEntry {
	stamps := make([]FileStamp, 0, len(headerDeps))
	for _, path := range headerDeps {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stamps = append(stamps, FileStamp{Path: path, Hash: HashFile(path), MTime: info.ModTime().UnixNano()})
	}
	return CacheEntry{Key: key, ResultHash: HashDocument(result), Result: result, HeaderDeps: stamps}
}
