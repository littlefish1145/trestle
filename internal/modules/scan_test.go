package modules

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"trestle/internal/modules/p1689"
)

func TestReadHeaderDepsTracksRecursiveAndIncludeDirectoryHeaders(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src", "feature.cppm")
	local := filepath.Join(root, "src", "local.hpp")
	shared := filepath.Join(root, "include", "shared.hpp")
	for path, data := range map[string]string{
		source: "#include \"local.hpp\"\n",
		local:  "#include <shared.hpp>\n",
		shared: "// leaf\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := readHeaderDeps(source, []string{filepath.Join(root, "include")})
	want := []string{canonicalPath(local), canonicalPath(shared)}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("header dependencies = %#v, want %#v", got, want)
	}
}

func TestGeneratedHeaderAppearanceInvalidatesCache(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "unit.cppm")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("#include <generated.hpp>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	header := filepath.Join(second, "generated.hpp")
	if err := os.WriteFile(header, []byte("// original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps, missing, complete := discoverHeaderDeps(source, []string{first, second})
	if !complete || len(deps) != 1 || deps[0] != canonicalPath(header) || len(missing) != 1 || missing[0] != canonicalPath(filepath.Join(first, "generated.hpp")) {
		t.Fatalf("deps=%v missing=%v complete=%v", deps, missing, complete)
	}
	key := MakeScanKeyWithFingerprints(source, "compiler", "scanner", "c++20")
	entry := NewEntry(key, p1689.Document{Version: 1}, deps)
	entry.MissingDeps = missing
	if !entry.Valid(key) {
		t.Fatal("fresh entry invalid")
	}
	if err := os.WriteFile(filepath.Join(first, "generated.hpp"), []byte("// generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if entry.Valid(key) {
		t.Fatal("new higher priority header did not invalidate cache")
	}
}

func TestCacheUpdatePreservesConcurrentEntries(t *testing.T) {
	cache := NewCache(t.TempDir())
	const workers = 12
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			entry := NewEntry(ScanKey{SourceHash: fmt.Sprint(i)}, p1689.Document{Version: 1}, nil)
			if err := cache.Update(context.Background(), fmt.Sprint(i), entry); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	group.Wait()
	entries, err := cache.Load()
	if err != nil || len(entries) != workers {
		t.Fatalf("cache has %d entries, want %d: %v", len(entries), workers, err)
	}
}

func TestCacheUpdateHelperProcess(t *testing.T) {
	if os.Getenv("TRESTLE_SCAN_CACHE_HELPER") != "1" {
		return
	}
	cache := Cache{Path: os.Getenv("TRESTLE_SCAN_CACHE_PATH")}
	name := os.Getenv("TRESTLE_SCAN_CACHE_NAME")
	if err := cache.Update(context.Background(), name, NewEntry(ScanKey{SourceHash: name}, p1689.Document{Version: 1}, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestCacheUpdateAcrossProcesses(t *testing.T) {
	cache := NewCache(t.TempDir())
	const workers = 6
	commands := make([]*exec.Cmd, workers)
	outputs := make([]*bytes.Buffer, workers)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestCacheUpdateHelperProcess$")
		outputs[i] = &bytes.Buffer{}
		commands[i].Stdout, commands[i].Stderr = outputs[i], outputs[i]
		commands[i].Env = append(os.Environ(), "TRESTLE_SCAN_CACHE_HELPER=1", "TRESTLE_SCAN_CACHE_PATH="+cache.Path, "TRESTLE_SCAN_CACHE_NAME="+fmt.Sprint(i))
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("worker %d: %v: %s", i, err, outputs[i].String())
		}
	}
	entries, err := cache.Load()
	if err != nil || len(entries) != workers {
		t.Fatalf("cross process cache has %d entries, want %d: %v", len(entries), workers, err)
	}
}

func TestCommandScannerWithClang(t *testing.T) {
	compiler, compilerErr := exec.LookPath("clang++")
	scannerPath, scannerErr := exec.LookPath("clang-scan-deps")
	if compilerErr != nil || scannerErr != nil {
		t.Skip("clang++ and clang-scan-deps are required")
	}
	root := t.TempDir()
	source := filepath.Join(root, "math.cppm")
	header := filepath.Join(root, "value.hpp")
	if err := os.WriteFile(header, []byte("inline int value = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("module;\n#define HEADER \"value.hpp\"\n#include HEADER\nexport module math;\nexport int answer() { return value; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanner := CommandScanner{Compiler: compiler, Scanner: scannerPath, Standard: "c++20", Cache: NewCache(root), Directory: root}
	first, err := scanner.Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || len(first.Document.Rules) == 0 || len(first.Document.Rules[0].Provides) == 0 {
		t.Fatalf("unexpected initial scan: %#v", first)
	}
	if len(first.HeaderDeps) != 1 || first.HeaderDeps[0] != canonicalPath(header) || !first.Complete {
		t.Fatalf("macro header was not tracked: %#v", first)
	}
	second, err := scanner.Scan(context.Background(), source)
	if err != nil || !second.Reused {
		t.Fatalf("cache was not reused: %#v %v", second, err)
	}
	if err := os.WriteFile(header, []byte("inline int value = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := scanner.Scan(context.Background(), source)
	if err != nil || third.Reused {
		t.Fatalf("changed header was not rescanned: %#v %v", third, err)
	}
}

func TestCommandScannerDetectsNewGeneratedHeader(t *testing.T) {
	compiler, compilerErr := exec.LookPath("clang++")
	scannerPath, scannerErr := exec.LookPath("clang-scan-deps")
	if compilerErr != nil || scannerErr != nil {
		t.Skip("clang++ and clang-scan-deps are required")
	}
	root := t.TempDir()
	source := filepath.Join(root, "optional.cppm")
	header := filepath.Join(root, "generated.hpp")
	contents := "module;\n#if __has_include(\"generated.hpp\")\n#include \"generated.hpp\"\n#endif\nexport module optional;\n"
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	scanner := CommandScanner{Compiler: compiler, Scanner: scannerPath, Standard: "c++20", Cache: NewCache(root), Directory: root}
	if _, err := scanner.Scan(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if cached, err := scanner.Scan(context.Background(), source); err != nil || !cached.Reused {
		t.Fatalf("unchanged scan should be cached: %#v %v", cached, err)
	}
	if err := os.WriteFile(header, []byte("// generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rescanned, err := scanner.Scan(context.Background(), source)
	if err != nil || rescanned.Reused || len(rescanned.HeaderDeps) != 1 || rescanned.HeaderDeps[0] != canonicalPath(header) {
		t.Fatalf("generated header was not discovered: %#v %v", rescanned, err)
	}
}

func TestParseMakeDependenciesPreservesWindowsPathsAndSpaces(t *testing.T) {
	deps, ok := parseMakeDependencies("trestle: \\\r\n C:\\src\\math.cppm C:\\src\\generated\\my\\ header.hpp\r\n")
	if !ok || len(deps) != 2 || deps[0] != `C:\src\math.cppm` || deps[1] != `C:\src\generated\my header.hpp` {
		t.Fatalf("parsed dependencies = %#v, ok=%v", deps, ok)
	}
}

func TestCommandScannerWSLInvocation(t *testing.T) {
	scanner := CommandScanner{Runner: "wsl.exe", RunnerArgs: []string{"-d", "Ubuntu", "--cd", "/mnt/c/work/build/debug"}}
	exe, args := scanner.invocation("/usr/bin/clang-scan-deps", []string{"-format=p1689", "--", "/usr/bin/clang++", "-std=c++20", "-c", "/mnt/c/work/math.cppm"})
	want := []string{"-d", "Ubuntu", "--cd", "/mnt/c/work/build/debug", "--exec", "/usr/bin/clang-scan-deps", "-format=p1689", "--", "/usr/bin/clang++", "-std=c++20", "-c", "/mnt/c/work/math.cppm"}
	if exe != "wsl.exe" || !reflect.DeepEqual(args, want) {
		t.Fatalf("WSL invocation = %s %#v, want %#v", exe, args, want)
	}
}

func TestCacheEntryInvalidatesChangedHeader(t *testing.T) {
	root := t.TempDir()
	header := filepath.Join(root, "header.hpp")
	if err := os.WriteFile(header, []byte("int value = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := MakeScanKeyWithFingerprints("source.cppm", "compiler-v1", "scanner-v1", "c++20")
	entry := NewEntry(key, p1689.Document{Version: 1}, []string{header})
	if !entry.Valid(key) {
		t.Fatal("fresh cache entry should be valid")
	}
	if err := os.WriteFile(header, []byte("int value = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if entry.Valid(key) {
		t.Fatal("cache entry remained valid after a header changed")
	}
}

func TestCacheLoadRecoversFromCorruptCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "modules", "cache.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := (Cache{Path: path}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("recovered cache = %#v, want empty cache", entries)
	}
}

func TestCacheUpdateRepairsNullDocument(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(cache.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.Path, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cache.Update(context.Background(), "source", NewEntry(ScanKey{}, p1689.Document{Version: 1}, nil)); err != nil {
		t.Fatal(err)
	}
	entries, err := cache.Load()
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache repair = %#v: %v", entries, err)
	}
}
