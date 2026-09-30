package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/diag"
	"trestle/internal/fsx"
	cmakeimport "trestle/internal/importer/cmake"
	"trestle/internal/policy"
	"trestle/internal/processx"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

type operationKey struct{}
type resourcesKey struct{}
type configurationKey struct{}

// Nested generation, execution and archiving share the same resolved snapshot.
func operationConfig(ctx context.Context, path string) (config.Config, error) {
	if cfg, ok := ctx.Value(configurationKey{}).(config.Config); ok {
		return cfg, nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return cfg, err
	}
	return policy.ApplyAt(ctx, cfg, filepath.Dir(path))
}

func ImportCMake(ctx context.Context, path string, progress func(string)) (cmakeimport.Result, error) {
	var result cmakeimport.Result
	err := runOperation(ctx, path, "import cmake", progress, func(ctx context.Context, emit func(string)) error {
		ctx, release, err := acquireResource(ctx, filepath.Join(filepath.Dir(path), ".trestle", "import"), "import cmake", false)
		if err != nil {
			return err
		}
		defer release()
		result, err = importCMake(ctx, path, emit)
		return err
	})
	return result, err
}

func runOperation(ctx context.Context, path, label string, progress func(string), action func(context.Context, func(string)) error) error {
	if ctx.Value(operationKey{}) != nil {
		return action(ctx, progress)
	}
	run, err := runlog.Start(path, label)
	if err != nil {
		return runlog.LogError(filepath.Join(filepath.Dir(path), ".trestle", "runs"), err)
	}
	child, cancel := context.WithCancel(context.WithValue(ctx, operationKey{}, run))
	defer cancel()
	emit := func(line string) {
		if _, err := run.Write([]byte(line + "\n")); err != nil {
			cancel()
			return
		}
		if progress != nil {
			progress(line)
		}
	}
	emit("Operation: " + label + "\nLog: " + run.Record.LogPath)
	child = processx.WithOutput(child, operationOutput{run, cancel}, progress)
	err = action(child, emit)
	if logErr := run.Err(); logErr != nil {
		err = runlog.LogError(run.Record.LogPath, logErr)
	}
	if err != nil {
		structured := diag.Enrich(err, diag.StageBuild)
		structured.LogPath = run.Record.LogPath
		err = structured
		emit(diag.Text(err))
	}
	if finishErr := run.Finish(err); finishErr != nil {
		return errors.Join(runlog.LogError(run.Record.LogPath, finishErr), err)
	}
	if progress != nil {
		progress("Log saved: " + run.Record.LogPath)
	}
	return err
}

type operationOutput struct {
	run    *runlog.Run
	cancel context.CancelFunc
}

func (output operationOutput) Write(data []byte) (int, error) {
	n, err := output.run.Write(data)
	if err != nil {
		output.cancel()
	}
	return n, err
}

// Context ownership allows release/build/generate to share a lock without reacquiring it.
func acquireResource(ctx context.Context, path, label string, shared bool) (context.Context, func(), error) {
	canonical, err := fsx.Canonical(path)
	if err != nil {
		return ctx, nil, err
	}
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	held, _ := ctx.Value(resourcesKey{}).(map[string]bool)
	if held[canonical] {
		return ctx, func() {}, nil
	}
	var lock *fsx.Lock
	if shared {
		lock, err = fsx.TryReadLock(ctx, canonical, label)
	} else {
		lock, err = fsx.TryLock(ctx, canonical, label)
	}
	if err != nil {
		return ctx, nil, err
	}
	copy := map[string]bool{}
	for key, value := range held {
		copy[key] = value
	}
	copy[canonical] = true
	return context.WithValue(ctx, resourcesKey{}, copy), lock.Close, nil
}

