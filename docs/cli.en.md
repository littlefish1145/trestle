# Command-line reference

[中文](cli.zh-CN.md) · [Back to README](../README_EN.md)

Trestle ships a single executable, `trestle`. It runs inside a project directory, reads [`trestle.toml`](configuration.en.md), and drives build, test, import, and packaging workflows. Run it in a terminal with no arguments to open the Project Console (TUI).

## Global conventions

- **`-C <dir>`**: accepted by every subcommand, defaulting to `.`. For `init` it is the directory to create the project in; for other commands Trestle changes into it before running.
- **No-argument run**: opens the TUI in a terminal (discovering the project configuration); prints usage when not attached to a terminal.
- **Diagnostics and exit codes**: exit `0` on success; on failure diagnostics go to standard error and the process exits `1`. Failures are also recorded as a run log entry that `trestle logs` can show.
- **Toolchain discovery cache**: results are cached in `[toolchain].cache_dir`, `TRESTLE_TOOLCHAIN_CACHE`, or `.trestle/toolchain` by default.

## Command overview

| Command | Purpose |
| --- | --- |
| `trestle` / `trestle tui` | Open the Project Console (TUI) |
| `trestle init` | Create a project |
| `trestle configure` | Configure compilers, environment, CUDA, WSL, and presets |
| `trestle analyze` | Analyze an existing source tree |
| `trestle build` | Build targets |
| `trestle test` | Run tests |
| `trestle import` / `import-cmake` | Import through the CMake File API |
| `trestle import-xmake` | Import Xmake targets |
| `trestle export-cmake` | Reverse-export to `CMakeLists.txt` (experimental) |
| `trestle package` | Build a release archive |
| `trestle release` | Build and create a release package |
| `trestle editor-config` | Generate VS Code `.vscode/tasks.json` |
| `trestle vcpkg` | Search or install vcpkg ports |
| `trestle toolchain` | List detected toolchains |
| `trestle doctor` | Check the project environment |
| `trestle logs` | Show run logs |
| `trestle version` | Print version information |
| `trestle help` / `-h` / `--help` | Print usage |

## `trestle` and `trestle tui`

```text
trestle [-C dir]
trestle tui [-C dir]
```

Open the Project Console. With no subcommand, `trestle` discovers the project configuration first, then opens the TUI.

## `trestle init`

```text
trestle init [-C dir] [-name project]
```

Create a minimal working project (`trestle.toml` plus a sample source file). `-C` selects the directory to create, `-name` sets the project name. When `.vscode/tasks.json` does not exist yet, init also writes VS Code build, test, release, and run tasks; an existing file is left untouched.

## `trestle configure`

```text
trestle configure [-C dir] [options ...]
```

Update project settings. Provide only the options you want to change; everything else is preserved.

| Option | Description |
| --- | --- |
| `-toolchain <name or path>` | Set the C++ compiler. |
| `-c <name or path>` | Set the C compiler. |
| `-profile <debug\|release>` | Set the build profile. |
| `-compile-flags "<flags>"` | Common compile flags (for example `"/W4 /utf-8"`). |
| `-c-flags "<flags>"` / `-cxx-flags "<flags>"` | C-only / C++-only compile flags. |
| `-link-flags "<flags>"` | Link flags. |
| `-mode <native\|wsl>` | Compiler execution mode. |
| `-wsl-distribution <name>` | WSL distribution. |
| `-preset <name>` | Apply a compiler preset. |
| `-save-preset <name>` | Save the resulting configuration as a compiler preset. |
| `-vcpkg-root <path>` / `-triplet <name>` | vcpkg root and triplet. |
| `-msvc <version or range>` | MSVC toolset, such as `14.3~14.5`. |
| `-cuda <range or path>` / `-cuda-mode <whole\|rdc>` | CUDA version and mode. |
| `-cuda-execution <native\|wsl>` / `-cuda-wsl-distribution <name>` | Where CUDA runs. |
| `-vulkan <path>` / `-vulkan-execution <native\|wsl>` / `-vulkan-wsl-distribution <name>` | Vulkan SDK location and execution. |
| `-setup <script>` | `vcvars64.bat` or an environment setup script. |
| `-module-scanner <executable>` / `-modules` | Module scanner and whether module scanning is enabled. |
| `-interactive` | Use the configuration wizard. |

```powershell
trestle configure -toolchain clang-cl -compile-flags "/W4 /utf-8" `
  -link-flags "/DEBUG" -save-preset dev
