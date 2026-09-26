package app

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"trestle/internal/config"
	"trestle/internal/graph"
)

func Package(path, output string) error {
	if output != "" {
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		cfg.Package.Output = output
		if err := config.Save(path, cfg); err != nil {
			return err
		}
	}
	if err := Build(path); err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	targets := cfg.Package.Targets
	if len(targets) == 0 {
		targets = cfg.Build.DefaultTargets
	}
	return packageSelected(path, targets, nil)
}

func packageSelected(path string, targets []string, progress func(string)) error {
	result, err := Generate(path)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	project, err := graph.Resolve(cfg)
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	output := cfg.Package.Output
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	defer archive.Close()
	manifestDir := filepath.Dir(result.Manifest)
	wanted := map[string]bool{}
	for _, name := range targets {
		wanted[name] = true
	}
	count := 0
	for _, target := range project.Targets {
		if !wanted[string(target.ID)] {
			continue
		}
		rel, ok := result.Outputs[target.ID]
		if !ok {
			continue
		}
		outputPath := filepath.Join(manifestDir, rel)
		if err := addFile(archive, outputPath, filepath.Join("bin", filepath.Base(outputPath))); err != nil {
			return err
		}
		count++
		for _, runtime := range target.LinkSelf.RuntimeFiles {
			if !filepath.IsAbs(runtime) {
				runtime = filepath.Join(root, runtime)
			}
			if err := addFile(archive, runtime, filepath.Join("bin", filepath.Base(runtime))); err != nil {
				return err
			}
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress(fmt.Sprintf("Release ZIP: %s · %d target artifacts", output, count))
	}
	return nil
}

func addFile(archive *zip.Writer, source, name string) error {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	writer, err := archive.Create(filepath.ToSlash(name))
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}
