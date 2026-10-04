package config

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/BurntSushi/toml"
	"trestle/internal/toolchain"
)

type migration func(map[string]any) error

var migrations = map[int]migration{
	1: migrateV1ToV2,
	2: migrateV2ToV3,
	3: migrateV3ToV4,
	4: migrateV4ToV5,
	5: migrateV5ToV6,
	6: migrateV6ToV7,
}

func migrateV5ToV6(document map[string]any) error { return nil }

// migrateV6ToV7 replaces machine-local toolchain paths with the portable
// version ranges of schema 7. Schema 6 stored the absolute path of whichever
// compiler happened to be installed, so a committed trestle.toml could not be
// used on another machine. Each path is rewritten into the constraint that
// describes it: an MSVC toolset directory becomes an msvc range, a CUDA toolkit
// root becomes a cuda range, and anything else falls back to "auto".
func migrateV6ToV7(document map[string]any) error {
	settings, ok := document["toolchain"].(map[string]any)
	if !ok {
		settings = map[string]any{}
		document["toolchain"] = settings
	}
	// Only write msvc when a toolset version was recoverable; an absent key is
	// normalized to "auto" without claiming a pin the project never had.
	if _, exists := settings["msvc"]; !exists {
		if recovered := msvcRangeFromPaths(settings); recovered != "" {
			settings["msvc"] = recovered
		}
	}
	if _, exists := settings["cache_dir"]; !exists {
		settings["cache_dir"] = DefaultToolchainCacheDir
	}
	for _, key := range []string{"c", "cxx"} {
		settings[key] = toolchain.PortableSelector(stringField(settings, key))
	}
	// archiver and linker are derivable from the resolved compiler, and setup is
	// derivable from the msvc range, so a stored absolute path is redundant.
	for _, key := range []string{"archiver", "linker"} {
		if toolchain.IsLocalPath(stringField(settings, key)) {
			settings[key] = "auto"
		}
	}
	if toolchain.IsLocalPath(stringField(settings, "setup")) {
		settings["setup"] = ""
	}
	migrateCUDARoot(settings)
	return nil
}

// coerceVersionFields lets version constraints be written unquoted, so
// cuda = 12.6 is accepted alongside cuda = "12.6". It runs before any migration
// so the new range syntax works at every schema version.
func coerceVersionFields(document map[string]any) {
	settings, ok := document["toolchain"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"msvc", "c", "cxx", "archiver", "linker", "setup", "cuda", "cuda_root", "cache_dir"} {
		if coerced, ok := coerceString(settings[key]); ok {
			settings[key] = coerced
		}
	}
}

func migrateCUDARoot(settings map[string]any) {
	root := stringField(settings, "cuda")
	if !toolchain.IsLocalPath(root) {
		return
	}
	if version, ok := toolchain.ToolkitVersionFromPath(root); ok {
		settings["cuda"] = version + "~" + version
		return
	}
	// The version could not be recovered from the layout alone; keep the root as
	// an explicit override so the project still builds, and let "auto" pick up any
	// toolkit the machine does have.
	settings["cuda"] = "auto"
	settings["cuda_root"] = root
}

// msvcRangeFromPaths recovers the VCTOOLSVERSION that an archiver, linker, or
// environment script belonged to, so the constraint survives losing the path.
func msvcRangeFromPaths(settings map[string]any) string {
	for _, key := range []string{"archiver", "linker", "setup", "cxx", "c"} {
		value := stringField(settings, key)
		if !toolchain.IsLocalPath(value) {
			continue
		}
		if version, ok := toolchain.MSVCToolsetVersion(value); ok {
			return version + "~" + version
		}
	}
	return ""
}

func stringField(settings map[string]any, key string) string {
	value, _ := coerceString(settings[key])
	return value
}

// coerceString lets version constraints be written unquoted, so cuda = 12.4 is
// accepted alongside cuda = "12.4".
func coerceString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}
	return "", false
}

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
	coerceVersionFields(document)
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
