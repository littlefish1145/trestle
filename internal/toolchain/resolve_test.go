package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeVisualStudio lays out the directory names discovery relies on without
// running any toolchain, and pins discovery to that tree so the machine's own
// Visual Studio installation cannot leak into the assertions.
func fakeVisualStudio(t *testing.T, toolsets ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vs2022")
	for _, toolset := range toolsets {
		compiler := filepath.Join(root, "VC", "Tools", "MSVC", toolset, "bin", "Hostx64", "x64", executableName("cl"))
		if err := os.MkdirAll(filepath.Dir(compiler), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(compiler, []byte("stub"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "VC", "Auxiliary", "Build"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(VisualStudioRootsEnv, root)
	return root
}

func fakeCUDAToolkit(t *testing.T, directory, version string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), directory)
	nvcc := filepath.Join(root, "bin", executableName("nvcc"))
	if err := os.MkdirAll(filepath.Dir(nvcc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nvcc, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if version != "" {
		metadata := `{"cuda":{"name":"` + version + `"}}`
		if err := os.WriteFile(filepath.Join(root, "version.json"), []byte(metadata), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(CUDARootsEnv, root)
	return root
}

func TestDiscoverMSVCInstallsReadsToolsetDirectories(t *testing.T) {
	root := fakeVisualStudio(t, "14.30.30808", "14.44.35207", "not-a-version")
	installs := DiscoverMSVCInstalls(context.Background())
	if len(installs) != 2 {
		t.Fatalf("expected the two versioned toolsets, got %#v", installs)
	}
	if installs[0].Version != "14.44.35207" {
		t.Fatalf("toolsets must be reported newest first, got %q", installs[0].Version)
	}
	install := installs[0]
	if install.Root != root {
		t.Fatalf("root = %q, want %q", install.Root, root)
	}
	if want := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat"); install.Setup != want {
		t.Fatalf("setup = %q, want %q", install.Setup, want)
	}
	if filepath.Base(install.Compiler) != executableName("cl") {
		t.Fatalf("compiler = %q", install.Compiler)
	}
}

func TestDiscoverCUDAInstallsRecoversVersionFromMetadata(t *testing.T) {
	root := fakeCUDAToolkit(t, "CUDA", "12.4.131")
	installs := DiscoverCUDAInstalls(context.Background())
	if len(installs) != 1 {
		t.Fatalf("expected one toolkit, got %#v", installs)
	}
	if installs[0].Version != "12.4.131" || installs[0].Root != root {
		t.Fatalf("unexpected toolkit: %#v", installs[0])
	}
}

func TestDiscoverCUDAInstallsPrefersDirectoryNameOverMetadata(t *testing.T) {
	fakeCUDAToolkit(t, "v12.6", "12.4.131")
	installs := DiscoverCUDAInstalls(context.Background())
	if len(installs) != 1 || installs[0].Version != "12.6" {
		t.Fatalf("the directory name is authoritative: %#v", installs)
	}
}

func TestResolveMSVCPicksHighestToolsetInRange(t *testing.T) {
	fakeVisualStudio(t, "14.30.30808", "14.38.33130", "14.44.35207")
	inventory := Inventory{MSVC: DiscoverMSVCInstalls(context.Background())}
	if len(inventory.MSVC) != 3 {
		t.Fatalf("fixture should expose three toolsets, got %#v", inventory.MSVC)
	}

	install, err := ResolveMSVC(inventory, "14.30~14.40")
	if err != nil {
		t.Fatal(err)
	}
	if install.Version != "14.38.33130" {
		t.Fatalf("14.30~14.40 should select 14.38.33130, got %q", install.Version)
	}
	if install, err = ResolveMSVC(inventory, "auto"); err != nil || install.Version != "14.44.35207" {
		t.Fatalf("auto must select the newest toolset, got %q (%v)", install.Version, err)
	}
	if install, err = ResolveMSVC(inventory, ""); err != nil || install.Version != "14.44.35207" {
		t.Fatalf("an empty spec must select the newest toolset, got %q (%v)", install.Version, err)
	}
	if install, err = ResolveMSVC(inventory, "14.44.35207"); err != nil || install.Version != "14.44.35207" {
		t.Fatalf("a full-precision pin must match exactly, got %q (%v)", install.Version, err)
	}
}

func TestResolveMSVCReportsWhatIsInstalled(t *testing.T) {
	fakeVisualStudio(t, "14.44.35207")
	inventory := Inventory{MSVC: DiscoverMSVCInstalls(context.Background())}
	_, err := ResolveMSVC(inventory, "14.2~14.3")
	if err == nil {
		t.Fatal("an unsatisfiable range must fail")
	}
	for _, fragment := range []string{"E_MSVC_NOT_FOUND", "14.2~14.3", "14.44.35207", "[toolchain].msvc"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q should mention %q", err, fragment)
		}
	}
	if _, err := ResolveMSVC(inventory, "14.x"); err == nil {
		t.Fatal("a malformed range must fail before scanning")
	}
}

func TestResolveCUDAPicksHighestToolkitInRange(t *testing.T) {
	fakeCUDAToolkit(t, "v12.6", "12.6")
	inventory := Inventory{CUDA: DiscoverCUDAInstalls(context.Background())}
	if len(inventory.CUDA) != 1 {
		t.Fatalf("fixture should expose one toolkit, got %#v", inventory.CUDA)
	}
	install, err := ResolveCUDA(inventory, "12.0~12.9")
	if err != nil || install.Version != "12.6" {
		t.Fatalf("12.0~12.9 should select 12.6, got %q (%v)", install.Version, err)
	}
	if _, err := ResolveCUDA(inventory, "11.0~11.8"); err == nil {
		t.Fatal("an unsatisfiable range must fail")
	}
	if _, err := ResolveCUDA(inventory, "13.0~13.9"); err == nil || !strings.Contains(err.Error(), "E_CUDA_NOT_FOUND") {
		t.Fatalf("missing toolkits must report E_CUDA_NOT_FOUND, got %v", err)
	}
}

func TestResolveCompilerSelectsByVersionRange(t *testing.T) {
	inventory := Inventory{Compilers: []Component{
		{Name: "g++", Family: "MinGW", Path: `/usr/bin/g++`, Version: "g++ (GCC) 12.2.0", Ready: true},
		{Name: "clang-cl", Family: "Clang", Path: `D:\LLVM\bin\clang-cl.exe`, Version: "clang version 18.1.8", Ready: true},
		{Name: "not detected", Family: "Clang", Ready: false},
	}}
	got, err := ResolveCompiler(inventory, "18~20")
	if err != nil {
		t.Fatal(err)
	}
	if got != `D:\LLVM\bin\clang-cl.exe` {
		t.Fatalf("18~20 should select clang 18, got %q", got)
	}
	got, err = ResolveCompiler(inventory, "12~13")
	if err != nil || got != `/usr/bin/g++` {
		t.Fatalf("12~13 should select g++ 12, got %q (%v)", got, err)
	}
	if _, err := ResolveCompiler(inventory, "20~22"); err == nil || !strings.Contains(err.Error(), "E_TOOLCHAIN_NOT_FOUND") {
		t.Fatalf("an unsatisfiable compiler range must fail, got %v", err)
	}
}

func TestResolveCompilerLeavesNamesAndPathsUntouched(t *testing.T) {
	inventory := Inventory{}
	for _, spec := range []string{"", "auto", "clang-cl", `D:\LLVM\bin\clang++.exe`, "/usr/bin/g++"} {
		got, err := ResolveCompiler(inventory, spec)
		if err != nil {
			t.Fatalf("ResolveCompiler(%q): %v", spec, err)
		}
		want := spec
		if spec == "" {
			want = "auto"
		}
		if got != want {
			t.Fatalf("ResolveCompiler(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestResolveCUDARootPrefersExplicitRoot(t *testing.T) {
	fakeCUDAToolkit(t, "v12.6", "12.6")
	request := Request{CacheDir: t.TempDir(), CUDA: "12.0~12.9"}
	root, err := ResolveCUDARoot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "v12.6" {
		t.Fatalf("resolved root = %q", root)
	}
	override := filepath.Join(t.TempDir(), "pinned")
	request.CUDARoot = override
	if root, err = ResolveCUDARoot(context.Background(), request); err != nil || root != override {
		t.Fatalf("an explicit cuda_root must win, got %q (%v)", root, err)
	}
	request.CUDA, request.CUDARoot = "", ""
	if root, err = ResolveCUDARoot(context.Background(), request); err == nil || !strings.Contains(err.Error(), "E_CUDA_NOT_FOUND") {
		t.Fatalf("an unconfigured CUDA request must fail, got %v", err)
	}
}

func TestResolveMSVCSetupScriptOnlyForConstrainedRanges(t *testing.T) {
	root := fakeVisualStudio(t, "14.44.35207")
	cacheDir := t.TempDir()
	if got := ResolveMSVCSetupScript(context.Background(), cacheDir, "auto"); got != "" {
		t.Fatalf("an unconstrained range must not pin a setup script, got %q", got)
	}
	want := filepath.Join(root, "VC", "Auxiliary", "Build", "vcvars64.bat")
	if got := ResolveMSVCSetupScript(context.Background(), cacheDir, "14.30~14.50"); got != want {
		t.Fatalf("setup = %q, want %q", got, want)
	}
	if got := ResolveMSVCSetupScript(context.Background(), cacheDir, "14.1~14.2"); got != "" {
		t.Fatalf("an unsatisfiable range must not produce a script, got %q", got)
	}
}
