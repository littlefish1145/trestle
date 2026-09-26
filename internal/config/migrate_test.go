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
	if !strings.Contains(string(migrated), "schema_version = 4") || !strings.Contains(string(migrated), "[build]") {
		t.Fatalf("unexpected migration: %s", migrated)
	}
}
