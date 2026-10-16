package vcpkg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/model"
)

func modelUsage(name string) model.Usage {
	return model.Usage{
		Compile: model.CompileUsage{IncludeDirs: []string{name}, Defines: []string{name}, Options: []string{name}},
		Link: model.LinkUsage{
			LibraryDirs:  []string{name},
			Items:        []model.LinkItem{{Kind: "library", Name: name}},
			Options:      []string{name},
			RuntimeFiles: []string{name},
		},
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolverFixture builds a vcpkg installation with pkg-config metadata, an
// installed manifest for a different port, and debug binaries, so every
// resolution method can be exercised against real files.
func resolverFixture(t *testing.T) (string, Layout) {
	t.Helper()
	root := t.TempDir()
	triplet := "x64-windows"
	installed := filepath.Join(root, "installed", triplet)
	mustWrite(t, filepath.Join(installed, "include", "demo", "demo.h"), "header")
	mustWrite(t, filepath.Join(installed, "include", "plain", "plain.h"), "header")
	mustWrite(t, filepath.Join(installed, "lib", "demo.lib"), "lib")
	mustWrite(t, filepath.Join(installed, "lib", "dep.lib"), "lib")
	mustWrite(t, filepath.Join(installed, "lib", "plain.lib"), "lib")
	mustWrite(t, filepath.Join(installed, "bin", "demo.dll"), "dll")
	mustWrite(t, filepath.Join(installed, "debug", "lib", "dbg.lib"), "lib")
	mustWrite(t, filepath.Join(installed, "debug", "bin", "dbg.dll"), "dll")
	mustWrite(t, filepath.Join(installed, "lib", "pkgconfig", "demo.pc"), `prefix=${pcfiledir}/../..
Name: demo
Requires: dep
Cflags: -I${prefix}/include -DDEMO=1 -fPIC
Libs: -L${prefix}/lib -ldemo foo.lib -Wl,--gc-sections
Libs.private: -lbcrypt
`)
	mustWrite(t, filepath.Join(installed, "share", "pkgconfig", "dep.pc"), `Name: dep
Requires: demo
Cflags: -I${pcfiledir}/../../include
Libs: -L${pcfiledir}/../../lib -ldep
`)
	mustWrite(t, filepath.Join(installed, "debug", "lib", "pkgconfig", "dbg.pc"), `Name: dbg
Cflags: -I${pcfiledir}/../../include
Libs: -L${pcfiledir}/../../lib -ldbg
`)
	mustWrite(t, filepath.Join(installed, "lib", "pkgconfig", "lonely.pc"), "Name: lonely\nRequires: ghost\nCflags: -I/nowhere\nLibs: -lghost\n")
	mustWrite(t, filepath.Join(installed, "lib", "pkgconfig", "broken.pc"), "Name: broken\nCflags: -I${missing}\n")
	mustWrite(t, filepath.Join(root, "installed", "vcpkg", "info", "demo_1.0_x64-windows.list"),
		"x64-windows/include/demo/demo.h\nx64-windows/lib/demo.lib\nx64-windows/bin/demo.dll\nx64-windows/bin/ghost.dll\n")
	mustWrite(t, filepath.Join(root, "installed", "vcpkg", "info", "plain_1.0_x64-windows.list"),
		"x64-windows/include/plain/plain.h\nx64-windows/lib/plain.lib\n")
	mustWrite(t, filepath.Join(root, "installed", "vcpkg", "info", "dep_1.0_x64-windows.list"),
		"x64-windows/lib/dep.lib\n")
	mustWrite(t, filepath.Join(root, "installed", "vcpkg", "info", "dbg_1.0_x64-windows.list"),
		"x64-windows/debug/lib/dbg.lib\nx64-windows/debug/bin/dbg.dll\n")
	mustWrite(t, filepath.Join(root, "installed", "vcpkg", "status"), `Package: plain
Architecture: x64-windows
Depends: dep, ghost (>= 1)
Version: 1.0

Package: dep
Architecture: x64-windows
Depends: plain
Version: 1.0

Package: dbg
Architecture: x64-windows
Depends: 
Version: 1.0

Package: ignored
Architecture: arm64-linux
Depends: plain
Version: 9.9
`)
	layout, err := DetectLayout(root, triplet)
	if err != nil {
		t.Fatal(err)
	}
	return root, layout
}

func packageFor(port, triplet string) config.Package {
	pkg := config.Package{Provider: "vcpkg", Port: port}
	if triplet != "" {
		pkg.Triplet = triplet
	}
	return pkg
}

// The pkg-config method resolves the port itself, its transitive requires, the
// manifests' runtime DLLs, and the compile/link flags a .pc file spells out.
func TestResolveThroughPkgConfig(t *testing.T) {
	root, layout := resolverFixture(t)
	resolved, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("demo", layout.Triplet))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Method != ResolutionPkgConfig || resolved.Name != "demo" || resolved.Triplet != layout.Triplet {
		t.Fatalf("resolved = %+v", resolved)
	}
	usage := resolved.Usage
	if len(usage.Compile.IncludeDirs) == 0 || usage.Compile.IncludeDirs[0] != layout.IncludeDir {
		t.Fatalf("include dirs = %v", usage.Compile.IncludeDirs)
	}
	for _, define := range usage.Compile.Defines {
		if define != "DEMO=1" {
			t.Fatalf("defines = %v", usage.Compile.Defines)
		}
	}
	if len(usage.Compile.Options) != 1 || usage.Compile.Options[0] != "-fPIC" {
		t.Fatalf("compile options = %v", usage.Compile.Options)
	}
	if len(usage.Link.LibraryDirs) != 1 || usage.Link.LibraryDirs[0] != layout.ReleaseLibDir {
		t.Fatalf("library dirs = %v", usage.Link.LibraryDirs)
	}
	items := map[string]string{}
	for _, item := range usage.Link.Items {
		items[item.Name] = item.Path
	}
	if items["demo"] != filepath.Join(layout.ReleaseLibDir, "demo.lib") {
		t.Fatalf("demo item = %q", items["demo"])
	}
	if items["dep"] != filepath.Join(layout.ReleaseLibDir, "dep.lib") {
		t.Fatalf("transitive dep item = %q", items["dep"])
	}
	if _, ok := items["foo.lib"]; !ok {
		t.Fatalf("absolute library flag was dropped: %v", usage.Link.Items)
	}
	if len(usage.Link.Options) != 1 || usage.Link.Options[0] != "-Wl,--gc-sections" {
		t.Fatalf("link options = %v", usage.Link.Options)
	}
	if len(usage.Link.RuntimeFiles) != 2 {
		t.Fatalf("runtime files = %v", usage.Link.RuntimeFiles)
	}
}

// Static linkage must pull in Libs.private, even when the private library is
// not on disk: the item stays, only its path is empty.
func TestResolveStaticLinkageAddsPrivateLibraries(t *testing.T) {
	root, layout := resolverFixture(t)
	resolved, err := (Resolver{Root: root, LibraryLinkage: "static"}).Resolve(context.Background(), packageFor("demo", layout.Triplet))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	found := false
	for _, item := range resolved.Usage.Link.Items {
		if item.Name == "bcrypt" {
			found = true
			if item.Path != "" {
				t.Fatalf("bcrypt should not resolve to a file: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("Libs.private was ignored: %+v", resolved.Usage.Link.Items)
	}
}

// The debug profile selects the debug triplet tree and the debug runtime DLL.
func TestResolveDebugProfileUsesDebugTree(t *testing.T) {
	root, layout := resolverFixture(t)
	resolved, err := (Resolver{Root: root, Profile: "debug"}).Resolve(context.Background(), packageFor("dbg", layout.Triplet))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Method != ResolutionPkgConfig {
		t.Fatalf("resolved = %+v", resolved)
	}
	if len(resolved.Usage.Link.LibraryDirs) != 1 || resolved.Usage.Link.LibraryDirs[0] != layout.DebugLibDir {
		t.Fatalf("library dirs = %v", resolved.Usage.Link.LibraryDirs)
	}
	if len(resolved.Usage.Link.RuntimeFiles) != 1 || filepath.Base(resolved.Usage.Link.RuntimeFiles[0]) != "dbg.dll" {
		t.Fatalf("runtime files = %v", resolved.Usage.Link.RuntimeFiles)
	}
}

// A missing transitive requirement is skipped, not fatal; an unparsable pc
// file is reported.
func TestResolvePkgConfigDegradations(t *testing.T) {
	root, layout := resolverFixture(t)
	resolved, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("lonely", layout.Triplet))
	if err != nil {
		t.Fatalf("resolve lonely: %v", err)
	}
	if resolved.Method != ResolutionPkgConfig || len(resolved.Usage.Link.Items) != 1 {
		t.Fatalf("resolved = %+v", resolved)
	}
	if _, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("broken", layout.Triplet)); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

// Ports without pkg-config metadata fall back to the built-in adapters.
func TestResolveUsesBuiltinAdapter(t *testing.T) {
	root, layout := resolverFixture(t)
	resolved, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("glm", layout.Triplet))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Method != ResolutionAdapter || len(resolved.Usage.Compile.IncludeDirs) != 1 || resolved.Usage.Compile.IncludeDirs[0] != layout.IncludeDir {
		t.Fatalf("resolved = %+v", resolved)
	}
}

// Ports without any metadata fall back to vcpkg's installed file lists, and the
// dependencies recorded in the status database are merged in, cycles included.
func TestResolveFallsBackToInstalledManifest(t *testing.T) {
	root, layout := resolverFixture(t)
	resolver := Resolver{Root: root}
	resolved, err := resolver.Resolve(context.Background(), config.Package{Provider: "plain", Triplet: layout.Triplet})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Method != ResolutionManifest || resolved.Name != "plain" || resolved.Port != "plain" {
		t.Fatalf("resolved = %+v", resolved)
	}
	items := map[string]string{}
	for _, item := range resolved.Usage.Link.Items {
		items[item.Name] = item.Path
	}
	if items["plain"] != filepath.Join(layout.ReleaseLibDir, "plain.lib") || items["dep"] == "" {
		t.Fatalf("items = %v", resolved.Usage.Link.Items)
	}
	// ghost is declared in the status file but has no manifest; it is skipped.
	if _, ok := items["ghost"]; ok {
		t.Fatalf("ghost should have no manifest: %v", resolved.Usage.Link.Items)
	}
}

// Manual overrides win whenever the vcpkg root cannot provide metadata.
func TestResolveManualMetadata(t *testing.T) {
	manual := config.Package{
		Provider: "manual", Port: "m",
		IncludeDirs: []string{"inc"}, LibraryDirs: []string{"lib"}, Libraries: []string{"m"},
		Defines: []string{"X=1"}, RuntimeFiles: []string{"m.dll"},
	}
	// No root at all.
	resolved, err := (Resolver{}).Resolve(context.Background(), manual)
	if err != nil {
		t.Fatalf("resolve without root: %v", err)
	}
	if resolved.Method != ResolutionManual || resolved.Name != "manual" || resolved.Port != "m" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if strings.Join(resolved.Usage.Compile.IncludeDirs, ",") != "inc" || strings.Join(resolved.Usage.Compile.Defines, ",") != "X=1" {
		t.Fatalf("usage = %+v", resolved.Usage)
	}
	if strings.Join(resolved.Usage.Link.LibraryDirs, ",") != "lib" || resolved.Usage.Link.Items[0].Name != "m" || len(resolved.Usage.Link.RuntimeFiles) != 1 {
		t.Fatalf("usage = %+v", resolved.Usage)
	}
	// Root configured, but the triplet is not installed.
	resolved, err = (Resolver{Root: t.TempDir()}).Resolve(context.Background(), manual)
	if err != nil || resolved.Method != ResolutionManual {
		t.Fatalf("resolved = %+v, err = %v", resolved, err)
	}
	// Root installed, but the port has no metadata in it.
	root, layout := resolverFixture(t)
	manual.Triplet = layout.Triplet
	resolved, err = (Resolver{Root: root}).Resolve(context.Background(), manual)
	if err != nil || resolved.Method != ResolutionManual {
		t.Fatalf("resolved = %+v, err = %v", resolved, err)
	}
}

func TestResolveReportsMissingConfiguration(t *testing.T) {
	t.Setenv("VCPKG_ROOT", "")
	if _, err := (Resolver{}).Resolve(context.Background(), packageFor("demo", "x64-windows")); err == nil || !strings.Contains(err.Error(), "E_VCPKG_ROOT") {
		t.Fatalf("err = %v, want E_VCPKG_ROOT", err)
	}
	if _, err := (Resolver{Root: t.TempDir()}).Resolve(context.Background(), packageFor("demo", "")); err == nil || !strings.Contains(err.Error(), "E_VCPKG_TRIPLET") {
		t.Fatalf("err = %v, want E_VCPKG_TRIPLET", err)
	}
	root := t.TempDir()
	if _, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("demo", "x64-windows")); err == nil || !strings.Contains(err.Error(), "E_VCPKG_LAYOUT") {
		t.Fatalf("err = %v, want E_VCPKG_LAYOUT", err)
	}
}

func TestResolveReportsUnsupportedMetadata(t *testing.T) {
	root, layout := resolverFixture(t)
	_, err := (Resolver{Root: root}).Resolve(context.Background(), packageFor("naked", layout.Triplet))
	if err == nil || !strings.Contains(err.Error(), "E_PACKAGE_METADATA_UNSUPPORTED") {
		t.Fatalf("err = %v", err)
	}
}

func TestLibraryItemChoosesTheExistingCandidate(t *testing.T) {
	directory := t.TempDir()
	mustWrite(t, filepath.Join(directory, "foo.lib"), "lib")
	mustWrite(t, filepath.Join(directory, "libbar.a"), "lib")
	mustWrite(t, filepath.Join(directory, "baz.a"), "lib")
	if item := libraryItem(directory, "foo"); item.Path != filepath.Join(directory, "foo.lib") {
		t.Fatalf("item = %+v", item)
	}
	if item := libraryItem(directory, "bar"); item.Path != filepath.Join(directory, "libbar.a") {
		t.Fatalf("item = %+v", item)
	}
	if item := libraryItem(directory, "baz"); item.Path != filepath.Join(directory, "baz.a") {
		t.Fatalf("item = %+v", item)
	}
	if item := libraryItem(directory, "missing"); item.Path != "" {
		t.Fatalf("item = %+v", item)
	}
	if path := existingLibrary(directory, "foo.lib"); path == "" {
		t.Fatal("existing library not found")
	}
	if path := existingLibrary(directory, "nope.lib"); path != "" {
		t.Fatalf("path = %q", path)
	}
}

func TestMergeUsageConcatenatesEveryField(t *testing.T) {
	a := modelUsage("a")
	b := modelUsage("b")
	merged := mergeUsage(a, b)
	if len(merged.Compile.IncludeDirs) != 2 || len(merged.Compile.Defines) != 2 || len(merged.Compile.Options) != 2 {
		t.Fatalf("merged = %+v", merged)
	}
	if len(merged.Link.LibraryDirs) != 2 || len(merged.Link.Items) != 2 || len(merged.Link.Options) != 2 || len(merged.Link.RuntimeFiles) != 2 {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestHasManualMetadataRecognizesEachField(t *testing.T) {
	if hasManualMetadata(config.Package{}) {
		t.Fatal("an empty package has no manual metadata")
	}
	for _, pkg := range []config.Package{
		{IncludeDirs: []string{"inc"}},
		{LibraryDirs: []string{"lib"}},
		{Libraries: []string{"lib"}},
		{Defines: []string{"X"}},
	} {
		if !hasManualMetadata(pkg) {
			t.Fatalf("package %+v should count as manual", pkg)
		}
	}
}

func TestPackageNameFromPathAndInstalledMarker(t *testing.T) {
	if got := PackageNameFromPath(filepath.Join("root", "lib", "libdemo.pc")); got != "libdemo" {
		t.Fatalf("name = %q", got)
	}
	layout := Layout{InstalledPrefix: t.TempDir()}
	if InstalledMarker(layout) {
		t.Fatal("no include directory yet")
	}
	if err := os.MkdirAll(filepath.Join(layout.InstalledPrefix, "include"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !InstalledMarker(layout) {
		t.Fatal("include directory marks the tree installed")
	}
}

func TestResolveAdapterRegistry(t *testing.T) {
	layout := Layout{IncludeDir: filepath.Join("root", "include")}
	for _, port := range []string{"glm", "eigen3", "nlohmann-json"} {
		usage, ok := ResolveAdapter(port, layout)
		if !ok || len(usage.Compile.IncludeDirs) != 1 {
			t.Fatalf("port %s: ok = %v usage = %+v", port, ok, usage)
		}
	}
	if _, ok := ResolveAdapter("unknown", layout); ok {
		t.Fatal("unknown ports have no adapter")
	}
}

func TestDetectLayoutRequiresRoot(t *testing.T) {
	t.Setenv("VCPKG_ROOT", "")
	if _, err := DetectLayout("", "x64-windows"); err == nil || !strings.Contains(err.Error(), "E_VCPKG_ROOT") {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectLayoutValidatesTripletAndTree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VCPKG_ROOT", root)
	if _, err := DetectLayout("", "auto"); err == nil || !strings.Contains(err.Error(), "E_VCPKG_TRIPLET") {
		t.Fatalf("err = %v", err)
	}
	if _, err := DetectLayout("", "x64-windows"); err == nil || !strings.Contains(err.Error(), "E_VCPKG_LAYOUT") {
		t.Fatalf("err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "installed", "x64-windows"), 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := DetectLayout("", "x64-windows")
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	if layout.Root != root || len(layout.PkgConfigDirs) != 3 {
		t.Fatalf("layout = %+v", layout)
	}
}

func TestTripletHelpers(t *testing.T) {
	if DefaultTriplet() != TripletFor(runtime.GOOS, runtime.GOARCH) {
		t.Fatal("DefaultTriplet must match the host platform")
	}
	if TripletFor("windows", "amd64") != "x64-windows" || TripletFor("linux", "riscv64") != "riscv64-linux" {
		t.Fatal("TripletFor mapping changed")
	}
	cases := map[string]bool{
		"":            false,
		"auto":        false,
		"x64/windows": false,
		`x64\windows`: false,
		"x64-windows": true,
	}
	for name, want := range cases {
		if got := ValidTripletName(name); got != want {
			t.Fatalf("ValidTripletName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParsePCRejectsUnresolvedVariablesAndSkipsNoise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.pc")
	mustWrite(t, path, "just a line without separator\n# a comment\nName: demo\nCflags: -I${nope}\n")
	if _, err := ParsePC(path); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePCReportsMissingFile(t *testing.T) {
	if _, err := ParsePC(filepath.Join(t.TempDir(), "missing.pc")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFindPCSearchesAllDirectories(t *testing.T) {
	layout := Layout{PkgConfigDirs: []string{
		filepath.Join(t.TempDir(), "empty"),
		t.TempDir(),
	}}
	mustWrite(t, filepath.Join(layout.PkgConfigDirs[1], "demo.pc"), "Name: demo\n")
	path, err := FindPC(layout, "demo")
	if err != nil || path != filepath.Join(layout.PkgConfigDirs[1], "demo.pc") {
		t.Fatalf("path = %q, err = %v", path, err)
	}
	if _, err := FindPC(layout, "missing"); err == nil || !strings.Contains(err.Error(), "E_PACKAGE_METADATA_UNSUPPORTED") {
		t.Fatalf("err = %v", err)
	}
}

func TestLibraryNameNormalizesPlatforms(t *testing.T) {
	cases := map[string]string{
		"x64-windows/lib/foo.so":       "foo",
		"x64-windows/lib/libbar.dylib": "bar",
		"x64-windows/lib/baz.lib":      "baz",
		"x64-windows/share/readme.txt": "",
	}
	for input, want := range cases {
		if got := libraryName(input); got != want {
			t.Fatalf("libraryName(%q) = %q, want %q", input, got, want)
		}
	}
}

// A broken symlink among the installed manifests is skipped instead of
// failing the whole resolution.
func TestResolveInstalledManifestSkipsUnreadableLists(t *testing.T) {
	root := t.TempDir()
	info := filepath.Join(root, "installed", "vcpkg", "info")
	mustWrite(t, filepath.Join(info, "demo_1_x64-windows.list"), "x64-windows/lib/demo.lib\n")
	if err := os.Symlink(filepath.Join(root, "deleted.list"), filepath.Join(info, "demo_2_x64-windows.list")); err != nil {
		t.Skipf("symlinks are unavailable here: %v", err)
	}
	layout := Layout{Root: root, Triplet: "x64-windows"}
	usage, ok := ResolveInstalledManifestForProfile(layout, "demo", "release")
	if !ok || len(usage.Link.Items) != 1 || usage.Link.Items[0].Name != "demo" {
		t.Fatalf("usage = %+v, ok = %v", usage, ok)
	}
}

func TestRankTripletsDefaultsStaticLinkageAndTies(t *testing.T) {
	host := TripletFor(runtime.GOOS, runtime.GOARCH)
	candidates, err := RankTriplets(TripletRequest{Available: []string{host}})
	if err != nil || len(candidates) != 1 || candidates[0].Name != host {
		t.Fatalf("candidates = %+v, err = %v", candidates, err)
	}
	candidates, err = RankTriplets(TripletRequest{
		OS: "windows", Arch: "amd64", Compiler: "clang",
		CRTLinkage: "static", LibraryLinkage: "static",
		Available: []string{"x64-windows", "x64-windows-static", "arm64-windows"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Name != "x64-windows-static" {
		t.Fatalf("candidates = %+v", candidates)
	}
	reasons := strings.Join(candidates[0].Why, ",")
	if !strings.Contains(reasons, "static CRT") || !strings.Contains(reasons, "static libraries") {
		t.Fatalf("reasons = %v", candidates[0].Why)
	}
	// Equal scores are ordered by name so "auto" selection is reproducible.
	candidates, err = RankTriplets(TripletRequest{OS: "linux", Arch: "amd64", Available: []string{"x64-linuxb", "x64-linuxa"}})
	if err != nil || len(candidates) != 2 || candidates[0].Name != "x64-linuxa" {
		t.Fatalf("candidates = %+v, err = %v", candidates, err)
	}
	// A compiler match breaks the tie in favor of the matching triplet.
	candidates, err = RankTriplets(TripletRequest{OS: "windows", Arch: "amd64", Compiler: "clang", Available: []string{"x64-windows", "x64-windows-clang"}})
	if err != nil || len(candidates) != 2 || candidates[0].Name != "x64-windows-clang" {
		t.Fatalf("candidates = %+v, err = %v", candidates, err)
	}
}

func TestSelectAutoTripletPicksUniqueMatch(t *testing.T) {
	name, err := SelectAutoTriplet(TripletRequest{OS: "linux", Arch: "amd64", Available: []string{"x64-linux", "arm64-linux"}})
	if err != nil || name != "x64-linux" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	if _, err := SelectAutoTriplet(TripletRequest{OS: "linux", Arch: "amd64", Available: []string{"arm64-linux"}}); err == nil || !strings.Contains(err.Error(), "E_VCPKG_TRIPLET") {
		t.Fatalf("err = %v", err)
	}
}

func TestAvailableTripletsListsInstalledTrees(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"x64-windows", "x64-linux", "auto"} {
		if err := os.MkdirAll(filepath.Join(root, "installed", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	triplets, err := AvailableTriplets(root)
	if err != nil {
		t.Fatalf("triplets: %v", err)
	}
	if strings.Join(triplets, ",") != "x64-linux,x64-windows" {
		t.Fatalf("triplets = %v", triplets)
	}
	empty, err := AvailableTriplets(filepath.Join(root, "missing"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing root = %v, err = %v", empty, err)
	}
}
