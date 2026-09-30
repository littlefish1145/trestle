package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcurrentSnapshotsCannotLoseUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	if err := Save(path, Default("demo")); err != nil {
		t.Fatal(err)
	}
	a, _ := Load(path)
	b, _ := Load(path)
	a.Build.Profile = "release"
	if err := Save(path, a); err != nil {
		t.Fatal(err)
	}
	b.Project.Name = "stale"
	if err := Save(path, b); err == nil || !strings.Contains(err.Error(), "E_CONFIG_CONFLICT") {
		t.Fatalf("stale update accepted: %v", err)
	}
	latest, err := Load(path)
	if err != nil || latest.Build.Profile != "release" || latest.Project.Name != "demo" {
		t.Fatalf("update lost: %#v %v", latest, err)
	}
}

func TestMigrationBackupAndInvalidSavePreserveOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trestle.toml")
	original := []byte("schema_version = 1\n[project]\nname = \"demo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("read mutated configuration")
	}
	invalid := cfg
	invalid.Project.Name = ""
	if err := Save(path, invalid); err == nil {
		t.Fatal("invalid save succeeded")
	}
	data, _ = os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("invalid save overwrote original")
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".schema-v1.*.bak")
	if len(backups) != 1 {
		t.Fatalf("missing schema backup: %v", backups)
	}
	data, _ = os.ReadFile(backups[0])
	if !bytes.Equal(data, original) {
		t.Fatal("backup is not original bytes")
	}
}

func TestFailedMigrationBackupPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	data := []byte("schema_version = 1\n[project]\nname = \"demo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	backup := path + ".schema-v1." + Fingerprint(data)[:12] + ".bak"
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err == nil {
		t.Fatal("saved migration without a valid backup")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) {
		t.Fatal("failed backup replaced original")
	}
}

func TestRelativeConfigurationDirectoryDoesNotAccumulatePrefix(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("project", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("project", DefaultFileName)
	if err := Save(path, Default("demo")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Build.BuildDir != filepath.Join(root, "project", "build") {
			t.Fatalf("duplicated prefix: %s", cfg.Build.BuildDir)
		}
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigAliasPreservesSymlinkAndFingerprint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DefaultFileName)
	alias := filepath.Join(root, "alias.toml")
	if err := Save(path, Default("demo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	stale, err := Load(alias)
	if err != nil {
		t.Fatal(err)
	}
	current, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	current.Build.Profile = "release"
	if err := Save(alias, current); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(alias)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v", err)
	}
	stale.Project.Name = "stale"
	if err := Save(path, stale); err == nil {
		t.Fatal("alias bypassed conflict detection")
	}
}
