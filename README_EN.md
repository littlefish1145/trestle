<p align="center">
  <img src="assets/trestle-banner.png" alt="Trestle" width="100%">
</p>

<p align="center">
  A lightweight build workspace for C, C++, CUDA, and Vulkan projects
</p>

<p align="center">
  <a href="README.md">中文</a> · <a href="LICENSE">MIT License</a>
</p>

# Trestle

Trestle brings compiler discovery, dependency resolution, project import, Ninja builds, testing, and releases into one CLI and TUI. A `trestle.toml` file is the source of truth, while the generated `build.ninja`, `compile_commands.json`, and command metadata remain transparent and inspectable.

## Highlights

- C17 and C++20 projects with static libraries, shared libraries, executables, tests, and Vulkan shader targets.
- Automatic discovery of MSVC, Clang, clang-cl, GCC, MinGW, CUDA, Vulkan SDKs, and WSL compilers.
- Automatic Visual Studio ABI setup for clang-cl through an explicit `vcvars64.bat`, configured tool paths, common VS locations, or `vswhere`.
- Portable toolchain selection: `msvc = "14.30~14.50"` and `cuda = "12.0~12.9"` pin installed versions by range, so a committed `trestle.toml` never records one machine's absolute paths. Schema 6 files are rewritten automatically on first load, with the original kept as a `.schema-v6.*.bak` file.
- Toolchain discovery results are cached in one designated directory (`[toolchain].cache_dir`, `TRESTLE_TOOLCHAIN_CACHE`, or `.trestle/toolchain`); `trestle toolchain --refresh` rescans after installing a new toolset.
- Isolated target builds: `trestle build app` does not let unrelated GUI, tool, or test targets block the requested target.
- C/C++ include dependency tracking for correct incremental rebuilds, including wrapper files that include implementation sources.
- vcpkg search, installation, transitive metadata, triplets, CRT/linkage handling, pkg-config parsing, and runtime DLL deployment.
- Native CMake File API and Xmake import.
- Reverse export (experimental): render `trestle.toml` as an equivalent `CMakeLists.txt` with matching targets, scopes, and tests; Trestle-only sections stay as comments.
- Test selection by job, group, owning source file, or the complete suite.
- Release workflows with built-in or custom optimization presets and ZIP packaging of outputs and runtime dependencies.
- `compile_commands.json` for VS Code/clangd and `.trestle/commands.json` for editor automation.
- VS Code tasks: `trestle init` writes `.vscode/tasks.json` with build, test, release, target-picker, and per-executable run tasks; `trestle editor-config` refreshes them, merging instead of clobbering your own tasks.
- Diagnostics grouped by target and compile/link stage, followed by succeeded, failed, and skipped target summaries.

## Requirements

- Go 1.26 or newer to build Trestle from source
- Ninja
- At least one supported C/C++ compiler
- CMake for CMake import, exported-symbol generation, and runtime file deployment
- Optional: vcpkg, Xmake, CUDA Toolkit, Vulkan SDK, and WSL

On Windows, clang-cl does not require a manually opened Developer Command Prompt. Trestle selects and persists a valid Visual Studio x64 ABI environment automatically.

## Build and install

```powershell
go build -o trestle.exe ./cmd/trestle
```

Or install it into your Go bin directory:

```powershell
go install ./cmd/trestle
```

Automated Alpha desktop packages are also available from GitHub Releases. Every update to `main` publishes x64 and ARM64 builds for Windows, Linux, and macOS:

- Windows: extract the ZIP and run `powershell -ExecutionPolicy Bypass -File .\install.ps1` from the package directory.
- Linux/macOS: extract the `tar.gz` archive and run `./install.sh` from the package directory.
- The installer copies the complete `bin` directory and adds it to the user `PATH`. Under GitHub Actions it writes to `GITHUB_PATH`, so later steps can invoke `trestle` directly.
- Every Alpha prerelease includes commit details, build logs, a package manifest, and SHA-256 checksums. The complete Actions log archive is attached after the main workflow finishes.

Confirm the installed build identity with:

```powershell
trestle version
```

Development checks:

```powershell
go test ./...
go vet ./...
```

## Quick start

Start with [the complete C++ + fmt tutorial](docs/tutorial.en.md) for Windows, Linux, macOS and WSL, including logs and recovery. A runnable project is in [examples/hello-fmt](examples/hello-fmt/). For multiple projects, see the runnable [examples/hello-workspace](examples/hello-workspace/) and the [workspace guide](docs/workspace.en.md).

```powershell
trestle init -C hello -name hello
cd hello
trestle configure -toolchain clang-cl
trestle build app
```

