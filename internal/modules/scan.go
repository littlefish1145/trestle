package modules

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trestle/internal/modules/p1689"
)

type CommandScanner struct {
	Compiler string
	Scanner  string
	Standard string
	Options  []string
	Cache    Cache
}

type ScanResult struct {
	Document p1689.Document
	Reused   bool
	Key      ScanKey
}

func (scanner CommandScanner) Scan(ctx context.Context, source string) (ScanResult, error) {
	if scanner.Compiler == "" || scanner.Scanner == "" {
		return ScanResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: compiler and clang-scan-deps are required")
	}
	key := MakeScanKey(source, scanner.Compiler, scanner.Standard+"\x00"+strings.Join(scanner.Options, "\x00"))
	entries, err := scanner.Cache.Load()
	if err != nil {
		return ScanResult{}, err
	}
	cacheKey := source
	if entry, ok := entries[cacheKey]; ok && entry.Valid(key) {
		return ScanResult{Document: entry.Result, Reused: true, Key: key}, nil
	}
	args := []string{"-format=p1689", "--", scanner.Compiler, "-std=" + strings.TrimPrefix(scanner.Standard, "c++"), "-c", source}
	args = append(args, scanner.Options...)
	command := exec.CommandContext(ctx, scanner.Scanner, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return ScanResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %s", source, strings.TrimSpace(stderr.String()))
	}
	document, err := p1689.Decode(bytes.NewReader(output))
	if err != nil {
		return ScanResult{}, err
	}
	headerDeps := readHeaderDeps(source)
	entries[cacheKey] = NewEntry(key, document, headerDeps)
	if err := scanner.Cache.Save(entries); err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Document: document, Key: key}, nil
}

func readHeaderDeps(source string) []string {
	data, err := os.ReadFile(source)
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#include \"") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "#include \""), "\"")
			path := filepath.Join(filepath.Dir(source), name)
			if _, err := os.Stat(path); err == nil {
				result = append(result, path)
			}
		}
	}
	return result
}
