package conan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"trestle/internal/config"
)

// TestParseListOutputReadsRemoteKeyedResults locks the shape conan actually
// emits for a remote search: the remote name is a TOP-LEVEL key, each recipe
// nests the full reference above its revisions, and a recipe with no revision
// detail is an empty object. `timestamp` is epoch-seconds float, not a string.
func TestParseListOutputReadsRemoteKeyedResults(t *testing.T) {
	data := []byte(`{
	  "conancenter": {
	    "fmt/10.2.1": {},
	    "fmt/11.1.4": {},
	    "zlib/1.3.1": {"revisions": {"d77ee68739fcbe5bf37b8a4690eea6ea": {"timestamp": 1659719850.773}}}
	  }
	}`)
	packages, err := parseListOutput(data, "conancenter")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 {
		t.Fatalf("expected 3 packages, got %d: %+v", len(packages), packages)
	}
	for _, entry := range packages {
		if entry.Source != "conancenter" {
			t.Fatalf("unexpected source: %+v", entry)
		}
		if entry.Installed {
			t.Fatalf("a remote recipe must not be installed: %+v", entry)
		}
		if entry.PackageURL != "https://conan.io/center/"+entry.Name {
			t.Fatalf("unexpected package URL: %+v", entry)
		}
	}
	// Names sort alphabetically, versions newest-first within a name.
	if packages[0].Name != "fmt" || packages[0].Version != "11.1.4" || packages[1].Version != "10.2.1" {
		t.Fatalf("fmt versions not ordered newest-first: %+v", packages[:2])
	}
	zlib := packages[2]
	if zlib.Name != "zlib" || zlib.Version != "1.3.1" {
		t.Fatalf("reference not split: %+v", zlib)
	}
	if zlib.Updated != "2022-08-05" {
		t.Fatalf("epoch-seconds timestamp not rendered as a date: %q", zlib.Updated)
	}
}

func TestParseListOutputReadsLocalCache(t *testing.T) {
	data := []byte(`{"Local Cache": {"zlib/1.3.1": {}}}`)
	packages, err := parseListOutput(data, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("expected 1 package, got %d: %+v", len(packages), packages)
	}
	entry := packages[0]
	if entry.Source != "local" || !entry.Installed {
		t.Fatalf("a cache recipe must be marked installed: %+v", entry)
	}
	if entry.Name != "zlib" || entry.Version != "1.3.1" {
		t.Fatalf("reference not split: %+v", entry)
	}
}

func TestParseListOutputSkipsLeadingConsoleNoise(t *testing.T) {
	// conan writes "Connecting to remote..." notices, so tolerate any leading
	// non-JSON text before the payload object.
	data := []byte("Connecting to remote 'conancenter' anonymously\n" +
		"Found 22 pkg/version recipes matching fmt in conancenter\n" +
		"{\n  \"conancenter\": {\n    \"fmt/11.1.4\": {}\n  }\n}\n")
	packages, err := parseListOutput(data, "conancenter")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Name != "fmt" {
		t.Fatalf("leading console noise broke parsing: %+v", packages)
	}
}

func TestParseListOutputRejectsNonJSON(t *testing.T) {
	if _, err := parseListOutput([]byte("not json"), "conancenter"); err == nil || !strings.Contains(err.Error(), "E_CONAN_LAYOUT") {
		t.Fatalf("expected a layout error, got %v", err)
	}
}

func TestParseRemoteList(t *testing.T) {
	table := []byte("REMOTE  URL  VERIFY SSL  ENABLED\n" +
		"conancenter: https://center2.conan.io [Verify SSL: True, Enabled: True]\n" +
		"private: https://artifactory.example.com [Verify SSL: True, Enabled: True]\n")
	remotes := parseRemoteList(table)
	if len(remotes) != 2 || remotes[0] != "conancenter" || remotes[1] != "private" {
		t.Fatalf("unexpected remotes: %v", remotes)
	}
}

func TestSplitReference(t *testing.T) {
	for reference, want := range map[string][2]string{
		"fmt":                        {"fmt", ""},
		"fmt/10.2.1":                 {"fmt", "10.2.1"},
		"fmt/10.2.1@x/stable":        {"fmt", "10.2.1"},
		"openssl/3.2.1@conan/stable": {"openssl", "3.2.1"},
	} {
		name, version := splitReference(reference)
		if name != want[0] || version != want[1] {
			t.Fatalf("splitReference(%q) = (%q, %q), want (%q, %q)", reference, name, version, want[0], want[1])
		}
	}
}

func TestReferenceBuildsVersionAndFeatures(t *testing.T) {
	if got := Reference(config.Package{Port: "fmt", Version: "10.2.1"}); got != "fmt/10.2.1" {
		t.Fatalf("plain reference = %q", got)
	}
	if got := Reference(config.Package{Port: "zlib", Features: []string{"shared", "fPIC"}}); got != "zlib[shared,fPIC]" {
		t.Fatalf("feature reference = %q", got)
	}
	if got := Reference(config.Package{Port: "fmt", Version: "10.2.1", Features: []string{"with_tls"}}); got != "fmt/10.2.1[with_tls]" {
		t.Fatalf("versioned feature reference = %q", got)
	}
}

