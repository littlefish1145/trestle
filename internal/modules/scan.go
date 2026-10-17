package modules

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"trestle/internal/processx"

	"trestle/internal/modules/p1689"
)

// CommandScanner scans module dependencies by invoking clang-scan-deps
// (possibly through a runner such as WSL), with a cache and optional path
// mappings that keep results portable.
type CommandScanner struct {
	Compiler         string
	Scanner          string
	Standard         string
	Options          []string
	Cache            Cache
	Directory        string
	Runner           string
	RunnerArgs       []string
	PathMapper       func(context.Context, string) (string, error)
	HeaderDirs       []string
	LocalOptions     []string
	DependencyMapper func(context.Context, string) (string, error)
}

// ScanResult is the outcome of a scan: the decoded document, the key it was
// stored under, the discovered header dependencies, and whether the result is
// complete enough to trust.
type ScanResult struct {
	Document   p1689.Document
	Reused     bool
	Key        ScanKey
	HeaderDeps []string
	Complete   bool
}

// Scan returns the module dependencies for source, reusing a valid cache entry
// when one exists and otherwise invoking the scanner and refreshing the cache.
func (scanner CommandScanner) Scan(ctx context.Context, source string) (ScanResult, error) {
	if scanner.Compiler == "" || scanner.Scanner == "" {
		return ScanResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: compiler and clang-scan-deps are required")
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	sourcePath := canonicalPath(source)
	compilerFingerprint := scanner.executableFingerprint(ctx, scanner.Compiler)
	scannerFingerprint := scanner.executableFingerprint(ctx, scanner.Scanner)
	key := MakeScanKeyWithFingerprints(sourcePath, compilerFingerprint, scannerFingerprint, scanner.Standard+"\x00"+strings.Join(scanner.Options, "\x00"))
	entries, err := scanner.Cache.Load()
	if err != nil {
		return ScanResult{}, err
	}
	if result, ok := cachedScanResult(entries, sourcePath, key); ok {
		return result, nil
	}
	document, headerDeps, missingDeps, complete, err := scanner.runScan(ctx, source, sourcePath, key, compilerFingerprint, scannerFingerprint)
	if err != nil {
		return ScanResult{}, err
	}
	entry := NewEntry(key, document, headerDeps)
	entry.MissingDeps = missingDeps
	entry.Complete = entry.Complete && complete
	if err := scanner.Cache.Update(ctx, sourcePath, entry); err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Document: document, Key: key, HeaderDeps: headerDeps, Complete: entry.Complete}, nil
}

// cachedScanResult rebuilds the reused result stored under cacheKey when the
// cached entry is still valid for key. The boolean is false on a cache miss or
// a stale entry, so the caller falls back to a fresh scan.
func cachedScanResult(entries map[string]CacheEntry, cacheKey string, key ScanKey) (ScanResult, bool) {
	entry, ok := entries[cacheKey]
	if !ok || !entry.Valid(key) {
		return ScanResult{}, false
	}
	deps := make([]string, 0, len(entry.HeaderDeps))
	for _, stamp := range entry.HeaderDeps {
		deps = append(deps, stamp.Path)
	}
	return ScanResult{Document: entry.Result, Reused: true, Key: key, HeaderDeps: deps, Complete: true}, true
}

