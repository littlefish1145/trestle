// Package state records the inputs of a generation run so a later build can tell
// whether the generated build manifest is still up to date.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/model"
	"trestle/internal/toolchain"
)

// GenerationKey captures every input that affects the generated build manifest:
// the normalized configuration, source list, toolchain, and dependency and
// module hashes.
type GenerationKey struct {
	GeneratorVersion string
	SchemaVersion    int
	NormalizedConfig string
	Profile          string
	SourcePaths      []string
	Toolchain        toolchain.Toolchain
	PackageHash      string
	SDKHash          string
	ModuleGraphHash  string
}

// Generation is the persisted record of the inputs and hash of the last
// successful manifest generation.
type Generation struct {
	Key       GenerationKey       `json:"key"`
	Hash      string              `json:"hash"`
	Sources   []string            `json:"sources"`
	Toolchain toolchain.Toolchain `json:"toolchain"`
}

// BuildKey derives the generation key for a resolved project, normalizing the
// configuration and sorting the collected source paths so equal inputs produce
// equal keys.
func BuildKey(cfg config.Config, project model.ResolvedProject, compiler toolchain.Toolchain, packageHash, sdkHash, moduleHash string) GenerationKey {
	sources := make([]string, 0)
	for _, target := range project.Targets {
		sources = append(sources, target.Sources...)
	}
	sort.Strings(sources)
	normalized := struct {
		Project   config.Project
		Build     config.Build
		Toolchain config.Toolchain
		Targets   map[string]config.Target
		Packages  map[string]config.Package
	}{cfg.Project, cfg.Build, cfg.Toolchain, cfg.Targets, cfg.Packages}
	data, _ := json.Marshal(normalized)
	return GenerationKey{GeneratorVersion: "trestle-v3", SchemaVersion: cfg.SchemaVersion, NormalizedConfig: string(data), Profile: cfg.Build.Profile, SourcePaths: sources, Toolchain: compiler, PackageHash: packageHash, SDKHash: sdkHash, ModuleGraphHash: moduleHash}
}

// Hash returns the SHA-256 of the JSON-encoded key, used as the generation
// identity stored beside the manifest.
func (key GenerationKey) Hash() string {
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Load reads a generation record from path, returning a zero Generation when the
// file does not exist.
func Load(path string) (Generation, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Generation{}, nil
	}
	if err != nil {
		return Generation{}, err
	}
	var generation Generation
	if err := json.Unmarshal(data, &generation); err != nil {
		return Generation{}, err
	}
	return generation, nil
}

// Save writes the generation record to path, creating parent directories and
// replacing the file atomically.
func Save(path string, generation Generation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(generation, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(path, data)
}
