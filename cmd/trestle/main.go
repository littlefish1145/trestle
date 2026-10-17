// Package main is the trestle command-line entry point. With no arguments it
// opens the interactive project console; otherwise it dispatches the named
// subcommand to the app package.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"
	"trestle/internal/app"
	"trestle/internal/config"
	"trestle/internal/deps"
	"trestle/internal/diag"
	"trestle/internal/policy"
	"trestle/internal/runlog"
	tuimodel "trestle/internal/tui"
)

// Version metadata stamped with -ldflags at release time; the defaults identify a
// development build.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			usage()
			return
		}
		path, err := discoverConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := os.Chdir(filepath.Dir(path)); err != nil {
			return
		}
		path = filepath.Base(path)
		if err := tuimodel.RunDashboard(path, dashboardServices(path)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	command := os.Args[1]
	if command == "-h" || command == "--help" || command == "help" {
		usage()
		return
	}
	if command == "version" || command == "--version" {
		fmt.Printf("trestle %s (%s, %s)\n", version, commit, buildDate)
		return
	}
	if err := run(command, os.Args[2:]); err != nil {
		diag.Render(os.Stderr, err)
		os.Exit(1)
	}
}

func run(command string, args []string) (result error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var flags runFlags
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	registerRunFlags(set, command, &flags)
	if err := set.Parse(args); err != nil {
		return err
	}
	if command == "init" {
		return app.Init(filepath.Clean(flags.directory), flags.name)
	}
	if err := os.Chdir(flags.directory); err != nil {
		return err
	}
	if command == "analyze" {
		return app.AnalyzeProject(".", os.Stdout)
	}
	path := config.DefaultFileName
	defer func() {
		if result != nil {
			result = runlog.Failure(path, command, result)
		}
	}()
	return executeCommand(ctx, command, path, set.Args(), &flags)
}

// runFlags collects every flag the subcommands can define. Keeping them in one
// struct keeps the registration and dispatch code in sync and avoids a long list
// of bare flag pointers.
type runFlags struct {
	directory          string
	name               string
	compiler           string
	c                  string
	profile            string
	output             string
	vcpkgRoot          string
	triplet            string
	msvc               string
	cudaRoot           string
	cudaMode           string
	cudaExecution      string
	cudaDistribution   string
	vulkanRoot         string
	vulkanExecution    string
	vulkanDistribution string
	setup              string
	moduleScanner      string
	compileFlags       string
	cFlags             string
	cxxFlags           string
	linkFlags          string
	buildProfile       string
	mode               string
	distribution       string
	preset             string
	savePreset         string
	toolchainCache     string
	query              string
	portsText          string
	vcpkgTriplet       string
	testJob            string
	testGroup          string
	testFile           string
	releaseTargets     string
	install            bool
	refresh            bool
	testAll            bool
	force              bool
	interactive        bool
	modulesEnabled     bool
	dashboard          bool
}