Run `trestle` in a project directory to open the TUI, or use:

```powershell
trestle build --ui
```

## Command overview

```text
trestle                         Open the project TUI
trestle init                    Create a project
trestle configure               Configure compilers, environments, CUDA, WSL, and presets
trestle analyze                 Analyze an existing source tree
trestle build [target ...]      Build selected targets
trestle build --all             Build every target
trestle build -p MEMBER         Build one workspace member
trestle build MEMBER:TARGET     Build one target of one member
trestle test --job NAME         Run one test job
trestle test --group NAME       Run a test group
trestle test --file FILE        Run the test that owns a source file
trestle test --all              Run the complete test suite
trestle test -p MEMBER          Run one workspace member's tests
trestle import                  Select Xmake or CMake import automatically
trestle import-xmake            Import an Xmake project
trestle export-cmake            Reverse-export trestle.toml to CMakeLists.txt (experimental; -force overwrites)
trestle package                 Build a release archive from the [package] settings
trestle release                 Build and create a release ZIP
trestle editor-config           Generate VS Code tasks (-merge updates, -force overwrites)
trestle vcpkg --search QUERY    Search vcpkg ports
trestle vcpkg --install --ports fmt,zlib
                                Install vcpkg ports
trestle toolchain               List detected toolchains
trestle doctor                  Check the project environment
trestle logs                    Show run logs
trestle version                 Print version information
```

See the [command-line reference](docs/cli.en.md) for every subcommand, option, and exit convention.

Use a WSL compiler:

```powershell
trestle configure -toolchain /usr/bin/clang++ -c /usr/bin/clang `
  -mode wsl -wsl-distribution Ubuntu-24.04
```

Connect CUDA and Vulkan SDK tools from the same WSL distribution (roots are optional; Trestle discovers them from environment variables and `PATH`):

```powershell
trestle configure -toolchain /usr/bin/g++ -c /usr/bin/gcc `
  -mode wsl -wsl-distribution Ubuntu-24.04 `
  -cuda-execution wsl -cuda-wsl-distribution Ubuntu-24.04 `
  -vulkan-execution wsl -vulkan-wsl-distribution Ubuntu-24.04
```

The Project Console Toolchains page also lists CUDA/Vulkan components found in each WSL distribution; select one and press Enter to connect it. CUDA must share a distribution with its WSL host compiler, while Vulkan shader tools can run independently in WSL.

Save and reuse compiler options:

```powershell
trestle configure -toolchain clang-cl -compile-flags "/W4 /utf-8" `
  -link-flags "/DEBUG" -save-preset dev
trestle build -preset dev app
```

## Documentation

- [Command-line reference](docs/cli.en.md): every subcommand and option.
- [`trestle.toml` reference](docs/configuration.en.md): the complete schema, defaults, condition expressions, and error codes.
- [Build a C++ project with fmt from scratch](docs/tutorial.en.md): a full walkthrough for Windows, Linux, macOS, and WSL.
- [Workspace guide](docs/workspace.en.md): one root configuration for several member projects, cross-member dependencies, and build order.
- [Chinese documentation](README.md).

## Example `trestle.toml`

```toml
schema_version = 7

[project]
name = "hello"

[build]
profile = "debug"
build_dir = "build"
c_standard = "c17"
cxx_standard = "c++20"
default_targets = ["app"]
compile_commands = "compile_commands.json"

[toolchain]
# Select tools by version range, never by absolute path, so the file stays shareable.
msvc = "14.30~14.50"
c = "auto"
cxx = "auto"
cuda = ""
cache_dir = ".trestle/toolchain"
mode = "native"
cuda_execution = "native"
vulkan_execution = "native"

[vcpkg]
root = "C:\\src\\vcpkg"
triplet = "x64-windows"

[targets.app]
type = "executable"
sources = ["src/*.cpp"]
include_dirs = ["include"]
compile_options = ["/W4"]

[[targets.app.dependencies]]
package = "fmt"
scope = "private"

[packages.fmt]
provider = "vcpkg"
port = "fmt"
triplet = "x64-windows"

[package]
format = "zip"
output = "dist/hello.zip"
optimization = "balanced"
targets = ["app"]
```

Dependencies may use `private`, `public`, or `interface` scope. Supported target types are `static`, `shared`, `executable`, `test`, and `shader`.

### Conditional rules and explicit tasks

Rules use a deliberately small, side-effect-free `when` expression: `&&`, `||`, `!`, comparisons, parentheses, `os`, `arch`, `mode`, and read-only `tool()`, `wsl()`, `wsl_tool()`, `pkg()`, `env()`, and `path()` queries. Rules run in TOML order and may select compilers, execution mode, flags, default targets, per-target `packages` dependencies, and `depends_on` target dependencies.

```toml
[[rules]]
when = 'os == "windows" && wsl_tool("Ubuntu", "clang++")'
mode = "wsl"
wsl_distribution = "Ubuntu"
cxx = "clang++"

