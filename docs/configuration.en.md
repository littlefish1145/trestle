# `trestle.toml` reference

[中文](configuration.zh-CN.md) · [Back to README](../README_EN.md)

`trestle.toml` is a Trestle project's source of truth: it describes the project identity, build and toolchain settings, dependency providers, targets, conditional rules, and tasks. Trestle reads it to emit `build.ninja`, `compile_commands.json`, and command metadata; those outputs can be recreated at any time, while the configuration itself is meant to be versioned.

## Loading, discovery, and saving

- **Discovery order**: look for `trestle.toml` in the project directory; otherwise look for a single first-level subdirectory holding it; fall back to the default file name in the current directory when a `CMakeLists.txt` exists; otherwise tell you to run `trestle init`.
- **Unknown fields are errors**: unrecognized keys are rejected with `unknown configuration fields: ...`, so a typo is never silently ignored.
- **Schema version**: currently `7` (`CurrentSchemaVersion`). A higher version is rejected (`E_CONFIG_NEWER_SCHEMA`); a lower one is migrated in memory, and on **save** the unmodified original is preserved as `trestle.toml.schema-v<old>.<first 12 hex of fingerprint>.bak`.
- **Concurrent edit detection**: the file fingerprint is compared before writing; if the file changed since it was loaded, Trestle reports `E_CONFIG_CONFLICT` and preserves the current file.
- **Path resolution**: project-relative paths resolve against the directory holding `trestle.toml`; absolute paths are kept as-is. On save, project-local paths are written back as relative paths, so `trestle init -C relative-dir` and moving a checkout do not duplicate prefixes.

## Top level

