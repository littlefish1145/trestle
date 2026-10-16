package vcpkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeVersionDatabase(t *testing.T, root, name, body string) {
	t.Helper()
	shard := filepath.Join(root, "versions", strings.ToLower(string(name[0]))+"-")
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shard, strings.ToLower(name)+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPortVersionsRejectsBlankName(t *testing.T) {
	client := Client{Root: t.TempDir()}
	_, err := client.PortVersions(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_HISTORY") {
		t.Fatalf("err = %v, want E_VCPKG_HISTORY", err)
	}
}

// A database entry that cannot be read is a hard error, not an empty history:
// a directory named like the database file exercises the read failure branch.
func TestPortVersionsReportsUnreadableDatabase(t *testing.T) {
	root := t.TempDir()
	shard := filepath.Join(root, "versions", "f-")
	if err := os.MkdirAll(filepath.Join(shard, "fmt.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (Client{Root: root}).PortVersions(context.Background(), "fmt")
	if err == nil || !strings.Contains(err.Error(), "E_VCPKG_HISTORY") {
		t.Fatalf("err = %v, want E_VCPKG_HISTORY", err)
	}
}

func TestPortVersionsReportsCorruptDatabase(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "fmt", "{not json")
	_, err := (Client{Root: root}).PortVersions(context.Background(), "fmt")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

// Entries without a version are noise from the database format; the entry that
// matches the working-tree manifest is current, and matching an installed list
// marks the version installed.
func TestPortVersionsSkipsEmptyEntriesAndMarksCurrentAndInstalled(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "fmt", `{"versions":[
		{"git-tree":"zzz","version":"","port-version":0},
		{"git-tree":"aaa","version":"11.2.0","port-version":0},
		{"git-tree":"bbb","version":"10.2.1","port-version":0}
	]}`)
	portDir := filepath.Join(root, "ports", "fmt")
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte(`{"name":"fmt","version":"11.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(root, "installed", "vcpkg", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info, "fmt_11.2.0_x64-windows.list"), []byte("x64-windows/lib/fmt.lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	versions, err := (Client{Root: root}).PortVersions(context.Background(), "fmt")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %+v, want 2 usable entries", versions)
	}
	if !versions[0].Current || !versions[0].Installed {
		t.Fatalf("newest entry should be current and installed: %+v", versions[0])
	}
	if versions[1].Current || versions[1].Installed {
		t.Fatalf("older entry must not be current or installed: %+v", versions[1])
	}
}

// A manifest carrying both a version and a version-date cannot be the current
// tree entry, because the entry matched only on the version field.
func TestPortVersionsKeepsCurrentFalseWhenManifestHasVersionDate(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "demo", `{"versions":[{"git-tree":"aaa","version":"5.0","port-version":0}]}`)
	portDir := filepath.Join(root, "ports", "demo")
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte(`{"name":"demo","version":"5.0","version-date":"2023-01-01"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	versions, err := (Client{Root: root}).PortVersions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(versions) != 1 || versions[0].Current {
		t.Fatalf("versions = %+v, want one non-current entry", versions)
	}
}

func TestCurrentManifestFallsBackToVersionString(t *testing.T) {
	root := t.TempDir()
	portDir := filepath.Join(root, "ports", "demo")
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte(`{"version-string":"7.5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := (Client{Root: root}).currentManifest("demo")
	if manifest == nil || manifest.Version != "7.5" {
		t.Fatalf("manifest = %+v, want version-string fallback", manifest)
	}
}

func TestCurrentManifestRejectsCorruptAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	portDir := filepath.Join(root, "ports", "bad")
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := Client{Root: root}
	if manifest := client.currentManifest("bad"); manifest != nil {
		t.Fatalf("corrupt manifest = %+v, want nil", manifest)
	}
	if manifest := client.currentManifest("missing"); manifest != nil {
		t.Fatalf("missing manifest = %+v, want nil", manifest)
	}
}

func TestManifestDatePrefersExplicitDate(t *testing.T) {
	if got := (portManifest{VersionDate: "2023-08-10", VersionField: "7.5"}).date(); got != "2023-08-10" {
		t.Fatalf("date = %q", got)
	}
	if got := (portManifest{VersionField: "7.5"}).date(); got != "7.5" {
		t.Fatalf("date = %q", got)
	}
	if got := (portManifest{}).date(); got != "" {
		t.Fatalf("date = %q, want empty", got)
	}
}

// gitRepository initializes a throwaway repository. Tests that need git-tree
// resolution skip where git is unavailable instead of failing the environment.
func gitRepository(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	root := t.TempDir()
	run := func(args ...string) string {
		full := append([]string{"-C", root, "-c", "user.name=trestle-test", "-c", "user.email=trestle@example.com"}, args...)
		output, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-q")
	return root, run
}

// A real repository proves the whole path: the database names a git tree, git
// resolves it, and the manifest of that release fills description, license,
// homepage and dependencies. A tree that does not resolve is skipped instead of
// failing the history.
func TestPortVersionsResolvesGitTreeDetails(t *testing.T) {
	root, git := gitRepository(t)
	manifest := `{"name":"demo","version-string":"1.0.0","description":"detailed demo","homepage":"https://demo.example","license":"MIT","dependencies":["dep","other"]}`
	if err := os.WriteFile(filepath.Join(root, "vcpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "vcpkg.json")
	git("commit", "-q", "-m", "port")
	tree := git("rev-parse", "HEAD^{tree}")

	writeVersionDatabase(t, root, "demo", fmt.Sprintf(`{"versions":[
		{"git-tree":%q,"version":"1.0.0","port-version":2},
		{"git-tree":"0123456789abcdef0123456789abcdef01234567","version":"0.9.0","port-version":0},
		{"git-tree":"","version":"0.8.0","port-version":0}
	]}`, tree))
	portDir := filepath.Join(root, "ports", "demo")
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(portDir, "vcpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(root, "installed", "vcpkg", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info, "demo_1.0.0_x64-windows.list"), []byte("x64-windows/lib/demo.lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	versions, err := (Client{Root: root}).PortVersions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("versions = %+v", versions)
	}
	newest := versions[0]
	if newest.FormatVersion() != "1.0.0#2" || !newest.Current || !newest.Installed || newest.GitTree != tree {
		t.Fatalf("newest = %+v", newest)
	}
	if newest.Description != "detailed demo" || newest.License != "MIT" || newest.Homepage != "https://demo.example" {
		t.Fatalf("release metadata = %+v", newest)
	}
	if len(newest.Dependencies) != 2 || newest.Date != "1.0.0" {
		t.Fatalf("release details = %+v", newest)
	}
	// The unresolvable and empty git trees keep their database fields only.
	for _, older := range versions[1:] {
		if older.Description != "" || older.Date != "" || older.License != "" {
			t.Fatalf("older entry gained metadata: %+v", older)
		}
	}
}

func TestPortVersionsStopsOnCanceledContext(t *testing.T) {
	root, git := gitRepository(t)
	if err := os.WriteFile(filepath.Join(root, "vcpkg.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "vcpkg.json")
	git("commit", "-q", "-m", "port")
	tree := git("rev-parse", "HEAD^{tree}")
	writeVersionDatabase(t, root, "demo", fmt.Sprintf(`{"versions":[{"git-tree":%q,"version":"1.0.0","port-version":0}]}`, tree))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	versions, err := (Client{Root: root}).PortVersions(ctx, "demo")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %+v, want the database entries despite cancellation", versions)
	}
}

func TestManifestFromTreeSurfacesFailures(t *testing.T) {
	root, git := gitRepository(t)
	client := Client{Root: root}
	emptyTree := git("mktree")
	if _, err := client.manifestFromTree(context.Background(), emptyTree); err == nil {
		t.Fatal("an empty tree has no vcpkg.json")
	}
	if err := os.WriteFile(filepath.Join(root, "vcpkg.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "vcpkg.json")
	git("commit", "-q", "-m", "broken")
	brokenTree := git("rev-parse", "HEAD^{tree}")
	if _, err := client.manifestFromTree(context.Background(), brokenTree); err == nil {
		t.Fatal("invalid manifest JSON must be reported")
	}
}

func TestHasGitCheckoutRequiresRepositoryAndGit(t *testing.T) {
	if (Client{Root: t.TempDir()}).hasGitCheckout() {
		t.Fatal("a directory without .git is not a checkout")
	}
	root, _ := gitRepository(t)
	client := Client{Root: root}
	if !client.hasGitCheckout() {
		t.Fatal("a repository with git on PATH is a checkout")
	}
	t.Setenv("PATH", t.TempDir())
	if client.hasGitCheckout() {
		t.Fatal("without git on PATH the history cannot be resolved")
	}
}

const fmtDatabase = `{"versions":[
	{"git-tree":"a1","version":"11.2.0","port-version":0},
	{"git-tree":"a2","version":"11.0.2","port-version":1},
	{"git-tree":"a3","version":"10.2.1","port-version":0}
]}`

const spdlogDatabase = `{"versions":[
	{"git-tree":"b1","version":"1.17.0","port-version":0},
	{"git-tree":"b2","version":"1.15.2","port-version":0}
]}`

// SearchPortsVersions merges the full history of every port the search names,
// and falls back to the single working-tree version for a port the version
// database has never seen.
func TestSearchPortsVersionsMergesHistoryAndFallback(t *testing.T) {
	client := newFakeClient(t, "search")
	writeVersionDatabase(t, client.Root, "fmt", fmtDatabase)
	writeVersionDatabase(t, client.Root, "spdlog", spdlogDatabase)

	versions, err := client.SearchPortsVersions(context.Background(), "fmt", 0)
	if err != nil {
		t.Fatalf("search versions: %v", err)
	}
	if len(versions) != 6 {
		t.Fatalf("versions = %+v, want 3 fmt + 2 spdlog + 1 zlib fallback", versions)
	}
	wantFormats := []string{"11.2.0", "11.0.2#1", "10.2.1", "1.17.0", "1.15.2", "1.3.1"}
	for index, want := range wantFormats {
		if got := versions[index].FormatVersion(); got != want {
			t.Fatalf("versions[%d] = %s, want %s (all: %+v)", index, got, want, versions)
		}
	}
	fallback := versions[5]
	if fallback.Port != "zlib" || !fallback.Current || fallback.Installed || fallback.PackageURL == "" {
		t.Fatalf("fallback = %+v", fallback)
	}
}

func TestSearchPortsVersionsHonorsLimit(t *testing.T) {
	client := newFakeClient(t, "search")
	writeVersionDatabase(t, client.Root, "fmt", fmtDatabase)
	writeVersionDatabase(t, client.Root, "spdlog", spdlogDatabase)
	versions, err := client.SearchPortsVersions(context.Background(), "fmt", 2)
	if err != nil {
		t.Fatalf("search versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %+v, want the limit applied", versions)
	}
}

func hugeDatabase(entries int) string {
	var builder strings.Builder
	builder.WriteString(`{"versions":[`)
	for index := 0; index < entries; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `{"git-tree":"t%d","version":"2.0.%d","port-version":0}`, index, index)
	}
	builder.WriteString("]}")
	return builder.String()
}

// A port with more history than the page can carry is reported, not silently
// cut, and a search that already found something keeps what it has.
func TestSearchPortsVersionsReportsTruncation(t *testing.T) {
	client := newFakeClient(t, "search")
	writeVersionDatabase(t, client.Root, "big", hugeDatabase(201))
	_, err := client.SearchPortsVersions(context.Background(), "big", 0)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err = %v, want a truncation error", err)
	}
}

func TestSearchPortsVersionsKeepsPartialResultsOnTruncation(t *testing.T) {
	client := newFakeClient(t, "search")
	writeVersionDatabase(t, client.Root, "fmt", fmtDatabase)
	writeVersionDatabase(t, client.Root, "big", hugeDatabase(201))
	versions, err := client.SearchPortsVersions(context.Background(), "mixed", 0)
	if err != nil {
		t.Fatalf("search versions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("versions = %+v, want the 3 fmt entries found before the huge port", versions)
	}
}

func TestSearchPortsVersionsPropagatesSearchFailure(t *testing.T) {
	client := newFakeClient(t, "search")
	_, err := client.SearchPortsVersions(context.Background(), "boom", 0)
	if err == nil || !strings.Contains(err.Error(), "search failed") {
		t.Fatalf("err = %v", err)
	}
}

// The unknown-version error names the newest releases, capped at four.
func TestResolveVersionListsNewestFourWhenNothingMatches(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "fmt", `{"versions":[
		{"git-tree":"a","version":"5.0.0","port-version":0},
		{"git-tree":"b","version":"4.0.0","port-version":0},
		{"git-tree":"c","version":"3.0.0","port-version":0},
		{"git-tree":"d","version":"2.0.0","port-version":0},
		{"git-tree":"e","version":"1.0.0","port-version":0}
	]}`)
	_, err := (Client{Root: root}).ResolveVersion(context.Background(), "fmt", "0.0.1")
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "newest: 5.0.0, 4.0.0, 3.0.0, 2.0.0"
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "1.0.0") {
		t.Fatalf("err = %v, want %q and no fifth release", err, want)
	}
}

func TestResolveVersionWithoutUsableEntries(t *testing.T) {
	root := t.TempDir()
	writeVersionDatabase(t, root, "fmt", `{"versions":[{"git-tree":"a","version":"","port-version":0}]}`)
	_, err := (Client{Root: root}).ResolveVersion(context.Background(), "fmt", "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "has no version 1.0.0") || strings.Contains(err.Error(), "newest:") {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitPortVersionTable(t *testing.T) {
	cases := []struct {
		input   string
		version string
		port    int
	}{
		{"11.0.2", "11.0.2", -1},
		{"11.0.2#3", "11.0.2", 3},
		{"11.0.2#", "11.0.2", -1},
		{"11.0.2#x", "11.0.2#x", -1},
		{" 11.0.2#7 ", "11.0.2", 7},
		{"", "", -1},
	}
	for _, test := range cases {
		version, port := splitPortVersion(test.input)
		if version != test.version || port != test.port {
			t.Fatalf("splitPortVersion(%q) = (%q, %d), want (%q, %d)", test.input, version, port, test.version, test.port)
		}
	}
}

func TestVersionNumbersRejectsNonNumericSchemes(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"1.2.3": true,
		"":      false,
		"1..2":  false,
		"1.2a":  false,
		"1_2":   false,
		"1-2":   false,
	}
	for input, ok := range cases {
		if _, got := versionNumbers(input); got != ok {
			t.Fatalf("versionNumbers(%q) ok = %v, want %v", input, got, ok)
		}
	}
}

func TestCompareVersionStringsOrdering(t *testing.T) {
	cases := []struct {
		left, right string
		sign        int
	}{
		{"1.10", "1.2", 1},
		{"1.2", "1.10", -1},
		{"1.2.3", "1.2", 1},
		{"1.2", "1.2.0", 0},
		{"2024-06-01", "2023-08-10", 1},
		{"2023-08-10", "2024-06-01", -1},
		{"11.0.0", "2023-08-10", 1},
		{"2023-08-10", "11.0.0", -1},
		{"tls-v2", "tls-v1", 1},
	}
	for _, test := range cases {
		got := compareVersionStrings(test.left, test.right)
		if test.sign > 0 && got <= 0 || test.sign < 0 && got >= 0 || test.sign == 0 && got != 0 {
			t.Fatalf("compareVersionStrings(%q, %q) = %d, want sign %d", test.left, test.right, got, test.sign)
		}
	}
}

// Date-scheme ports must still list newest first; the comparison used to treat
// a newer date as smaller, which reversed the whole history.
func TestSortPortVersionsOrdersDatesNewestFirst(t *testing.T) {
	versions := []PortVersion{
		{Version: "2023-08-10"},
		{Version: "2024-06-01"},
		{Version: "2023-12-01"},
	}
	sortPortVersions(versions)
	want := []string{"2024-06-01", "2023-12-01", "2023-08-10"}
	for index, expected := range want {
		if versions[index].Version != expected {
			t.Fatalf("versions = %+v, want %v", versions, want)
		}
	}
}

func TestFormatVersionWithoutUpstreamVersion(t *testing.T) {
	if got := (PortVersion{}).FormatVersion(); got != "unknown" {
		t.Fatalf("FormatVersion = %q", got)
	}
}
