package app

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"trestle/internal/config"
	"trestle/internal/fsx"
	"trestle/internal/graph"
)

func Package(path, output string) error {
	return PackageWithProgress(context.Background(), path, output, func(line string) { fmt.Println(line) })
}

func PackageWithProgress(ctx context.Context, path, output string, progress func(string)) error {
	return runOperation(ctx, path, "package", progress, func(ctx context.Context, emit func(string)) error {
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
		cfg, err := operationConfig(ctx, path)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, configurationKey{}, cfg)
		ctx, release, err := buildResources(ctx, cfg, "package")
		if err != nil {
			return err
		}
		defer release()
		ctx, releaseOutput, err := acquireResource(ctx, cfg.Package.Output, "package archive", false)
		if err != nil {
			return err
		}
		defer releaseOutput()
		if err := BuildWithProgress(ctx, path, emit); err != nil {
			return err
		}
		targets := cfg.Package.Targets
		if len(targets) == 0 {
			targets = cfg.Build.DefaultTargets
		}
		return packageSelectedWithContext(ctx, path, targets, emit)
	})
}

func packageSelected(path string, targets []string, progress func(string)) error {
	return packageSelectedWithContext(context.Background(), path, targets, progress)
}

func packageSelectedWithContext(ctx context.Context, path string, targets []string, progress func(string)) error {
	cfg, err := operationConfig(ctx, path)
	if err != nil {
		return err
	}
	ctx, release, err := buildResources(ctx, cfg, "archive")
	if err != nil {
		return err
	}
	defer release()
	_, releaseOutput, err := acquireResource(ctx, cfg.Package.Output, "archive", false)
	if err != nil {
		return err
	}
	defer releaseOutput()
	result, err := generateWithConfig(ctx, path, cfg)
	if err != nil {
		return err
	}
	project, err := graph.ResolveAt(ctx, cfg, filepath.Dir(path))
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
	file, err := os.CreateTemp(filepath.Dir(output), ".trestle-archive-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	archive := zip.NewWriter(file)
	defer archive.Close()
	manifestDir := filepath.Dir(result.Manifest)
	wanted := map[string]bool{}
	for _, name := range targets {
		wanted[name] = true
	}
	count := 0
	for _, target := range project.Targets {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	if count == 0 {
		return fmt.Errorf("E_PACKAGE_EMPTY: no selected artifacts; build the requested targets before packaging")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := fsx.Replace(file.Name(), output); err != nil {
		return err
	}
	if progress != nil {
		progress(fmt.Sprintf("Release ZIP: %s · %d target artifacts", output, count))
	}
	return nil
}

func addFile(archive *zip.Writer, source, name string) error {
	if _, err := os.Stat(source); err != nil {
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
