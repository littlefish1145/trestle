package vcpkg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"trestle/internal/processx"
)

func vcpkgExecutableName() string {
	if runtime.GOOS == "windows" {
		return "vcpkg.exe"
	}
	return "vcpkg"
}

// NewClient resolves a root from the configured value, VCPKG_ROOT, or PATH,
// and never invents an executable that is not there.
func TestNewClientResolvesConfiguredRoot(t *testing.T) {
	for _, name := range []string{"vcpkg", "vcpkg.exe"} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(root)
		if err != nil {
			t.Fatalf("NewClient(%s root): %v", name, err)
		}
		if client.Root != root || client.Executable != filepath.Join(root, name) {
			t.Fatalf("client = %+v, want root %s executable %s", client, root, name)
		}
	}
}

func TestNewClientUsesEnvironmentRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "vcpkg"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VCPKG_ROOT", root)
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient with VCPKG_ROOT: %v", err)
	}
	if client.Root != root {
		t.Fatalf("Root = %q, want %q", client.Root, root)
	}
}

func TestNewClientFindsExecutableOnPath(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, vcpkgExecutableName())
	if err := os.WriteFile(executable, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VCPKG_ROOT", "")
	t.Setenv("PATH", directory)
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient from PATH: %v", err)
	}
	if client.Root != directory {
		t.Fatalf("Root = %q, want %q", client.Root, directory)
	}
}