// runScan invokes the scanner for a cache miss and assembles the source's
// header dependencies, consulting the compiler's own preprocessor probe and
// reporting whether the result is complete enough to cache.
func (scanner CommandScanner) runScan(ctx context.Context, source, sourcePath string, key ScanKey, compilerFingerprint, scannerFingerprint string) (p1689.Document, []string, []string, bool, error) {
	commandSource := sourcePath
	if scanner.PathMapper != nil {
		mapped, err := scanner.PathMapper(ctx, sourcePath)
		if err != nil {
			return p1689.Document{}, nil, nil, false, fmt.Errorf("E_MODULE_SCAN_FAILED: map %s into toolchain: %w", source, err)
		}
		commandSource = mapped
	}
	output, err := scanner.runScanner(ctx, source, commandSource)
	if err != nil {
		return p1689.Document{}, nil, nil, false, err
	}
	document, err := p1689.Decode(bytes.NewReader(output))
	if err != nil {
		return p1689.Document{}, nil, nil, false, err
	}
	localOptions := scanner.Options
	if scanner.LocalOptions != nil {
		localOptions = scanner.LocalOptions
	}
	headerDirs := append(includeDirs(localOptions, scanner.Directory), scanner.HeaderDirs...)
	headerDeps, missingDeps, complete := discoverHeaderDeps(sourcePath, headerDirs)
	compilerDeps, probeOK, probeComplete := scanner.compilerHeaderDeps(ctx, commandSource, sourcePath)
	if probeOK {
		headerDeps = mergeHeaderDeps(headerDeps, compilerDeps)
		complete = probeComplete
	} else {
		complete = false // unknown macro/system include resolution
	}
	if HashFile(sourcePath) != key.SourceHash {
		complete = false
	}
	if strings.Contains(compilerFingerprint, "|probe-failed") || strings.Contains(scannerFingerprint, "|probe-failed") {
		complete = false
	}
	return document, headerDeps, missingDeps, complete, nil
}

