package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	cmakeexport "trestle/internal/exporter/cmake"
	"trestle/internal/fsx"
)

// ExportCMake reverse-exports trestle.toml into a CMakeLists.txt. The
// operation is experimental: it renders the declarative build model and keeps
// Trestle-only sections as comments. An existing file is only replaced when
// force is set, because CMakeLists.txt is often handwritten.
func ExportCMake(ctx context.Context, path, output string, force bool, progress func(string)) (cmakeexport.Result, error) {
	var result cmakeexport.Result
	err := runOperation(ctx, path, "export cmake", progress, func(ctx context.Context, emit func(string)) error {
		ctx, release, err := acquireResource(ctx, filepath.Join(filepath.Dir(path), ".trestle", "export"), "export cmake", false)
		if err != nil {
			return err
		}
		defer release()
		cfg, err := operationConfig(ctx, path)
		if err != nil {
			return err
		}
		target := output
		if target == "" {
			target = "CMakeLists.txt"
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if !force {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("%s already exists; pass -force to overwrite it", filepath.Base(target))
			}
		}
		result, err = cmakeexport.Generate(cfg, cmakeexport.Options{Root: filepath.Dir(path)})
		if err != nil {
			return err
		}
		for _, warning := range result.Warnings {
			emit("Warning: " + warning)
		}
		if err := fsx.AtomicWrite(target, []byte(result.Content)); err != nil {
			return err
		}
		emit(fmt.Sprintf("Exported %d target(s) to %s", result.Targets, target))
		return nil
	})
	return result, err
}