// registerRunFlags defines the flags accepted by command, mirroring the help
// text each subcommand advertises.
func registerRunFlags(set *flag.FlagSet, command string, flags *runFlags) {
	set.StringVar(&flags.directory, "C", ".", "run from this directory")
	switch command {
	case "init":
		set.StringVar(&flags.name, "name", "", "project name")
	case "configure":
		set.StringVar(&flags.compiler, "toolchain", "", "compiler name or path")
		set.StringVar(&flags.c, "c", "", "C compiler name or path")
		set.StringVar(&flags.profile, "profile", "", "build profile")
		set.StringVar(&flags.compileFlags, "compile-flags", "", "comma-free compiler flags")
		set.StringVar(&flags.cFlags, "c-flags", "", "C-only compiler flags")
		set.StringVar(&flags.cxxFlags, "cxx-flags", "", "C++-only compiler flags")
		set.StringVar(&flags.linkFlags, "link-flags", "", "linker flags")
		set.StringVar(&flags.mode, "mode", "", "compiler execution mode: native or wsl")
		set.StringVar(&flags.distribution, "wsl-distribution", "", "WSL distribution name")
		set.StringVar(&flags.preset, "preset", "", "apply a compiler preset")
		set.StringVar(&flags.savePreset, "save-preset", "", "save the resulting compiler configuration")
		set.StringVar(&flags.vcpkgRoot, "vcpkg-root", "", "vcpkg installation root")
		set.StringVar(&flags.triplet, "triplet", "", "vcpkg triplet")
		set.StringVar(&flags.msvc, "msvc", "", "MSVC toolset version or range, such as 14.3~14.5")
		set.StringVar(&flags.cudaRoot, "cuda", "", "CUDA version range such as 12.0~12.9, or a toolkit root")
		set.StringVar(&flags.cudaMode, "cuda-mode", "", "whole or rdc")
		set.StringVar(&flags.cudaExecution, "cuda-execution", "", "CUDA execution mode: native or wsl")
		set.StringVar(&flags.cudaDistribution, "cuda-wsl-distribution", "", "WSL distribution containing CUDA")
		set.StringVar(&flags.vulkanRoot, "vulkan", "", "Vulkan SDK root")
		set.StringVar(&flags.vulkanExecution, "vulkan-execution", "", "Vulkan execution mode: native or wsl")
		set.StringVar(&flags.vulkanDistribution, "vulkan-wsl-distribution", "", "WSL distribution containing Vulkan SDK tools")
		set.BoolVar(&flags.interactive, "interactive", false, "use the configuration wizard")
		set.StringVar(&flags.setup, "setup", "", "vcvars64.bat or environment setup script")
		set.StringVar(&flags.moduleScanner, "module-scanner", "", "clang-scan-deps executable")
		set.BoolVar(&flags.modulesEnabled, "modules", false, "enable C++ module scanning")
	case "toolchain":
		set.StringVar(&flags.toolchainCache, "cache-dir", "", "directory for the cached toolchain inventory")
		set.BoolVar(&flags.refresh, "refresh", false, "rescan installed toolchains and refresh the cache")
	case "build":
		set.BoolVar(&flags.dashboard, "ui", false, "open the project console")
		set.StringVar(&flags.buildProfile, "profile", "", "debug or release")
		set.StringVar(&flags.preset, "preset", "", "apply a compiler preset")
		set.BoolVar(&flags.testAll, "all", false, "build every target")
		set.BoolVar(&flags.force, "force", false, "attempt unavailable targets anyway")
	case "test":
		set.StringVar(&flags.testJob, "job", "", "run one test job")
		set.StringVar(&flags.testGroup, "group", "", "run one test group")
		set.StringVar(&flags.testFile, "file", "", "run the test job owning a source file")
		set.BoolVar(&flags.testAll, "all", false, "run all test jobs")
	case "package":
		set.StringVar(&flags.output, "output", "", "package output path")
	case "release":
		set.StringVar(&flags.output, "output", "", "release ZIP output path")
		set.StringVar(&flags.preset, "optimization", "", "balanced, speed, size, or custom:<preset>")
		set.StringVar(&flags.releaseTargets, "targets", "", "comma-separated release targets")
	case "vcpkg":
		set.StringVar(&flags.vcpkgRoot, "root", "", "vcpkg root")
		set.StringVar(&flags.query, "search", "", "search ports")
		set.BoolVar(&flags.install, "install", false, "install ports")
		set.StringVar(&flags.portsText, "ports", "", "comma-separated ports")
		set.StringVar(&flags.vcpkgTriplet, "triplet", "", "vcpkg triplet")
	case "export-cmake":
		set.StringVar(&flags.output, "output", "CMakeLists.txt", "output path relative to the project")
		set.BoolVar(&flags.force, "force", false, "overwrite an existing CMakeLists.txt")
	}
}