// runScanner executes clang-scan-deps for a source and returns its raw p1689
// output. A failed run is wrapped with the source and the captured stderr.
func (scanner CommandScanner) runScanner(ctx context.Context, source, commandSource string) ([]byte, error) {
	args := []string{"-format=p1689", "--", scanner.Compiler, "-std=" + scanner.Standard, "-c", commandSource}
	args = append(args, scanner.Options...)
	executable, args := scanner.invocation(scanner.Scanner, args)
	command := processx.Command(ctx, executable, args...)
	command.Dir = scanner.Directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %w: %s", source, err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

// mergeHeaderDeps returns the sorted union of two header dependency lists,
// deduplicated by path.
func mergeHeaderDeps(primary, additional []string) []string {
	seen := make(map[string]bool, len(primary)+len(additional))
	for _, path := range primary {
		seen[path] = true
	}
	for _, path := range additional {
		seen[path] = true
	}
	result := make([]string, 0, len(seen))
	for path := range seen {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (scanner CommandScanner) executableFingerprint(ctx context.Context, executable string) string {
	if scanner.Runner != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		runner, args := scanner.invocation(executable, []string{"--version"})
		version, err := processx.Command(probeCtx, runner, args...).CombinedOutput()
		if err != nil {
			return scanner.Runner + "|" + strings.Join(scanner.RunnerArgs, "\x00") + "|" + executable + "|probe-failed"
		}
		sum := sha256.Sum256(version)
		return scanner.Runner + "|" + strings.Join(scanner.RunnerArgs, "\x00") + "|" + executable + "|" + hex.EncodeToString(sum[:])
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return executable + "|probe-failed"
	}
	file, err := os.Open(resolved)
	if err != nil {
		return resolved + "|probe-failed"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return resolved + "|probe-failed"
	}
	// Native identity comes from the executable itself. Version subprocesses
	// may time out on busy runners and yield a different fingerprint even
	// though the tool has not changed.
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return resolved + "|probe-failed"
	}
	return fmt.Sprintf("%s|%d|%d|%s", resolved, info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(hash.Sum(nil)))
}

func (scanner CommandScanner) invocation(executable string, args []string) (string, []string) {
	if scanner.Runner == "" {
		return executable, args
	}
	wrapped := append(append(append([]string{}, scanner.RunnerArgs...), "--exec", executable), args...)
	return scanner.Runner, wrapped
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func includeDirs(options []string, base string) []string {
	var result []string
	seen := map[string]bool{}
	for index := 0; index < len(options); index++ {
		value, consumed, ok := includeOptionValue(options[index], options, index)
		if !ok {
			continue
		}
		if consumed {
			index++
		}
		value = normalizeIncludeDir(value, base)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// includeOptionValue extracts the directory named by one include option. It
// reports whether the option consumed the following argument and whether the
// option was an include directive at all.
func includeOptionValue(option string, options []string, index int) (value string, consumed, ok bool) {
	switch {
	case option == "-I" || option == "/I" || option == "-isystem":
		if index+1 < len(options) {
			return options[index+1], true, true
		}
		return "", false, true
	case strings.HasPrefix(option, "-I"):
		return strings.TrimPrefix(option, "-I"), false, true
	case strings.HasPrefix(option, "/I"):
		return strings.TrimPrefix(option, "/I"), false, true
	case strings.HasPrefix(option, "-isystem"):
		return strings.TrimPrefix(option, "-isystem"), false, true
	}
	return "", false, false
}

// normalizeIncludeDir resolves a compiler include-directory argument against
// base and returns its canonical path, or an empty string when the argument is
// blank.
func normalizeIncludeDir(value, base string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return canonicalPath(value)
}

func readHeaderDeps(source string, directories []string) []string {
	deps, _, _ := discoverHeaderDeps(source, directories)
	return deps
}

func discoverHeaderDeps(source string, directories []string) ([]string, []string, bool) {
	source = canonicalPath(source)
	queue := []string{source}
	visited := map[string]bool{}
	deps := map[string]bool{}
	missing := map[string]bool{}
	complete := true
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if visited[path] {
			continue
		}
		visited[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			name, angled, ok := parseInclude(line)
			if !ok {
				if strings.HasPrefix(strings.TrimSpace(line), "#include") {
					complete = false // macro or otherwise unresolved include
				}
				continue
			}
			candidates := includeCandidates(path, name, angled, directories)
			candidate, resolved := resolveInclude(candidates, source, deps, missing, complete)
			complete = resolved
			if candidate != "" && !visited[candidate] {
				queue = append(queue, candidate)
			}
		}
	}
	return sortedKeys(deps), sortedKeys(missing), complete
}

// includeCandidates lists the paths an include directive may resolve to: the
// including file's own directory for quoted includes, followed by each search
// directory in order.
func includeCandidates(includer, name string, angled bool, directories []string) []string {
	candidates := make([]string, 0, len(directories)+1)
	if !angled {
		candidates = append(candidates, filepath.Join(filepath.Dir(includer), name))
	}
	for _, directory := range directories {
		candidates = append(candidates, filepath.Join(directory, name))
	}
	return candidates
}

// resolveInclude finds the first existing candidate for an include directive,
// recording candidates that do not exist and clearing complete when a candidate
// exists but cannot be read as a file. It returns the resolved path, or an
// empty string when no candidate could be used.
func resolveInclude(candidates []string, source string, deps, missing map[string]bool, complete bool) (string, bool) {
	for _, candidate := range candidates {
		candidate = canonicalPath(candidate)
		info, statErr := os.Stat(candidate)
		if os.IsNotExist(statErr) {
			missing[candidate] = true
			continue
		}
		if statErr != nil || info.IsDir() {
			complete = false
			continue
		}
		if candidate != source {
			deps[candidate] = true
		}
		return candidate, complete
	}
	return "", complete
}

// sortedKeys returns the keys of a set in sorted order.
func sortedKeys(set map[string]bool) []string {
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func parseInclude(line string) (name string, angled, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return "", false, false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
	if strings.HasPrefix(line, "include_next") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "include_next"))
	} else if strings.HasPrefix(line, "include") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "include"))
	} else {
		return "", false, false
	}
	if len(line) < 2 {
		return "", false, false
	}
	if line[0] == '"' {
		end := strings.IndexByte(line[1:], '"')
		if end < 0 {
			return "", false, false
		}
		return strings.TrimSpace(line[1 : end+1]), false, true
	}
	if line[0] == '<' {
		end := strings.IndexByte(line[1:], '>')
		if end < 0 {
			return "", false, false
		}
		return strings.TrimSpace(line[1 : end+1]), true, true
	}
	return "", false, false
}
