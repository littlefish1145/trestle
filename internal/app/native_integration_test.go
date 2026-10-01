package app

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"trestle/internal/config"
	"trestle/internal/diag"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

func integrationCompiler() string {
	if value := os.Getenv("TRESTLE_TEST_COMPILER"); value != "" {
		return value
	}
	if runtime.GOOS == "windows" {
		return "clang-cl"
	}
	if runtime.GOOS == "darwin" {
		return "clang++"
	}
	return "g++"
}

func integrationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	structured := diag.Enrich(err, diag.StageBuild)
	data, _ := os.ReadFile(structured.LogPath)
	t.Fatalf("%s\n%s", diag.Text(err), data)
}

func TestRealNativeCPPBuild(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_NATIVE") != "1" {
		t.Skip("set TRESTLE_TEST_NATIVE=1 to require an actual native C++ build and run")
	}
	root := t.TempDir()
	if err := Init(root, "hello-native"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, config.DefaultFileName)
	if err := Configure(path, integrationCompiler(), "", "debug", "", "", "", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.cpp"), []byte("#include <iostream>\nint main(){ std::cout << \"native OK\\n\"; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithProgress(context.Background(), path, nil); err != nil {
		integrationError(t, err)
	}
	result, err := GenerateWithContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(filepath.Join(filepath.Dir(result.Manifest), result.Outputs["app"])).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "native OK" {
		t.Fatalf("run: %s %v", output, err)
	}
	records, err := runlog.List(path)
	if err != nil || len(records) != 1 || records[0].Status != "succeeded" {
		t.Fatalf("history: %+v %v", records, err)
	}
	// Packaging must rebuild its own selection, even when an older artifact
	// already exists and the default build selects a different target.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Targets["tool"] = config.Target{Type: "executable", Sources: []string{"src/tool.cpp"}, OutputName: "package-tool"}
	cfg.Package.Targets = []string{"tool"}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "src", "tool.cpp")
	if err := os.WriteFile(source, []byte("#include <iostream>\nint main(){ std::cout << \"stale package\\n\"; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BuildTargetsWithProgress(context.Background(), path, []string{"tool"}, false, nil); err != nil {
		integrationError(t, err)
	}
	if err := os.WriteFile(source, []byte("#include <iostream>\nint main(){ std::cout << \"package OK\\n\"; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PackageWithProgress(context.Background(), path, "", nil); err != nil {
		integrationError(t, err)
	}
	archive, err := zip.OpenReader(filepath.Join(root, "dist", "hello-native.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 {
		t.Fatalf("unexpected package entries: %d", len(archive.File))
	}
	entry := archive.File[0]
	wantName := "bin/package-tool"
	if runtime.GOOS == "windows" {
		wantName += ".exe"
	}
	if entry.Name != wantName {
		t.Fatalf("packaged the wrong target: %s; want %s", entry.Name, wantName)
	}
	if runtime.GOOS != "windows" && entry.Mode().Perm()&0111 == 0 {
		t.Fatalf("packaged binary has no executable permission: %v", entry.Mode())
	}
	file, err := entry.Open()
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read packaged binary: %v %v", readErr, closeErr)
	}
	extracted := filepath.Join(t.TempDir(), filepath.Base(entry.Name))
	if err := os.WriteFile(extracted, data, entry.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	output, err = exec.Command(extracted).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "package OK" {
		t.Fatalf("run packaged binary: %s %v", output, err)
	}
}

func TestFmtTutorial(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_FMT") != "1" {
		t.Skip("set TRESTLE_TEST_FMT=1 with VCPKG_ROOT and TRESTLE_TEST_TRIPLET for the fmt tutorial")
	}
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := Init(root, "hello-fmt"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "hello-fmt", "src", "main.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.cpp"), data, 0600); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join("..", "..", "examples", "hello-fmt", "trestle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	triplet := os.Getenv("TRESTLE_TEST_TRIPLET")
	if triplet == "" {
		t.Fatal("TRESTLE_TEST_TRIPLET is required")
	}
	if err := Configure(path, integrationCompiler(), "", "debug", os.Getenv("VCPKG_ROOT"), triplet, "", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithProgress(context.Background(), path, nil); err != nil {
		integrationError(t, err)
	}
	result, err := GenerateWithContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(filepath.Join(filepath.Dir(result.Manifest), result.Outputs["app"])).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "Hello from Trestle + fmt!" {
		t.Fatalf("run: %s %v", output, err)
	}
	if err := ReleaseWithProgress(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dist", "hello-fmt.zip")); err != nil {
		t.Fatal(err)
	}
}

func TestRealWSLBuild(t *testing.T) {
	if os.Getenv("TRESTLE_TEST_WSL") != "1" {
		t.Skip("real WSL validation disabled; set TRESTLE_TEST_WSL=1")
	}
	if runtime.GOOS != "windows" {
		t.Skip("Windows-driven WSL requires Windows; SKIP is not a pass")
	}
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Skip("WSL is not installed; SKIP is not a pass")
	}
	distro := os.Getenv("TRESTLE_WSL_DISTRIBUTION")
	if distro == "" {
		t.Skip("set TRESTLE_WSL_DISTRIBUTION; SKIP is not a pass")
	}
	output, err := exec.Command(wsl, "--list", "--quiet").Output()
	if err != nil {
		t.Skipf("WSL unavailable: %v; SKIP is not a pass", err)
	}
	found := false
	for _, name := range toolchain.WSLDistributions(output) {
		found = found || strings.EqualFold(name, distro)
	}
	if !found {
		t.Skipf("distribution %q is not installed; SKIP is not a pass", distro)
	}
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	if err := Init(root, "hello-wsl"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Toolchain.Mode, cfg.Toolchain.WSLDistribution, cfg.Toolchain.CXX = "wsl", distro, "/usr/bin/g++"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.cpp"), []byte("#include <iostream>\nint main(){ std::cout << \"WSL OK\\n\"; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithProgress(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "build", "debug", "bin", "hello-wsl")
	linux, err := toolchain.WSLPath(context.Background(), distro, program)
	if err != nil {
		t.Fatal(err)
	}
	output, err = exec.Command(wsl, "-d", distro, "--exec", linux).Output()
	if err != nil || strings.TrimSpace(string(output)) != "WSL OK" {
		t.Fatalf("run: %s %v", output, err)
	}
}
