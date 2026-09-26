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
- Isolated target builds: `trestle build app` does not let unrelated GUI, tool, or test targets block the requested target.
- C/C++ include dependency tracking for correct incremental rebuilds, including wrapper files that include implementation sources.
- vcpkg search, installation, transitive metadata, triplets, CRT/linkage handling, pkg-config parsing, and runtime DLL deployment.
- Native CMake File API and Xmake import.
- Test selection by job, group, owning source file, or the complete suite.
- Release workflows with built-in or custom optimization presets and ZIP packaging of outputs and runtime dependencies.
- `compile_commands.json` for VS Code/clangd and `.trestle/commands.json` for editor automation.
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

Development checks:

```powershell
go test ./...
go vet ./...
```

## Quick start

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
trestle test --job NAME         Run one test job
trestle test --group NAME       Run a test group
trestle test --file FILE        Run the test that owns a source file
trestle test --all              Run the complete test suite
trestle import                  Select Xmake or CMake import automatically
trestle import-xmake            Import an Xmake project
trestle release                 Build and create a release ZIP
trestle vcpkg --search QUERY    Search vcpkg ports
trestle vcpkg --install --ports fmt,zlib
                                Install vcpkg ports
trestle toolchain               List detected toolchains
trestle doctor                  Check the project environment
```

Use a WSL compiler:

```powershell
trestle configure -toolchain /usr/bin/clang++ -c /usr/bin/clang `
  -mode wsl -wsl-distribution Ubuntu-24.04
```

Save and reuse compiler options:

```powershell
trestle configure -toolchain clang-cl -compile-flags "/W4 /utf-8" `
  -link-flags "/DEBUG" -save-preset dev
trestle build -preset dev app
```

## Example `trestle.toml`

```toml
schema_version = 4

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
c = "auto"
cxx = "auto"
mode = "native"

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
testdata/             Test fixtures
```

## Design

Trestle emits a conventional Ninja graph and keeps complete commands available for inspection. Project configuration, generated files, and the active toolchain stay clearly separated: configuration can be versioned, build directories can be recreated at any time, and compiler or dependency failures are reported by target and stage.

## License

Trestle is available under the [MIT License](LICENSE).