func TestInstallArgsCarriesProfileOutputFolderAndOptions(t *testing.T) {
	client := Client{
		BuildRoot: filepath.Join("build", "deps"),
		Profile:   filepath.Join("profiles", "debug"),
		Settings:  map[string]string{"build_type": "Debug", "skipped": ""},
		Options:   map[string]string{"shared": "True"},
	}
	args := strings.Join(client.installArgs("fmt/10.2.1"), " ")
	for _, want := range []string{
		"install",
		"--requires=fmt/10.2.1",
		"--build=missing",
		"-pr " + client.Profile,
		"-of=" + filepath.Join(client.BuildRoot, "conan-debug"),
		"-g CMakeDeps -g CMakeToolchain",
		"-s build_type=Debug",
		"-o shared=True",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("install args missing %q: %s", want, args)
		}
	}
	if strings.Contains(args, "skipped=") {
		t.Fatalf("empty settings must be dropped: %s", args)
	}
}

func TestInstallArgsWithoutBuildRootOmitsOutputFolder(t *testing.T) {
	client := Client{}
	for _, arg := range client.installArgs("fmt") {
		if strings.HasPrefix(arg, "-of=") || strings.HasPrefix(arg, "-pr") {
			t.Fatalf("unexpected output/profile flag %q", arg)
		}
	}
}

func TestClientEnvironmentOverridesExistingValues(t *testing.T) {
	t.Setenv("TRESTLE_CONAN_TEST_PATH", "original")
	client := Client{Environment: map[string]string{
		"PATH":                    "C:\\Conan",
		"TRESTLE_CONAN_TEST_PATH": "overridden",
	}}
	values := make(map[string]string)
	for _, entry := range client.environment() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			values[strings.ToUpper(parts[0])] = parts[1]
		}
	}
	if values["PATH"] != "C:\\Conan" || values["TRESTLE_CONAN_TEST_PATH"] != "overridden" {
		t.Fatalf("unexpected environment: %#v", values)
	}
	if os.Getenv("TRESTLE_CONAN_TEST_PATH") != "original" {
		t.Fatal("client environment mutated the parent process")
	}
}

func TestNewLayoutRequiresExistingInstallOutput(t *testing.T) {
	root := t.TempDir()
	buildRoot := filepath.Join(root, "build")
	if _, err := NewLayout(root, buildRoot, "default"); err == nil || !strings.Contains(err.Error(), "E_CONAN_LAYOUT") {
		t.Fatalf("expected a missing-output error, got %v", err)
	}
	if _, err := NewLayout(root, "", "default"); err == nil || !strings.Contains(err.Error(), "E_CONAN_BUILD_ROOT") {
		t.Fatalf("expected a missing build root error, got %v", err)
	}
	output := filepath.Join(buildRoot, "conan-default")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(root, buildRoot, "default")
	if err != nil {
		t.Fatal(err)
	}
	if layout.OutputDir != output || layout.IncludeDir != filepath.Join(output, "include") {
		t.Fatalf("unexpected layout: %+v", layout)
	}
}

func TestResolverReadsGeneratorOutput(t *testing.T) {
	root := t.TempDir()
	buildRoot := filepath.Join(root, "build")
	output := filepath.Join(buildRoot, "conan-default")
	if err := os.MkdirAll(filepath.Join(output, "include"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(output, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(output, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "include", "fmt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "lib", "fmt.lib"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "bin", "zlib1.dll"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	generated := "set(fmt_LIBRARIES \"${fmt_LIBRARIES};${CMAKE_CURRENT_LIST_DIR}/lib/fmt.lib\" OPTIONAL)\n"
	if err := os.WriteFile(filepath.Join(output, "fmtConfig.cmake"), []byte(generated), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{Root: root, BuildRoot: buildRoot, Profile: "default"}
	resolved, err := resolver.Resolve(context.Background(), config.Package{Provider: "conan", Port: "fmt", Version: "10.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Method != ResolutionGenerators || resolved.Reference != "fmt/10.2.1" {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
	if len(resolved.Usage.Link.Items) != 1 || resolved.Usage.Link.Items[0].Name != "fmt.lib" {
		t.Fatalf("library not read from CMakeDeps output: %+v", resolved.Usage.Link.Items)
	}
	if len(resolved.Usage.Compile.IncludeDirs) != 1 {
		t.Fatalf("include dir missing: %+v", resolved.Usage.Compile.IncludeDirs)
	}
	if len(resolved.Usage.Link.RuntimeFiles) != 1 || !strings.HasSuffix(resolved.Usage.Link.RuntimeFiles[0], "zlib1.dll") {
		t.Fatalf("runtime files missing: %+v", resolved.Usage.Link.RuntimeFiles)
	}
}

func TestResolverFallsBackToManualMetadata(t *testing.T) {
	resolver := Resolver{}
	resolved, err := resolver.Resolve(context.Background(), config.Package{Provider: "conan", Port: "fmt", IncludeDirs: []string{"vendor/include"}, Libraries: []string{"fmt"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Method != ResolutionManual {
		t.Fatalf("expected manual resolution, got %q", resolved.Method)
	}
	if len(resolved.Usage.Compile.IncludeDirs) != 1 || len(resolved.Usage.Link.Items) != 1 {
		t.Fatalf("manual usage not carried over: %+v", resolved.Usage)
	}
}

func packageBySource(t *testing.T, packages []Package, source string) Package {
	t.Helper()
	for _, entry := range packages {
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf("package from %q not found in %+v", source, packages)
	return Package{}
}