func buildResources(ctx context.Context, cfg config.Config, label string) (context.Context, func(), error) {
	child, release, err := acquireResource(ctx, filepath.Join(cfg.Build.BuildDir, cfg.Build.Profile), label, false)
	if err != nil {
		return ctx, nil, err
	}
	vcpkgRoot := cfg.Vcpkg.Root
	if vcpkgRoot == "" {
		vcpkgRoot = os.Getenv("VCPKG_ROOT")
	}
	if vcpkgRoot != "" && len(cfg.Packages) > 0 {
		var unlock func()
		child, unlock, err = acquireResource(child, filepath.Join(vcpkgRoot, "installed"), label+" dependency reader", true)
		if err != nil {
			release()
			return ctx, nil, err
		}
		return child, func() { unlock(); release() }, nil
	}
	return child, release, nil
}

func BuildTargetsWithOptions(ctx context.Context, path string, targets []string, all, force bool, progress func(string)) error {
	return runOperation(ctx, path, "build", progress, func(ctx context.Context, emit func(string)) error {
		return buildTargetsWithOptions(ctx, path, targets, all, force, emit)
	})
}
func RunTests(ctx context.Context, path string, selection TestSelection, progress func(string)) error {
	return runOperation(ctx, path, "test", progress, func(ctx context.Context, emit func(string)) error { return runTests(ctx, path, selection, emit) })
}
func InstallPackage(ctx context.Context, path, name string, progress func(string)) error {
	return runOperation(ctx, path, "install "+name, progress, func(ctx context.Context, emit func(string)) error { return installPackage(ctx, path, name, emit) })
}
func ExecuteTaskPreview(ctx context.Context, path, name string, preview config.TaskPreview, progress func(string)) error {
	return runOperation(ctx, path, "task "+name, progress, func(ctx context.Context, emit func(string)) error {
		ctx, release, err := acquireResource(ctx, filepath.Join(filepath.Dir(path), ".trestle", "tasks", fsx.Hash(name)), "task "+name, false)
		if err != nil {
			return err
		}
		defer release()
		return executeTaskPreview(ctx, path, name, preview, emit)
	})
}
func ReleaseWithProgress(ctx context.Context, path string, progress func(string)) error {
	return runOperation(ctx, path, "release", progress, func(ctx context.Context, emit func(string)) error { return releaseWithProgress(ctx, path, emit) })
}

func checkPackagePlatform(cfg config.Config) error {
	if cfg.Toolchain.Mode != "wsl" {
		return nil
	}
	for name, pkg := range cfg.Packages {
		triplet := pkg.Triplet
		if triplet == "" || triplet == "auto" {
			triplet = cfg.Vcpkg.Triplet
		}
		if strings.Contains(strings.ToLower(triplet), "windows") || strings.Contains(strings.ToLower(triplet), "osx") {
			return fmt.Errorf("E_VCPKG_TARGET_MISMATCH: package %s uses %s but WSL builds Linux binaries; install a matching Linux triplet or run Linux Trestle inside the distribution", name, triplet)
		}
	}
	return nil
}

func checkPackageTarget(cfg config.Config, tc toolchain.Toolchain) error {
	osName, arch := toolchain.TargetPlatform(tc)
	prefix := vcpkg.TripletFor(osName, arch)
	if toolchain.TargetABI(tc) == "mingw" {
		prefix = strings.Split(prefix, "-")[0] + "-mingw"
	}
	for name, pkg := range cfg.Packages {
		if pkg.Provider != "vcpkg" {
			continue
		}
		triplet := pkg.Triplet
		if triplet == "" || triplet == "auto" {
			triplet = cfg.Vcpkg.Triplet
		}
		lower := strings.ToLower(triplet)
		known := strings.Contains(lower, "-windows") || strings.Contains(lower, "-mingw") || strings.Contains(lower, "-linux") || strings.Contains(lower, "-osx")
		if !known {
			continue
		}
		if !strings.HasPrefix(lower, prefix) {
			return fmt.Errorf("E_VCPKG_TARGET_MISMATCH: package %s triplet %s does not match target %s/%s (%s); reinstall for %s or explicitly configure the matching compiler ABI", name, triplet, osName, arch, tc.CXX, prefix)
		}
	}
	return nil
}
