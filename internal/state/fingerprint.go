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

type Generation struct {
	Key       GenerationKey       `json:"key"`
	Hash      string              `json:"hash"`
	Sources   []string            `json:"sources"`
	Toolchain toolchain.Toolchain `json:"toolchain"`
}

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

func (key GenerationKey) Hash() string {
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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