[[rules]]
when = 'mode == "wsl" && pkg("fmt")'
target = "app"
compile_flags = ["-Wall"]
packages = ["fmt"]

[tasks.prepare]
description = "Check compiler and select release profile"
command = ["clang++", "--version"]
set = { "build.profile" = "release", "toolchain.cxx" = "clang++" }
```

Targets can also declare `when`, `requires_tools`, and `requires_wsl`. The Targets and Build views explain unavailable targets. Press `f` to attempt the selected target despite failed checks, or use `trestle build --force`; missing tools or packages may still make the build fail. The TUI Tasks view displays every command and configuration change and requires explicit confirmation. Tasks from unknown projects can harm your device; commands run as argv, without an implicit shell.

## Workspaces

A root `trestle.toml` can reference member projects through `[workspace]`, giving one build and one dependency resolution across them:

```toml
# trestle.toml (root)
schema_version = 7
[project]
name = "myproj"

[build]
profile = "debug"

[toolchain]
cxx = "clang++"

[workspace]
members = ["core", "cli"]
```

Every member keeps its own `trestle.toml`:

```toml
# cli/trestle.toml
schema_version = 7
[project]
name = "cli"

[targets.app]
type = "executable"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:util", scope = "public" }]
```

Rules:

- `[toolchain]`, `[vcpkg]`, `[conan]`, and `[compiler_presets]` are inherited from the root; member values win. `[build]` profile, standards, and flags stay per member.
- Root `[packages]` and `[workspace.dependencies]` entries (each must set `port`) are visible to every member; member declarations win on name conflicts.
- Target names are unique across the workspace; a duplicate fails with `E_WORKSPACE_INVALID`.
- Cross-member dependencies use `member:target`. Members build in dependency order, with artifacts under `build/<profile>/<member>/`.
- The root may be a pure container or declare its own aggregate targets; root artifacts live in `build/<profile>/`.
- `compile_commands.json` aggregates every member's compile commands (written both at the root and under `build/<profile>/`).
- A standalone project (no `[workspace]` section) behaves exactly as before.

```powershell
trestle build                 # build every member in dependency order
trestle build -p core         # build only core
trestle build cli:app         # build cli's app target
trestle test -p core --all    # run only core's tests
trestle doctor                # check each member
trestle editor-config         # VS Code tasks for member builds and runs
```

In a workspace the TUI turns the sidebar into a collapsible member tree: the `Root` section carries the workspace-wide screens (Overview, aggregate Targets, shared Packages, Tasks, Toolchains, Presets, Doctor, Settings) and every member carries its own Targets / Packages / Tests / Tasks / Presets / Release / Doctor / Settings. ↑↓ switches screens immediately (wrapping), digits jump, Enter focuses the content pane, `z` folds a section, and `w` switches members. Root Targets aggregates every member's targets as `member:target` and supports `/` search (standalone Targets screens do too). Member edits are written to that member's `trestle.toml`; root packages are inherited by every member and marked `shared from root`. Log lines carry a `[member]` prefix and the header shows `ws:` and `pkg:` context.

## Repository layout

```text
cmd/trestle/          CLI entry point
internal/app/         Application services and build/test/release workflows
internal/config/      TOML schema, migration, and validation
internal/deps/vcpkg/  vcpkg search, installation, and metadata resolution
internal/graph/       Target graph and usage propagation
internal/importer/    CMake and Xmake importers
internal/ninja/       Ninja manifest emitter
internal/plan/        Compile, link, and runtime deployment plans
internal/toolchain/   Compilers, Visual Studio ABI, WSL, and CUDA
internal/tui/         Terminal user interface
scripts/release/      Alpha packages and PATH-aware installers
.github/workflows/    Desktop Alpha build, release, and log archival
docs/                 Tutorials, configuration, and command references
testdata/             Test fixtures
```

## Design

Trestle emits a conventional Ninja graph and keeps complete commands available for inspection. Project configuration, generated files, and the active toolchain stay clearly separated: configuration can be versioned, build directories can be recreated at any time, and compiler or dependency failures are reported by target and stage.

## License

Trestle is available under the [MIT License](LICENSE).
