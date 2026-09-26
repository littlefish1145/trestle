package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"trestle/internal/app"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/diag"
	tuimodel "trestle/internal/tui"
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
	if err := run(command, os.Args[2:]); err != nil {
		diag.Render(os.Stderr, err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	directory := set.String("C", ".", "run from this directory")
	var name, compiler, c, profile, output, vcpkgRoot, triplet, cudaRoot, cudaMode, cudaExecution, cudaDistribution, vulkanRoot, vulkanExecution, vulkanDistribution, setup, moduleScanner, compileFlags, cFlags, cxxFlags, linkFlags, buildProfile, mode, distribution, preset, savePreset *string
	var query string
	var ports []string
	var portsText string
	var vcpkgTriplet string
	var install bool
	var testJob, testGroup, testFile string
	var releaseTargets string
	var testAll bool
	var interactive, modulesEnabled, dashboard *bool
	if command == "init" {
		name = set.String("name", "", "project name")
	} else {
		switch command {
		case "configure":
			compiler = set.String("toolchain", "", "compiler name or path")
			c = set.String("c", "", "C compiler name or path")
			profile = set.String("profile", "", "build profile")
			compileFlags = set.String("compile-flags", "", "comma-free compiler flags")
			cFlags = set.String("c-flags", "", "C-only compiler flags")
			cxxFlags = set.String("cxx-flags", "", "C++-only compiler flags")
			linkFlags = set.String("link-flags", "", "linker flags")
			mode = set.String("mode", "", "compiler execution mode: native or wsl")
			distribution = set.String("wsl-distribution", "", "WSL distribution name")
			preset = set.String("preset", "", "apply a compiler preset")
			savePreset = set.String("save-preset", "", "save the resulting compiler configuration")
			vcpkgRoot = set.String("vcpkg-root", "", "vcpkg installation root")
			triplet = set.String("triplet", "", "vcpkg triplet")
			cudaRoot = set.String("cuda", "", "CUDA toolkit root")
			cudaMode = set.String("cuda-mode", "", "whole or rdc")
			cudaExecution = set.String("cuda-execution", "", "CUDA execution mode: native or wsl")
			cudaDistribution = set.String("cuda-wsl-distribution", "", "WSL distribution containing CUDA")
			vulkanRoot = set.String("vulkan", "", "Vulkan SDK root")
			vulkanExecution = set.String("vulkan-execution", "", "Vulkan execution mode: native or wsl")
			vulkanDistribution = set.String("vulkan-wsl-distribution", "", "WSL distribution containing Vulkan SDK tools")
			interactive = set.Bool("interactive", false, "use the configuration wizard")
			setup = set.String("setup", "", "vcvars64.bat or environment setup script")
			moduleScanner = set.String("module-scanner", "", "clang-scan-deps executable")
			modulesEnabled = set.Bool("modules", false, "enable C++ module scanning")
		case "build":
			dashboard = set.Bool("ui", false, "open the project console")
			buildProfile = set.String("profile", "", "debug or release")
			preset = set.String("preset", "", "apply a compiler preset")
			set.BoolVar(&testAll, "all", false, "build every target")
		case "test":
			set.StringVar(&testJob, "job", "", "run one test job")
			set.StringVar(&testGroup, "group", "", "run one test group")
			set.StringVar(&testFile, "file", "", "run the test job owning a source file")
			set.BoolVar(&testAll, "all", false, "run all test jobs")
		case "package":
			output = set.String("output", "", "package output path")
		case "release":
			output = set.String("output", "", "release ZIP output path")
			preset = set.String("optimization", "", "balanced, speed, size, or custom:<preset>")
			set.StringVar(&releaseTargets, "targets", "", "comma-separated release targets")
		case "vcpkg":
			vcpkgRoot = set.String("root", "", "vcpkg root")
			set.StringVar(&query, "search", "", "search ports")
			set.BoolVar(&install, "install", false, "install ports")
			set.StringVar(&portsText, "ports", "", "comma-separated ports")
			set.StringVar(&vcpkgTriplet, "triplet", "", "vcpkg triplet")
		}
	}
	if err := set.Parse(args); err != nil {
		return err
	}
	if command == "init" {
		return app.Init(filepath.Clean(*directory), *name)
	}
	if err := os.Chdir(*directory); err != nil {
		return err
	}
	if command == "analyze" {
		return app.AnalyzeProject(".", os.Stdout)
	}
	path := config.DefaultFileName
	switch command {
	case "configure":
		if *interactive {
			return app.ConfigureInteractive(path, os.Stdin, os.Stdout)
		}
		if *preset != "" {
			if err := app.ApplyCompilerPreset(path, *preset); err != nil {
				return err
			}
		}
		if *compileFlags != "" || *linkFlags != "" {
			if err := app.SetFlags(path, *compileFlags, *linkFlags); err != nil {
				return err
			}
		}
		if err := app.ConfigureCompilerOptions(path, *mode, *distribution, *cFlags, *cxxFlags); err != nil {
			return err
		}
		configuredCUDA := ""
		if *cudaExecution == "" && *cudaDistribution == "" {
			configuredCUDA = *cudaRoot
		}
		if err := app.Configure(path, *compiler, *c, *profile, *vcpkgRoot, *triplet, configuredCUDA, *cudaMode, *setup, *moduleScanner, *modulesEnabled); err != nil {
			return err
		}
		if *cudaExecution != "" || *cudaDistribution != "" {
			if err := app.SetCUDAConnection(path, true, *cudaRoot, *cudaExecution, *cudaDistribution); err != nil {
				return err
			}
		}
		if *vulkanRoot != "" || *vulkanExecution != "" || *vulkanDistribution != "" {
			if err := app.SetVulkanConnection(path, true, *vulkanRoot, *vulkanExecution, *vulkanDistribution); err != nil {
				return err
			}
		}
		if *savePreset != "" {
			return app.SaveCompilerPreset(path, *savePreset)
		}
		return nil
	case "build":
		if *preset != "" {
			if err := app.ApplyCompilerPreset(path, *preset); err != nil {
				return err
			}
		}
		if *buildProfile != "" {
			if err := app.SetProfile(path, *buildProfile); err != nil {
				return err
			}
		}
		if *dashboard && term.IsTerminal(int(os.Stdin.Fd())) {
			return tuimodel.RunDashboard(path, dashboardServices(path))
		}
		return app.BuildTargetsWithProgress(context.Background(), path, set.Args(), testAll, func(line string) { fmt.Println(line) })
	case "test":
		selection := app.TestSelection{Job: testJob, Group: testGroup, File: testFile, All: testAll || (testJob == "" && testGroup == "" && testFile == "")}
		return app.RunTests(context.Background(), path, selection, func(line string) { fmt.Println(line) })
	case "import", "import-cmake":
		if command == "import" {
			if _, err := os.Stat("xmake.lua"); err == nil {
				return app.ImportXmake(context.Background(), path, func(line string) { fmt.Println(line) })
			}
		}
		result, err := app.ImportCMake(context.Background(), path, func(line string) { fmt.Println(line) })
		if err == nil {
			for _, warning := range result.Warnings {
				fmt.Println("warning:", warning)
			}
		}
		return err
	case "import-xmake":
		return app.ImportXmake(context.Background(), path, func(line string) { fmt.Println(line) })
	case "package":
		return app.Package(path, *output)
	case "release":
		var targets []string
		if releaseTargets != "" {
			for _, name := range strings.Split(releaseTargets, ",") {
				if name = strings.TrimSpace(name); name != "" {
					targets = append(targets, name)
				}
			}
		}
		if err := app.ConfigureRelease(path, *preset, targets, *output); err != nil {
			return err
		}
		return app.ReleaseWithProgress(context.Background(), path, func(line string) { fmt.Println(line) })
	case "vcpkg":
		if portsText != "" {
			for _, port := range strings.Split(portsText, ",") {
				if port = strings.TrimSpace(port); port != "" {
					ports = append(ports, port)
				}
			}
		}
		return app.Vcpkg(context.Background(), *vcpkgRoot, query, ports, vcpkgTriplet, install)
	case "toolchain":
		return app.Toolchains(context.Background())
	case "doctor":
		return app.Doctor(path)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func dashboardServices(path string) tuimodel.Services {
	return tuimodel.Services{
		Build: func(ctx context.Context, progress func(string)) error {
			return app.BuildWithProgress(ctx, path, progress)
		},
		BuildTargets: func(ctx context.Context, targets []string, all bool, progress func(string)) error {
			return app.BuildTargetsWithProgress(ctx, path, targets, all, progress)
		},
		AddPackage: func(name string) error { return app.AddPackage(path, name, "") },
		InstallPackage: func(ctx context.Context, name string, progress func(string)) error {
			return app.InstallPackage(ctx, path, name, progress)
		},
		SetProfile:      func(profile string) error { return app.SetProfile(path, profile) },
		SetCompileFlags: func(flags string) error { return app.SetFlags(path, flags, "") },
		SearchPackages: func(ctx context.Context, query string) ([]vcpkg.Port, error) {
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
	fmt.Println("  trestle init [-C dir] [-name project]")
	fmt.Println("  trestle configure [-C dir] [-toolchain clang++] [-mode native|wsl] [-wsl-distribution Ubuntu] [-cuda path] [-cuda-execution native|wsl] [-vulkan path] [-vulkan-execution native|wsl]")
	fmt.Println("  trestle analyze [-C dir]")
	fmt.Println("  trestle build [-C dir] [--all] [--preset name] [target ...]")
	fmt.Println("  trestle test [-C dir] [-job name | -group name | -file source | -all]")
	fmt.Println("  trestle import [-C dir]          import CMake through the File API")
	fmt.Println("  trestle import-xmake [-C dir]    import Xmake targets (new and legacy Xmake)")
	fmt.Println("  trestle package [-C dir] [-output dist/app.zip]")
	fmt.Println("  trestle release [-C dir] [-optimization balanced|speed|size|custom:name] [-targets app,tool] [-output dist/app.zip]")
	fmt.Println("  trestle doctor [-C dir]")
	fmt.Println("  trestle toolchain [-C dir]")
	fmt.Println("  trestle vcpkg [-C dir] [-root path] [-search query] | [-install -ports zlib,fmt]")
}