```toml
schema_version = 7

[project]          # project identity
[build]            # global build settings
[toolchain]        # toolchain selection
[compiler_presets] # reusable compiler presets
[vcpkg]            # vcpkg provider
[conan]            # Conan 2 provider
[targets.<name>]   # buildable units
[packages.<name>]  # dependency declarations
[package]          # release archive settings
[[rules]]          # conditional rules
[tasks.<name>]     # explicit tasks
[workspace]        # workspace members (optional)
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `schema_version` | integer | `7` | Must equal the supported version. |

## `[project]`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `name` | string | `trestle-project` | **Required and non-empty.** Seeds default output names and the default target. |

## `[build]`

Settings that apply to every target unless a rule or a target overrides them.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `profile` | string | `debug` | Build profile, usually `debug` or `release`. |
| `build_dir` | string | `build` | Directory for generated files and artifacts. |
| `c_standard` | string | `c17` | C language standard. |
| `cxx_standard` | string | `c++20` | C++ language standard. |
| `compile_flags` | string[] | `[]` | Compile flags shared by all languages. |
| `c_flags` | string[] | `[]` | C-only compile flags. |
| `cxx_flags` | string[] | `[]` | C++-only compile flags. |
| `link_flags` | string[] | `[]` | Link flags. |
| `default_targets` | string[] | auto-selected | Targets built when none are given. When empty, Trestle prefers the project name, then `app`, then the sorted executable targets, then the first sorted target. |
| `compile_commands` | string | `compile_commands.json` | Output path for `compile_commands.json`. |
| `auto_compile_shaders` | bool | `true` | Compile Vulkan shader targets automatically. |
| `modules` | bool | `false` | Enable C++ module scanning. |
| `module_scanner` | string | empty | Reserved; currently unused. Use `[toolchain].module_scanner`. |

## `[toolchain]`

Tools are selected by **portable version constraint**, never by one machine's absolute path. Version selector syntax:

- `auto`: newest installed version (the default for `c`, `cxx`, `msvc`).
- Dotted version: `14.30`, pinning one version.
- Range: `14.30~14.50`, inclusive on both ends.
- Bare executable name: `clang++`.
- Machine-local absolute path: an escape hatch Trestle itself never writes.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `mode` | string | `native` | `native` or `wsl`. |
| `c` / `cxx` | string | `auto` | C / C++ compiler selector. |
| `msvc` | string | `auto` | MSVC toolset selector. |
| `archiver` | string | empty | Archiver override (`lib`/`ar`). |
| `linker` | string | empty | Linker override. |
| `setup` | string | empty | `vcvars64.bat` or environment setup script. |
| `cuda` | string | empty | CUDA version selector (`12.0~12.9`). |
| `cuda_root` | string | empty | CUDA root override. |
| `cuda_mode` | string | `whole` | `whole` or `rdc`. |
| `cuda_architectures` | string[] | `["sm_75"]` | Target GPU architectures. |
| `cuda_execution` | string | `native` | Whether CUDA runs in `native` or `wsl`. |
| `cuda_wsl_distribution` | string | empty | WSL distribution holding CUDA. |
| `vulkan` | string | empty | Vulkan SDK root. |
| `vulkan_execution` | string | `native` | Whether Vulkan shader tools run in `native` or `wsl`. |
| `vulkan_wsl_distribution` | string | empty | WSL distribution holding Vulkan SDK tools. |
| `module_scanner` | string | empty | `clang-scan-deps` executable. |
| `cache_dir` | string | `.trestle/toolchain` | Toolchain discovery cache; also settable via `TRESTLE_TOOLCHAIN_CACHE`. |
| `wsl_distribution` | string | empty | Distribution used when `mode = "wsl"`. |
| `preset` | string | empty | Referenced compiler preset name. |

## `[compiler_presets.<name>]`

A named, reusable toolchain selection. Rules and `package.optimization = "custom:<preset>"` can refer to it instead of repeating compiler and flag settings. An empty `mode` inherits the project toolchain.

| Key | Type |
| --- | --- |
| `c` / `cxx` / `archiver` / `linker` / `setup` | string |
| `mode` | string (`native` or `wsl`) |
| `wsl_distribution` | string |
| `compile_flags` / `c_flags` / `cxx_flags` / `link_flags` | string[] |

## `[vcpkg]`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `root` | string | `VCPKG_ROOT` | vcpkg installation root. |
| `triplet` | string | `auto` | vcpkg triplet; `auto` picks one from the target architecture. |
| `crt_linkage` | string | empty | `static` or `dynamic`, used to match a triplet. |
| `library_linkage` | string | empty | `static` or `dynamic`, used to match a triplet. |

## `[conan]`

Used by packages with `provider = "conan"`.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `root` | string | empty | Conan cache (home) directory. |
| `build_root` | string | build directory's parent | Parent folder for per-profile `conan-<profile>` installs. |
| `profile` | string | `default` | Profile resolved by `conan profile detect`. |
| `settings` | table | empty | Passed through as `-s` on every install for reproducible cross builds. |
| `options` | table | empty | Passed through as `-o` on every install. |

## `[targets.<name>]`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `type` | string | required | `static`, `shared`, `executable`, `test`, or `shader`. |
| `sources` | string[] | required, non-empty | Source globs such as `src/*.cpp`. |
| `output_name` | string | target name | Artifact file name. |
| `when` | string | empty | Condition expression; a false condition makes the target unavailable. |
| `requires_tools` | string[] | `[]` | Tools that must exist before building. |
| `requires_wsl` | string | empty | WSL distribution that must exist. |
| `c_standard` / `cxx_standard` | string | empty | Override the global standards. |
| `export_all_symbols` | bool | `false` | Export all symbols from a shared library. |
| `include_dirs` | string[] | `[]` | Public include directories (propagate to dependents). |
| `private_include_dirs` | string[] | `[]` | Include directories for this target only. |
| `defines` | string[] | `[]` | Public defines. |
| `private_defines` | string[] | `[]` | Private defines. |
| `compile_options` | string[] | `[]` | Compile options. |
| `c_flags` / `cxx_flags` | string[] | `[]` | C-only / C++-only compile flags. |
| `library_dirs` | string[] | `[]` | Library directories. |
| `libraries` | string[] | `[]` | Libraries to link. |
| `link_options` | string[] | `[]` | Link options. |
| `interface_compile_options` | string[] | `[]` | Compile options propagated to dependents only. |
| `interface_link_options` | string[] | `[]` | Link options propagated to dependents only. |
| `shader_stage` | string | empty | **Required** for `shader` targets: `vert`, `frag`, `comp`, `geom`, `tesc`, `tese`. |
| `shader_entry` | string | empty | Shader entry point name. |
| `shader_tool` | string | empty | Shader compiler (for example `glslc`). |
| `shader_target_env` | string | empty | Target environment (for example `vulkan1.1`). |
| `dependencies` | array of tables | `[]` | Dependency edges, see below. |
| `test_group` | string | empty | Test group name used by `trestle test --group`. |
| `test_args` | string[] | `[]` | Arguments passed when running the test. |
| `test_working_dir` | string | empty | Working directory for test runs. |

### `[[targets.<name>.dependencies]]`

| Key | Type | Description |
| --- | --- | --- |
| `target` | string | A project target dependency. Exactly one of `target` and `package` must be set. In a workspace, `member:target` refers to another member's target. |
| `package` | string | A declared package dependency. |
| `scope` | string | `private` (default), `public`, or `interface`. |

## `[workspace]`

Turns a root `trestle.toml` into the single build entry point for several member projects. Without this section the project stays a standalone project with unchanged behavior.

| Key | Type | Description |
| --- | --- | --- |
| `members` | string array | **Required, non-empty.** Member directories relative to the workspace root; each must contain its own `trestle.toml`. Absolute paths and `..` escapes are rejected. |
| `dependencies` | array of inline tables | Dependencies shared by every member. Each entry must set `port`, which becomes the reference name inside members. |

Merge rules:

- `[toolchain]`, `[vcpkg]`, and `[conan]` are inherited from the root field by field; `[compiler_presets]` are unioned with member definitions winning.
- `[build]` profile, language standards, and flags stay per member; `build_dir` and `compile_commands` come from the root, with member artifacts under `build/<profile>/<member>/`.
- Root `[packages]` and `[workspace].dependencies` are visible to every member, with member declarations winning on conflicts.
- Target names must be unique across the workspace. Cross-member dependencies build in dependency order; root targets build last.
- The root may be a pure container (no `[targets]`) or declare aggregate targets.

```toml
schema_version = 7
[project]
name = "myproj"

[workspace]
members = ["core", "cli"]

[[workspace.dependencies]]
provider = "vcpkg"
port = "fmt"
```

## `[packages.<name>]`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `provider` | string | `vcpkg` | Empty, `vcpkg`, `conan`, or `manual`. |
| `port` | string | map key | Upstream package name. |
| `version` | string | empty | Conan version; features belong on the reference name, not the version. |
| `features` | string[] | `[]` | Enabled package features. |
| `triplet` | string | empty | Override the vcpkg triplet. |
| `include_dirs` / `library_dirs` / `libraries` / `defines` | string[] | `[]` | Manual (`manual`) usage paths. |
| `runtime_files` | string[] | `[]` | Runtime files to deploy with the artifact (for example DLLs). |

## `[package]`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `format` | string | `zip` | Only `zip` is supported. |
| `output` | string | `dist/<project>.zip` | Archive output path. |
| `optimization` | string | `balanced` | `balanced`, `speed`, `size`, or `custom:<preset>`. |
| `targets` | string[] | `build.default_targets` | Targets to package. |

## `[[rules]]`

Rules apply in TOML order; a false condition leaves the configuration unchanged.

| Key | Type | Description |
| --- | --- | --- |
| `when` | string | **Required** condition expression. |
| `target` | string | When set, the flags and dependencies below affect only this target. |
| `c` / `cxx` | string | Set the compiler (project-wide). |
| `mode` | string | `native` or `wsl` (project-wide). |
| `wsl_distribution` | string | WSL distribution (project-wide). |
| `compile_flags` / `c_flags` / `cxx_flags` / `link_flags` | string[] | Scope depends on whether `target` is set. |
| `packages` | string[] | Add external package dependencies to `target`; **requires `target`**. |
| `depends_on` | string[] | Add project target dependencies to `target`; **requires `target`**. |
| `targets` | string[] | Set the default targets (project-wide). |

Every referenced target, package, and dependency name must be declared.

## `[tasks.<name>]`

A task is an explicitly confirmed argv command followed by whitelisted configuration edits.

| Key | Type | Description |
| --- | --- | --- |
| `description` | string | Task description. |
| `command` | string[] | Executed as argv, without a shell. |
| `working_dir` | string | Project-relative or absolute; defaults to the project directory. |
| `timeout` | string | Positive duration such as `30s` or `5m`; omitted means no limit. |
| `set` | table | Configuration edits applied after success; whitelisted keys only. |

Allowed `set` keys: `build.profile`, `toolchain.c`, `toolchain.cxx`, `toolchain.msvc`, `toolchain.mode`, `toolchain.wsl_distribution`, `vcpkg.root`, `vcpkg.triplet`, `conan.root`, `conan.profile`.

## Condition expressions

`when` uses a deliberately small, side-effect-free expression:

- Operators: `&&`, `||`, `!`, `==`, `!=`, parentheses.
- Variables: `os`, `arch`, `mode` (all strings).
- Read-only queries (booleans, except `env` which returns a string):
  - `tool("name")`: look up a tool in the current mode; WSL mode searches the selected distribution.
  - `wsl("distribution")`: whether a distribution exists.
  - `wsl_tool("distribution", "tool")`: whether a tool exists in a distribution.
  - `pkg("name")`: whether a declared package is resolvable.
  - `env("VAR")`: read an environment variable.
  - `path("path")`: whether a path exists; relative paths resolve against the directory holding `trestle.toml`.

```toml
[[rules]]
when = 'os == "windows" && wsl_tool("Ubuntu", "clang++")'
mode = "wsl"
wsl_distribution = "Ubuntu"
cxx = "clang++"
```

## Validation and error codes

| Code | Meaning |
| --- | --- |
| `E_CONFIG_NEWER_SCHEMA` | The file requires a schema newer than this executable. |
| `E_CONFIG_OLD_SCHEMA` | The schema is too old and cannot be migrated. |
| `E_CONFIG_MISSING_REQUIRED` | Missing `project.name`, or no targets at all. |
| `E_CONFIG_INVALID_VERSION` | A `[toolchain]` version selector cannot be parsed. |
| `E_CONFIG_CONFLICT` | A concurrent edit was detected before saving. |
| `E_CONFIG_BACKUP` | The pre-migration original could not be preserved. |
| `E_DEP_CYCLE` | The target dependency graph has a cycle. |
| `E_WORKSPACE_INVALID` | A workspace member is missing, escapes the root, is listed twice, or two members share a target name. |
| `E_WORKSPACE_CYCLE` | Members depend on each other. |

In addition, `: unknown configuration fields: ...` reports an unrecognized key.

## See also

- [Command-line reference](cli.en.md)
- [Build a C++ project with fmt from scratch](tutorial.en.md)
- [Workspace guide](workspace.en.md)
