package modules

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trestle/internal/modules/p1689"
)

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

type ScanResult struct {
	Document   p1689.Document
	Reused     bool
	Key        ScanKey
	HeaderDeps []string
	Complete   bool
}

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
	cacheKey := sourcePath
	if entry, ok := entries[cacheKey]; ok && entry.Valid(key) {
		deps := make([]string, 0, len(entry.HeaderDeps))
		for _, stamp := range entry.HeaderDeps {
			deps = append(deps, stamp.Path)
		}
		return ScanResult{Document: entry.Result, Reused: true, Key: key, HeaderDeps: deps, Complete: true}, nil
	}
	commandSource := sourcePath
	if scanner.PathMapper != nil {
		commandSource, err = scanner.PathMapper(ctx, sourcePath)
		if err != nil {
			return ScanResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: map %s into toolchain: %w", source, err)
		}
	}
	args := []string{"-format=p1689", "--", scanner.Compiler, "-std=" + scanner.Standard, "-c", commandSource}
	args = append(args, scanner.Options...)
	executable, args := scanner.invocation(scanner.Scanner, args)
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = scanner.Directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return ScanResult{}, fmt.Errorf("E_MODULE_SCAN_FAILED: %s: %w: %s", source, err, strings.TrimSpace(stderr.String()))
	}
	document, err := p1689.Decode(bytes.NewReader(output))
	if err != nil {
		return ScanResult{}, err
	}
	localOptions := scanner.Options
	if scanner.LocalOptions != nil {
		localOptions = scanner.LocalOptions
	}
	headerDirs := append(includeDirs(localOptions, scanner.Directory), scanner.HeaderDirs...)
	headerDeps, missingDeps, complete := discoverHeaderDeps(sourcePath, headerDirs)
	compilerDeps, probeOK, probeComplete := scanner.compilerHeaderDeps(ctx, commandSource, sourcePath)
	if probeOK {
		seen := make(map[string]bool, len(headerDeps)+len(compilerDeps))
		for _, path := range append(headerDeps, compilerDeps...) {
			seen[path] = true
		}
		headerDeps = headerDeps[:0]
		for path := range seen {
			headerDeps = append(headerDeps, path)
		}
		sort.Strings(headerDeps)
		complete = probeComplete
	}
	if !probeOK {
		complete = false // unknown macro/system include resolution
	}
	if HashFile(sourcePath) != key.SourceHash {
		complete = false
	}
	if strings.Contains(compilerFingerprint, "|probe-failed") || strings.Contains(scannerFingerprint, "|probe-failed") {
		complete = false
	}
	entry := NewEntry(key, document, headerDeps)
	entry.MissingDeps = missingDeps
	entry.Complete = entry.Complete && complete
	if err := scanner.Cache.Update(ctx, cacheKey, entry); err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Document: document, Key: key, HeaderDeps: headerDeps, Complete: entry.Complete}, nil
}

func (scanner CommandScanner) executableFingerprint(ctx context.Context, executable string) string {
	if scanner.Runner != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		runner, args := scanner.invocation(executable, []string{"--version"})
		version, err := exec.CommandContext(probeCtx, runner, args...).CombinedOutput()
		if err != nil {
			return scanner.Runner + "|" + strings.Join(scanner.RunnerArgs, "\x00") + "|" + executable + "|probe-failed"
		}
		sum := sha256.Sum256(version)
		return scanner.Runner + "|" + strings.Join(scanner.RunnerArgs, "\x00") + "|" + executable + "|" + hex.EncodeToString(sum[:])
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return executable
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return resolved
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, _ := exec.CommandContext(probeCtx, resolved, "--version").CombinedOutput()
	sum := sha256.Sum256(version)
	return fmt.Sprintf("%s|%d|%d|%s", resolved, info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(sum[:]))
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
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		value = canonicalPath(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	for index := 0; index < len(options); index++ {
		option := options[index]
		switch {
		case option == "-I" || option == "/I" || option == "-isystem":
			if index+1 < len(options) {
				index++
				add(options[index])
			}
		case strings.HasPrefix(option, "-I"):
			add(strings.TrimPrefix(option, "-I"))
		case strings.HasPrefix(option, "/I"):
			add(strings.TrimPrefix(option, "/I"))
		case strings.HasPrefix(option, "-isystem"):
			add(strings.TrimPrefix(option, "-isystem"))
		}
	}
	return result
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
			candidates := make([]string, 0, len(directories)+1)
			if !angled {
				candidates = append(candidates, filepath.Join(filepath.Dir(path), name))
			}
			for _, directory := range directories {
				candidates = append(candidates, filepath.Join(directory, name))
			}
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
				if !visited[candidate] {
					queue = append(queue, candidate)
				}
				break
			}
		}
	}
	result := make([]string, 0, len(deps))
	for path := range deps {
		result = append(result, path)
	}
	sort.Strings(result)
	missingResult := make([]string, 0, len(missing))
	for path := range missing {
		missingResult = append(missingResult, path)
	}
	sort.Strings(missingResult)
	return result, missingResult, complete
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