trestle build -preset dev app
```

## `trestle analyze`

```text
trestle analyze [-C dir]
```

Analyze an existing source tree and print the result to standard output.

## `trestle build`

```text
trestle build [-C dir] [--all] [--force] [--preset name] [target ...]
```

| Option | Description |
| --- | --- |
| `[target ...]` | Targets to build; when omitted, `build.default_targets` is used. |
| `--all` | Build every target. |
| `--force` | Skip prechecks and attempt unavailable targets (missing tools or packages may still fail). |
| `--preset <name>` | Apply a compiler preset before building. |
| `--profile <debug\|release>` | Override the build profile. |
| `--ui` | Open the Project Console first. |

## `trestle test`

```text
trestle test [-C dir] [-job name | -group name | -file source | -all]
```

| Option | Description |
| --- | --- |
| `-job <name>` | Run one test job. |
| `-group <name>` | Run one test group. |
| `-file <source>` | Run the test that owns a source file. |
| `-all` | Run the complete test suite. |

With no selection option, the behavior is the same as `-all`.

## `trestle import` and `trestle import-cmake`

```text
trestle import [-C dir]
trestle import-cmake [-C dir]
```

Import through the CMake File API. `trestle import` switches to Xmake import automatically when an `xmake.lua` exists in the current directory.

## `trestle import-xmake`

```text
trestle import-xmake [-C dir]
```

Import Xmake targets (new and legacy Xmake).

## `trestle export-cmake`

```text
trestle export-cmake [-C dir] [-output CMakeLists.txt] [-force]
```

Reverse-export `trestle.toml` as an equivalent `CMakeLists.txt` (experimental). `-output` sets the path relative to the project (default `CMakeLists.txt`), and `-force` overwrites an existing file. It is informational and may lose detail: Trestle-only sections are preserved as comments.

## `trestle package`

```text
trestle package [-C dir] [-output dist/app.zip]
```

Build a release archive from the `[package]` settings. `-output` overrides the output path. Package references accept vcpkg ports (`zlib`) and Conan references (`fmt/10.2.1@x/stable`).

## `trestle release`

```text
trestle release [-C dir] [-optimization balanced|speed|size|custom:name] [-targets app,tool] [-output dist/app.zip]
```

Configure and run a release build, then package it. `-optimization` selects the optimization preset, `-targets` takes a comma-separated target list, and `-output` overrides the archive path.

## `trestle editor-config`

```text
trestle editor-config [-C dir] [-merge | -force]
```

Write `.vscode/tasks.json` with `Trestle: Build`, `Trestle: Build All`, `Trestle: Build Current Target`, `Trestle: Test`, `Trestle: Release`, and one `Trestle: Run <target>` task per executable target. Run tasks depend on `Trestle: Build`, execute the profile artifact from `${workspaceFolder}`, and carry a `windows` override that appends `.exe`, so the same file works on every OS and is safe to commit.

An existing `.vscode/tasks.json` is never overwritten silently. Without flags the command fails, `-merge` replaces only the tasks and inputs trestle generates (your own tasks, inputs, and top-level settings are kept), and `-force` replaces the whole file. Tasks whose label starts with `Trestle: ` count as generated. `-merge` requires plain JSON; files containing comments are rejected so nothing is lost. `trestle init` writes the file automatically when it is missing, and prints a hint when a `launch.json` exists: set `"preLaunchTask": "Trestle: Build"` to build before debugging.

## `trestle vcpkg`

```text
trestle vcpkg [-C dir] [-root path] [-search query] | [-install -ports zlib,fmt] [-triplet name]
```

| Option | Description |
| --- | --- |
| `-root <path>` | vcpkg root. |
| `-search <query>` | Search ports. |
| `-install` | Install ports. |
| `-ports <comma-separated>` | Ports to install, such as `fmt,zlib`. |
| `-triplet <name>` | vcpkg triplet. |

## `trestle toolchain`

```text
trestle toolchain [-C dir] [--cache-dir path] [--refresh]
```

List detected toolchains. `--refresh` rescans installed toolchains and refreshes the cache, and `--cache-dir` overrides the cache directory. Use it after installing a new toolset.

## `trestle doctor`

```text
trestle doctor [-C dir]
```

Check the project environment (tools, compiler ABI, dependencies, Vulkan/CUDA, WSL, and more) and report problems.

## `trestle logs`

```text
trestle logs [-C dir] [operation-id]
```

Show run logs. With no `operation-id`, list or show the most recent logs.

## `trestle version`

```text
trestle version
trestle --version
```

Print `trestle <version> (<commit>, <build date>)`.

## See also

- [`trestle.toml` reference](configuration.en.md)
- [Build a C++ project with fmt from scratch](tutorial.en.md)
