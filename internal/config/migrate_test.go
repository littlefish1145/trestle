package config

import (
	"strings"
	"testing"
)

func TestMigrateV1(t *testing.T) {
	input := "schema_version = 1\n[project]\nname = \"demo\"\n[targets.app]\ntype = \"executable\"\nsources = [\"src/main.cpp\"]\n"
	migrated, err := Migrate([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), "schema_version = 6") || !strings.Contains(string(migrated), "[build]") || !strings.Contains(string(migrated), "cuda_execution = \"native\"") {
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
	if !strings.Contains(text, "schema_version = 6") || !strings.Contains(text, "cuda_execution = \"native\"") || !strings.Contains(text, "vulkan_execution = \"native\"") {
		t.Fatalf("unexpected migration: %s", migrated)
	}
}
