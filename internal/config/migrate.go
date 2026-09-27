package config

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"
)

type migration func(map[string]any) error

var migrations = map[int]migration{
	1: migrateV1ToV2,
	2: migrateV2ToV3,
	3: migrateV3ToV4,
	4: migrateV4ToV5,
	5: migrateV5ToV6,
}

func migrateV5ToV6(document map[string]any) error { return nil }

func Migrate(data []byte) ([]byte, error) {
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	version := 0
	switch value := document["schema_version"].(type) {
	case int64:
		version = int(value)
	case int:
		version = value
	case float64:
		version = int(value)
	}
	if version == 0 {
		return nil, fmt.Errorf("E_CONFIG_MISSING_REQUIRED: schema_version is required")
	}
	if version > CurrentSchemaVersion {
		return nil, fmt.Errorf("E_CONFIG_NEWER_SCHEMA: project requires schema %d; this executable supports up to %d", version, CurrentSchemaVersion)
	}
	if version < 1 {
		return nil, fmt.Errorf("E_CONFIG_OLD_SCHEMA: schema %d is not supported", version)
	}
	for current := version; current < CurrentSchemaVersion; current++ {
		migrate, ok := migrations[current]
		if !ok {
			return nil, fmt.Errorf("E_CONFIG_MIGRATION: missing migration from schema %d", current)
		}
		if err := migrate(document); err != nil {
			return nil, fmt.Errorf("migrate schema %d to %d: %w", current, current+1, err)
		}
		document["schema_version"] = int64(current + 1)
	}
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func migrateV1ToV2(document map[string]any) error {
	build, ok := document["build"].(map[string]any)
	if !ok {
		build = map[string]any{}
		document["build"] = build
	}
	if _, exists := build["build_dir"]; !exists {
		build["build_dir"] = "build"
	}
	if _, exists := build["profile"]; !exists {
		build["profile"] = "debug"
	}
	toolchain, ok := document["toolchain"].(map[string]any)
	if !ok {
		toolchain = map[string]any{}
		document["toolchain"] = toolchain
	}
	if _, exists := toolchain["cxx"]; !exists {
		toolchain["cxx"] = "auto"
	}
	return nil
}

func migrateV2ToV3(document map[string]any) error {
	project, ok := document["project"].(map[string]any)
	if !ok {
		return fmt.Errorf("project table is missing")
	}
	if _, exists := project["name"]; !exists {
		return fmt.Errorf("project.name is missing")
	}
	return nil
}

func migrateV3ToV4(document map[string]any) error {
	build, ok := document["build"].(map[string]any)
	if !ok {
		build = map[string]any{}
		document["build"] = build
	}
	if _, exists := build["compile_commands"]; !exists {
		build["compile_commands"] = "compile_commands.json"
	}
	toolchain, ok := document["toolchain"].(map[string]any)
	if !ok {
		toolchain = map[string]any{}
		document["toolchain"] = toolchain
	}
	if _, exists := toolchain["mode"]; !exists {
		toolchain["mode"] = "native"
	}
	if _, exists := document["compiler_presets"]; !exists {
		document["compiler_presets"] = map[string]any{}
	}
	return nil
}

func migrateV4ToV5(document map[string]any) error {
	toolchain, ok := document["toolchain"].(map[string]any)
	if !ok {
		toolchain = map[string]any{}
		document["toolchain"] = toolchain
	}
	if _, exists := toolchain["cuda_execution"]; !exists {
		toolchain["cuda_execution"] = "native"
	}
	if _, exists := toolchain["vulkan_execution"]; !exists {
		toolchain["vulkan_execution"] = "native"
	}
	return nil
}