// executeCommand dispatches a parsed subcommand to the app package. args holds
// the positional arguments left after flag parsing.
func executeCommand(ctx context.Context, command, path string, args []string, flags *runFlags) error {
	switch command {
	case "tui":
		return tuimodel.RunDashboard(path, dashboardServices(path))
	case "configure":
		return configureProject(path, flags)
	case "build":
		return buildProject(ctx, path, args, flags)
	case "test":
		selection := app.TestSelection{
			Job:   flags.testJob,
			Group: flags.testGroup,
			File:  flags.testFile,
			All:   flags.testAll || (flags.testJob == "" && flags.testGroup == "" && flags.testFile == ""),
		}
		return app.RunTests(ctx, path, selection, progressPrinter)
	case "import", "import-cmake":
		return importProject(ctx, command, path)
	case "import-xmake":
		return app.ImportXmake(ctx, path, progressPrinter)
	case "export-cmake":
		_, err := app.ExportCMake(ctx, path, flags.output, flags.force, progressPrinter)
		return err
	case "package":
		return app.PackageWithProgress(ctx, path, flags.output, progressPrinter)
	case "release":
		return releaseProject(ctx, path, flags)
	case "vcpkg":
		return app.Vcpkg(ctx, flags.vcpkgRoot, flags.query, splitCSV(flags.portsText), flags.vcpkgTriplet, flags.install)
	case "toolchain":
		return app.Toolchains(ctx, path, flags.toolchainCache, flags.refresh)
	case "doctor":
		return app.DoctorWithContext(ctx, path, progressPrinter)
	case "logs":
		id := ""
		if len(args) > 0 {
			id = args[0]
		}
		return app.Logs(path, id, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

// progressPrinter writes build progress lines to standard output.
func progressPrinter(line string) { fmt.Println(line) }

// splitCSV splits a comma-separated flag value into trimmed, non-empty items and
// returns nil when nothing remains.
func splitCSV(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// configureProject applies the configure subcommand's flags to the project
// configuration.
func configureProject(path string, flags *runFlags) error {
	if flags.interactive {
		return app.ConfigureInteractive(path, os.Stdin, os.Stdout)
	}
	if flags.preset != "" {
		if err := app.ApplyCompilerPreset(path, flags.preset); err != nil {
			return err
		}
	}
	if flags.compileFlags != "" || flags.linkFlags != "" {
		if err := app.SetFlags(path, flags.compileFlags, flags.linkFlags); err != nil {
			return err
		}
	}
	if err := app.ConfigureCompilerOptions(path, flags.mode, flags.distribution, flags.cFlags, flags.cxxFlags); err != nil {
		return err
	}
	configuredCUDA := ""
	if flags.cudaExecution == "" && flags.cudaDistribution == "" {
		configuredCUDA = flags.cudaRoot
	}
	if err := app.Configure(app.ConfigureOptions{
		Path:           path,
		Compiler:       flags.compiler,
		C:              flags.c,
		Profile:        flags.profile,
		VcpkgRoot:      flags.vcpkgRoot,
		Triplet:        flags.triplet,
		MSVC:           flags.msvc,
		CUDAVersion:    configuredCUDA,
		CUDAMode:       flags.cudaMode,
		Setup:          flags.setup,
		ModuleScanner:  flags.moduleScanner,
		ModulesEnabled: flags.modulesEnabled,
	}); err != nil {
		return err
	}
	if flags.cudaExecution != "" || flags.cudaDistribution != "" {
		if err := app.SetCUDAConnection(path, true, flags.cudaRoot, flags.cudaExecution, flags.cudaDistribution); err != nil {
			return err
		}
	}
	if flags.vulkanRoot != "" || flags.vulkanExecution != "" || flags.vulkanDistribution != "" {
		if err := app.SetVulkanConnection(path, true, flags.vulkanRoot, flags.vulkanExecution, flags.vulkanDistribution); err != nil {
			return err
		}
	}
	if flags.savePreset != "" {
		return app.SaveCompilerPreset(path, flags.savePreset)
	}
	return nil
}

// buildProject applies the build subcommand's flags and then builds the selected
// targets, opening the dashboard first when -ui was requested on a terminal.
func buildProject(ctx context.Context, path string, args []string, flags *runFlags) error {
	if flags.preset != "" {
		if err := app.ApplyCompilerPreset(path, flags.preset); err != nil {
			return err
		}
	}
	if flags.buildProfile != "" {
		if err := app.SetProfile(path, flags.buildProfile); err != nil {
			return err
		}
	}
	if flags.dashboard && term.IsTerminal(int(os.Stdin.Fd())) {
		return tuimodel.RunDashboard(path, dashboardServices(path))
	}
	return app.BuildTargetsWithOptions(ctx, path, args, flags.testAll, flags.force, progressPrinter)
}

// importProject imports a CMake build directory, falling back to Xmake when the
// plain import command finds an xmake.lua in the current directory.
func importProject(ctx context.Context, command, path string) error {
	if command == "import" {
		if _, err := os.Stat("xmake.lua"); err == nil {
			return app.ImportXmake(ctx, path, progressPrinter)
		}
	}
	result, err := app.ImportCMake(ctx, path, progressPrinter)
	if err == nil {
		for _, warning := range result.Warnings {
			fmt.Println("warning:", warning)
		}
	}
	return err
}

// releaseProject configures and runs a release build.
func releaseProject(ctx context.Context, path string, flags *runFlags) error {
	if err := app.ConfigureRelease(path, flags.preset, splitCSV(flags.releaseTargets), flags.output); err != nil {
		return err
	}
	return app.ReleaseWithProgress(ctx, path, progressPrinter)
}

func dashboardServices(path string) tuimodel.Services {
	return tuimodel.Services{
		Doctor: func(ctx context.Context, progress func(string)) error {
			return app.DoctorWithContext(ctx, path, progress)
		},
		Build: func(ctx context.Context, progress func(string)) error {
			return app.BuildWithProgress(ctx, path, progress)
		},
		BuildTargets: func(ctx context.Context, targets []string, all bool, progress func(string)) error {
			return app.BuildTargetsWithProgress(ctx, path, targets, all, progress)
		},
		BuildTargetsForce: func(ctx context.Context, targets []string, all bool, progress func(string)) error {
			return app.BuildTargetsWithOptions(ctx, path, targets, all, true, progress)
		},
		AssessTargets: func(ctx context.Context) (config.Config, map[string]policy.Status, policy.Report, error) {
			return app.AssessTargetsWithReport(ctx, path)
		},
		PreviewTask: func(_ context.Context, name string) (config.TaskPreview, error) {
			return app.PreviewTask(path, name)
		},
		RunTask: func(ctx context.Context, name string, preview config.TaskPreview, progress func(string)) error {
			return app.ExecuteTaskPreview(ctx, path, name, preview, progress)
		},
		AddPackage: func(name string) error { return app.AddPackage(path, name, "") },
		InstallPackage: func(ctx context.Context, name string, progress func(string)) error {
			return app.InstallPackage(ctx, path, name, progress)
		},
		SetProfile:      func(profile string) error { return app.SetProfile(path, profile) },
		SetCompileFlags: func(flags string) error { return app.SetFlags(path, flags, "") },
		SearchPackages: func(ctx context.Context, query string) ([]deps.PackageEntry, error) {
			return app.SearchPackages(ctx, path, query)
		},
		RemovePackage: func(name string) error { return app.RemovePackage(path, name) },
		SetPackageVersion: func(name, version string) error {
			return app.SetPackageVersion(path, name, version)
		},
		SetCompiler:    func(compiler string) error { return app.SetCompiler(path, compiler) },
		SetWSLCompiler: func(distribution, compiler string) error { return app.SetWSLCompiler(path, distribution, compiler) },
		SetEnvironment: func(setup string) error { return app.SetEnvironment(path, setup) },
		SetCUDA:        func(enabled bool, root string) error { return app.SetCUDA(path, enabled, root) },
		SetCUDAConnection: func(enabled bool, root, execution, distribution string) error {
			return app.SetCUDAConnection(path, enabled, root, execution, distribution)
		},
		SetVulkan: func(enabled bool, root, execution, distribution string) error {
			return app.SetVulkanConnection(path, enabled, root, execution, distribution)
		},
		SetTestGroup: func(job, group string) error { return app.SetTestGroup(path, job, group) },
		RunTests: func(ctx context.Context, selection tuimodel.TestSelection, progress func(string)) error {
			return app.RunTests(ctx, path, app.TestSelection{Job: selection.Job, Group: selection.Group, File: selection.File, All: selection.All}, progress)
		},
		ImportCMake: func(ctx context.Context, progress func(string)) error {
			result, err := app.ImportCMake(ctx, path, progress)
			if err == nil {
				for _, warning := range result.Warnings {
					progress("Warning: " + warning)
				}
			}
			return err
		},
		ImportXmake:       func(ctx context.Context, progress func(string)) error { return app.ImportXmake(ctx, path, progress) },
		SetLanguageFlags:  func(language, flags string) error { return app.SetLanguageFlags(path, language, flags) },
		ApplyPreset:       func(name string) error { return app.ApplyCompilerPreset(path, name) },
		SavePreset:        func(name string) error { return app.SaveCompilerPreset(path, name) },
		SetDefaultTargets: func(targets []string) error { return app.SetDefaultTargets(path, targets) },
		SetProjectSetting: func(key, value string) error { return app.SetProjectSetting(path, key, value) },
		DeletePreset:      func(name string) error { return app.DeleteCompilerPreset(path, name) },
		ConfigureRelease: func(preset string, targets []string, output string) error {
			return app.ConfigureRelease(path, preset, targets, output)
		},
		Release: func(ctx context.Context, progress func(string)) error {
			return app.ReleaseWithProgress(ctx, path, progress)
		},
	}
}

func discoverConfig() (string, error) {
	path := config.DefaultFileName
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	matches, err := filepath.Glob(filepath.Join("*", config.DefaultFileName))
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple projects found: %s", strings.Join(matches, ", "))
	}
	if _, err := os.Stat("CMakeLists.txt"); err == nil {
		return config.DefaultFileName, nil
	}
	return "", fmt.Errorf("trestle.toml not found; run trestle init first")
}

func usage() {
	fmt.Println("Trestle - lightweight C/C++/CUDA/Vulkan build-plan generator")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  trestle                         open Project Console TUI")
	fmt.Println("  trestle tui [-C dir]            open Project Console TUI")
	fmt.Println("  trestle init [-C dir] [-name project]")
	fmt.Println("  trestle configure [-C dir] [-toolchain clang++] [-msvc 14.3~14.5] [-mode native|wsl] [-wsl-distribution Ubuntu] [-cuda 12.0~12.9] [-cuda-execution native|wsl] [-vulkan path] [-vulkan-execution native|wsl]")
	fmt.Println("  trestle analyze [-C dir]")
	fmt.Println("  trestle build [-C dir] [--all] [--force] [--preset name] [target ...]")
	fmt.Println("  trestle test [-C dir] [-job name | -group name | -file source | -all]")
	fmt.Println("  trestle import [-C dir]          import CMake through the File API")
	fmt.Println("  trestle import-xmake [-C dir]    import Xmake targets (new and legacy Xmake)")
	fmt.Println("  trestle export-cmake [-C dir] [-output CMakeLists.txt] [-force]")
	fmt.Println("                                 reverse-export trestle.toml to CMakeLists.txt (experimental)")
	fmt.Println("  trestle package [-C dir] [-output dist/app.zip]")
	fmt.Println("  trestle release [-C dir] [-optimization balanced|speed|size|custom:name] [-targets app,tool] [-output dist/app.zip]")
	fmt.Println("  trestle doctor [-C dir]")
	fmt.Println("  trestle logs [-C dir] [operation-id]")
	fmt.Println("  trestle toolchain [-C dir] [--refresh]")
	fmt.Println("  trestle vcpkg [-C dir] [-root path] [-search query] | [-install -ports zlib,fmt]")
	fmt.Println("  package references accept vcpkg ports (zlib) and conan references (fmt/10.2.1@x/stable)")
	fmt.Println("  trestle version")
}
