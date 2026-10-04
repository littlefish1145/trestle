package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateV1(t *testing.T) {
	input := "schema_version = 1\n[project]\nname = \"demo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), "schema_version = 7") || !strings.Contains(string(migrated), "[build]") || !strings.Contains(string(migrated), "cuda_execution = \"native\"") {
		t.Fatalf("unexpected migration: %s", migrated)
	}
}

func TestMigrateV4AddsSDKExecutionModes(t *testing.T) {
	input := "schema_version = 4\n[project]\nname = \"demo\"\n[toolchain]\ncuda = \"D:/CUDA\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(migrated)
	if !strings.Contains(text, "schema_version = 7") || !strings.Contains(text, "cuda_execution = \"native\"") || !strings.Contains(text, "vulkan_execution = \"native\"") {
		t.Fatalf("unexpected migration: %s", migrated)
	}
}

// A toolkit root whose version cannot be read from the layout keeps working
// through an explicit override instead of being guessed at.
func TestMigrateV6KeepsUnversionedCUDARootAsOverride(t *testing.T) {
	input := "schema_version = 6\n[project]\nname = \"demo\"\n[toolchain]\ncuda = \"D:/CUDA\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(migrated)
	if !strings.Contains(text, "cuda = \"auto\"") || !strings.Contains(text, "cuda_root = \"D:/CUDA\"") {
		t.Fatalf("unversioned toolkit root was not preserved: %s", migrated)
	}
}

// Schema 6 stored the absolute path of one machine's tools. Schema 7 must turn
// those into portable version constraints so a committed file stays usable.
func TestMigrateV6ReplacesMachineLocalToolchainPaths(t *testing.T) {
	input := `schema_version = 6
[project]
name = "demo"
[toolchain]
c = "D:/LLVM/bin/clang.exe"
cxx = "D:/LLVM/bin/clang++.exe"
archiver = "D:/vs2022/VC/Tools/MSVC/14.44.35207/bin/HostX64/x64/lib.exe"
linker = "D:/LLVM/bin/clang++.exe"
setup = "D:/vs2022/VC/Auxiliary/Build/vcvars64.bat"
cuda = "C:/Program Files/NVIDIA GPU Computing Toolkit/CUDA/v12.6"
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
`
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(migrated)
	for _, leaked := range []string{"D:/LLVM", "D:/vs2022", "C:/Program Files/NVIDIA"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("migration leaked %q: %s", leaked, migrated)
		}
	}
	for _, expected := range []string{
		"schema_version = 7",
		"msvc = \"14.44.35207~14.44.35207\"",
		"c = \"clang\"",
		"cxx = \"clang++\"",
		"archiver = \"auto\"",
		"linker = \"auto\"",
		"cuda = \"12.6~12.6\"",
		"cache_dir = \".trestle/toolchain\"",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("migration is missing %q: %s", expected, migrated)
		}
	}
	if !strings.Contains(text, `setup = ""`) {
		t.Fatalf("environment script survived migration: %s", migrated)
	}
}

// An explicit msvc range written by hand must not be overwritten by a value
// recovered from a leftover path.
func TestMigrateV6KeepsExplicitMSVCRange(t *testing.T) {
	input := "schema_version = 6\n[project]\nname = \"demo\"\n[toolchain]\nmsvc = \"14.3~14.5\"\narchiver = \"D:/vs2022/VC/Tools/MSVC/14.44.35207/bin/HostX64/x64/lib.exe\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), "msvc = \"14.3~14.5\"") {
		t.Fatalf("explicit msvc range was replaced: %s", migrated)
	}
}

// Version constraints may be written unquoted, so cuda = 12.4 is accepted.
func TestMigrateV6AcceptsUnquotedVersion(t *testing.T) {
	input := "schema_version = 6\n[project]\nname = \"demo\"\n[toolchain]\ncuda = 12.4\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), `cuda = "12.4"`) {
		t.Fatalf("unquoted version was not coerced: %s", migrated)
	}
	cfg, err := Decode(filepath.Join(t.TempDir(), DefaultFileName), migrated)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.CUDA != "12.4" {
		t.Fatalf("cuda = %q", cfg.Toolchain.CUDA)
	}
}

// A toolkit installed under a plain directory name still yields a range when it
// ships version.json.
func TestMigrateV6ReadsToolkitVersionJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CUDA")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version.json"), []byte(`{"cuda":{"name":"12.4.131"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input := "schema_version = 6\n[project]\nname = \"demo\"\n[toolchain]\ncuda = \"" + filepath.ToSlash(root) + "\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), `cuda = "12.4.131~12.4.131"`) {
		t.Fatalf("version.json was not used: %s", migrated)
	}
	if strings.Contains(string(migrated), "cuda_root") {
		t.Fatalf("a resolved toolkit must not leave a machine path: %s", migrated)
	}
}