func TestNewClientReportsMissingRoot(t *testing.T) {
	t.Setenv("VCPKG_ROOT", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := NewClient(""); err == nil || !strings.Contains(err.Error(), "E_VCPKG_ROOT") {
		t.Fatalf("err = %v, want E_VCPKG_ROOT", err)
	}
}

func TestNewClientReportsMissingExecutable(t *testing.T) {
	if _, err := NewClient(t.TempDir()); err == nil || !strings.Contains(err.Error(), "E_VCPKG_EXECUTABLE") {
		t.Fatalf("err = %v, want E_VCPKG_EXECUTABLE", err)
	}
}

func TestEnvironmentWithoutOverridesStaysNil(t *testing.T) {
	if got := (Client{}).environment(); got != nil {
		t.Fatalf("environment() = %#v, want nil", got)
	}
}

func TestTailKeepsTheLastLines(t *testing.T) {
	if got := tail("a\nb\nc\nd\ne", 4); got != "b\nc\nd\ne" {
		t.Fatalf("tail = %q", got)
	}
	if got := tail("only", 4); got != "only" {
		t.Fatalf("short tail = %q", got)
	}
	if got := tail("  spaced  ", 4); got != "spaced" {
		t.Fatalf("trimmed tail = %q", got)
	}
}

func TestIsInstalledFindsShareDirectory(t *testing.T) {
	root := t.TempDir()
	share := filepath.Join(root, "installed", "x64-windows", "share")
	if err := os.MkdirAll(filepath.Join(share, "glfw3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "notaport"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	if !client.IsInstalled("glfw3") {
		t.Fatal("a share directory marks the port installed")
	}
	if client.IsInstalled("notaport") {
		t.Fatal("a share file is not an installation")
	}
}

func TestEnrichPortReadsManifestMetadata(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "ports", "demo")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "demo",
		"description": ["line one", "line two"],
		"homepage": "https://demo.example",
		"license": "MIT",
		"features": {"unicode": {}, "fast": {}},
		"dependencies": [{"name": "zlib"}, "fmt", {"Name": "extra"}, 17]
	}`
	if err := os.WriteFile(filepath.Join(directory, "vcpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	port := Port{Name: "demo"}
	client.enrichPort(&port)
	if port.PackageURL != "https://vcpkg.io/en/package/demo.html" {
		t.Fatalf("PackageURL = %q", port.PackageURL)
	}
	if port.Description != "line one line two" || port.Homepage != "https://demo.example" || port.License != "MIT" {
		t.Fatalf("metadata not decoded: %+v", port)
	}
	if strings.Join(port.Features, ",") != "fast,unicode" {
		t.Fatalf("features = %v", port.Features)
	}
	if strings.Join(port.Dependencies, ",") != "zlib,fmt,extra" {
		t.Fatalf("dependencies = %v", port.Dependencies)
	}
}

func TestEnrichPortToleratesBadManifest(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "ports", "demo")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "vcpkg.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	port := Port{Name: "demo"}
	client.enrichPort(&port)
	if port.PackageURL == "" || port.Description != "" {
		t.Fatalf("port = %+v", port)
	}
}

// Search runs the vcpkg executable and keeps only port rows, not the feature
// continuation rows vcpkg prints beneath them.
func TestSearchDropsFeatureContinuationRows(t *testing.T) {
	client := newFakeClient(t, "search")
	lines, err := client.Search(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	want := []string{
		"fmt 11.2.0#1 {fmt} is an open-source formatting library",
		"spdlog 1.17.0 Fast C++ logging library",
		"zlib 1.3.1 A compression library",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines = %#v, want %#v", lines, want)
	}
}

func TestSearchReportsProcessFailure(t *testing.T) {
	client := newFakeClient(t, "search")
	_, err := client.Search(context.Background(), "boom")
	if err == nil || !strings.Contains(err.Error(), "vcpkg search failed") || !strings.Contains(err.Error(), "exploded") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchPortsMergesManifestMetadata(t *testing.T) {
	client := newFakeClient(t, "search")
	directory := filepath.Join(client.Root, "ports", "fmt")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"description":"formatting library","homepage":"https://fmt.dev","license":"MIT","features":{"unicode":{}},"dependencies":["dep"]}`
	if err := os.WriteFile(filepath.Join(directory, "vcpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ports, err := client.SearchPorts(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("search ports: %v", err)
	}
	if len(ports) != 3 {
		t.Fatalf("ports = %+v", ports)
	}
	fmtPort := ports[0]
	if fmtPort.Name != "fmt" || fmtPort.Version != "11.2.0#1" || fmtPort.Source != "local" {
		t.Fatalf("fmt row = %+v", fmtPort)
	}
	if fmtPort.Description != "formatting library" || fmtPort.Homepage != "https://fmt.dev" || fmtPort.License != "MIT" {
		t.Fatalf("fmt metadata = %+v", fmtPort)
	}
	if len(fmtPort.Features) != 1 || fmtPort.Features[0] != "unicode" || len(fmtPort.Dependencies) != 1 {
		t.Fatalf("fmt features/dependencies = %+v", fmtPort)
	}
	if ports[2].Description != "A compression library" {
		t.Fatalf("zlib row = %+v", ports[2])
	}
}

func TestSearchPortsSkipsNonPortLines(t *testing.T) {
	client := newFakeClient(t, "search")
	ports, err := client.SearchPorts(context.Background(), "truthy")
	if err != nil {
		t.Fatalf("search ports: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("ports = %+v, want none", ports)
	}
}

func TestInstallStreamsQuietRun(t *testing.T) {
	client := newFakeClient(t, "install")
	if err := client.Install(context.Background(), []string{"quiet"}, ""); err != nil {
		t.Fatalf("install: %v", err)
	}
}

// InstallCapture must run the executable with the client environment applied,
// and pass --triplet only for a concrete triplet.
func TestInstallCaptureAppliesEnvironmentAndTriplet(t *testing.T) {
	client := newFakeClient(t, "install")
	client.Environment = map[string]string{"TRESTLE_TEST_VCPKG_FAKE_CANARY": "yes"}
	output, err := client.InstallCapture(context.Background(), []string{"fmt"}, "x64-windows")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, want := range []string{"canary=yes", "install fmt", "--triplet x64-windows"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
	}
	output, err = client.InstallCapture(context.Background(), []string{"fmt"}, "auto")
	if err != nil {
		t.Fatalf("install auto: %v", err)
	}
	if strings.Contains(output, "--triplet") {
		t.Fatalf("auto triplet must not be forwarded: %q", output)
	}
}

func TestInstallCaptureReportsFailureDetail(t *testing.T) {
	client := newFakeClient(t, "install")
	output, err := client.InstallCapture(context.Background(), []string{"fail-loud"}, "")
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_INSTALL") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(output, "compile error") {
		t.Fatalf("output = %q", output)
	}
}

func TestInstallCaptureReportsSilentFailure(t *testing.T) {
	client := newFakeClient(t, "install")
	output, err := client.InstallCapture(context.Background(), []string{"fail-silent"}, "")
	if err == nil || !strings.Contains(err.Error(), "vcpkg install failed") {
		t.Fatalf("err = %v", err)
	}
	if output != "" {
		t.Fatalf("output = %q, want empty", output)
	}
}

// InstallWithProgress emits every complete line of both output streams.
func TestInstallWithProgressEmitsCompleteLines(t *testing.T) {
	client := newFakeClient(t, "install")
	var lock sync.Mutex
	var lines []string
	err := client.InstallWithProgress(context.Background(), []string{"fmt"}, "x64-windows", func(line string) {
		lock.Lock()
		lines = append(lines, line)
		lock.Unlock()
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	lock.Lock()
	defer lock.Unlock()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Computing installation plan...", "args=install fmt --triplet x64-windows", "warning: fake vcpkg warning"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress lines %q missing %q", joined, want)
		}
	}
}

// A context carrying an output writer receives every streamed byte, which is
// how the TUI mirrors install output to its own view.
func TestInstallWithProgressTeesToContextOutput(t *testing.T) {
	client := newFakeClient(t, "install")
	var buffer strings.Builder
	ctx := processx.WithOutput(context.Background(), &buffer, nil)
	if err := client.InstallWithProgress(ctx, []string{"fmt"}, "", nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, want := range []string{"Computing installation plan...", "warning: fake vcpkg warning"} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("tee %q missing %q", buffer.String(), want)
		}
	}
}

// Cancelling the context kills the running vcpkg and surfaces the cancellation
// instead of the exit code taskkill produces.
func TestInstallWithProgressReturnsContextCancellation(t *testing.T) {
	client := newFakeClient(t, "install")
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("TRESTLE_TEST_VCPKG_FAKE_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.InstallWithProgress(ctx, []string{"block"}, "", nil)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake vcpkg never started")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("install did not stop after cancellation")
	}
}

func TestInstallWithProgressReportsStartFailure(t *testing.T) {
	client := Client{Root: t.TempDir(), Executable: filepath.Join(t.TempDir(), "missing-vcpkg")}
	err := client.InstallWithProgress(context.Background(), nil, "", nil)
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_INSTALL") {
		t.Fatalf("err = %v", err)
	}
}
