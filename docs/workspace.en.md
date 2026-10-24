# Workspaces: one root configuration, many projects

A Trestle workspace lets one root `trestle.toml` reference several member projects, share toolchain and dependency declarations, and build them in dependency order. Without a `[workspace]` section a project stays standalone with unchanged behavior.

This guide uses the runnable [examples/hello-workspace](../examples/hello-workspace/): `core` provides a static library and `cli` links it into an executable.

## 1. Layout

```text
hello-workspace/
  trestle.toml        # root: [workspace] members and shared settings
  core/
    trestle.toml      # member: static library + test
    include/greeter/greeter.h
    src/greeter.cpp
    tests/greeter_test.cpp
  cli/
    trestle.toml      # member: executable depending on core:greeter
    src/main.cpp
```

Artifacts are isolated per member:

```text
build/
  debug/
    core/bin/...            # core library and intermediates
    cli/bin/hello-workspace # cli executable
    compile_commands.json   # aggregated compile commands for every member
```

## 2. Root configuration

```toml
schema_version = 7

[project]
name = "hello-workspace"

[build]
profile = "debug"
build_dir = "build"

[toolchain]
c = "auto"
cxx = "auto"
mode = "native"

[workspace]
members = ["core", "cli"]
```

- `members` are workspace-relative directories, and **each must contain its own `trestle.toml`**. Absolute paths and `..` escapes are rejected.
- Target names are unique across the workspace; duplicates fail with `E_WORKSPACE_INVALID`.
- The root may be a pure container (as above) or declare its own targets. Root targets build last, with artifacts under `build/<profile>/`.

## 3. Member configuration

### Static library member (core)

```toml
schema_version = 7

[project]
name = "hello-core"

[build]
default_targets = ["greeter"]

[targets.greeter]
type = "static"
sources = ["src/greeter.cpp"]
include_dirs = ["include"]

[targets.greeter_test]
type = "test"
sources = ["tests/greeter_test.cpp"]
test_group = "unit"
dependencies = [{ target = "greeter", scope = "public" }]
```

Member-relative paths (sources, `include_dirs`, `library_dirs`, and so on) resolve against the **member directory**, exactly like a standalone project.

### Depending on another member (cli)

```toml
schema_version = 7

[project]
name = "hello-cli"

[build]
default_targets = ["hello"]

[targets.hello]
type = "executable"
sources = ["src/main.cpp"]
output_name = "hello-workspace"
dependencies = [{ target = "core:greeter", scope = "public" }]
```

Cross-member dependencies use `member:target`. A `public` scope passes `greeter`'s include directories, defines, and link artifact to `hello`; `private` keeps them local and `interface` only re-exports them. Transitive chains (A→B→C) work too.

## 4. Merge rules

| Setting | Source |
| --- | --- |
| `[toolchain]`, `[vcpkg]`, `[conan]` | Inherited from the root when the member omits them; member values win |
| `[compiler_presets.*]` | Union of root and member presets, member definitions win on conflicts |
| `[build]` profile, standards, flags | Per member |
| `build_dir`, `compile_commands` | Root; member outputs go to `build/<profile>/<member>/` |
| `[packages.*]` | Root packages are visible to members; member declarations win |
| `[workspace.dependencies]` | Shared dependencies, named by `port`, added to every member |

Shared dependency example:

```toml
[[workspace.dependencies]]
provider = "vcpkg"
port = "fmt"
```

Members reference it as `package = "fmt"` without repeating the declaration (installing the vcpkg port still follows the standalone flow).

## 5. Commands

```powershell
# Build
trestle build -C examples/hello-workspace            # every member in dependency order
trestle build -C examples/hello-workspace -p core    # one member
trestle build -C examples/hello-workspace cli:hello  # member:target shorthand

# Test and checks
trestle test -C examples/hello-workspace             # members without tests are skipped
trestle test -C examples/hello-workspace -p core     # core only
trestle doctor -C examples/hello-workspace           # check every member
trestle doctor -C examples/hello-workspace -p cli    # check one member

# Editor configuration
trestle editor-config -C examples/hello-workspace
```

After a build, all member compile commands are merged into:

- `build/<profile>/compile_commands.json`
- the root's `build.compile_commands` (default `compile_commands.json` at the root)

`editor-config` writes `Trestle: Build <member>` for each member plus `Trestle: Run <member>:<target>` for executables, with `member:target` values in the target picker.

## 6. TUI

```powershell
trestle -C examples/hello-workspace
```

In a workspace the sidebar is a member tree, and the digits open the matching row:

```text
▼ Root
  1 Overview
  2 Toolchains
  3 Build All
▼ core
  4 Targets (2)
  5 Packages
▼ cli
  6 Targets (1)
  7 Packages
```

`Build All` opens the Build screen scoped to every member: it lists each member with its target count, and `b` (or Enter with the content focused) builds them all.

- The `Root` group holds workspace-wide screens: Overview, Toolchains, and a Build screen scoped to every member.
- A member group shows that member's Targets and Packages; the Targets and Tests screens are scoped to the selected member.
- Selection behaves exactly like a standalone project: ↑↓ switches the screen immediately (wrapping around), digits jump directly, and Enter moves focus into the content pane. `b` builds the current scope, which means every member on the Build screen.
- The header shows `ws: <name>` and `pkg: <member|all>`.
- `w` opens a member prompt for quick switching (empty means all members); `b`, the Tests screen, and Doctor then apply to that member.
- A member's Packages screen is read-only: declare packages in that member's own `trestle.toml`. Clear the member selection to manage shared packages on the Root.
- Build output is prefixed per member, such as `[core] Building CXX object ...`.

## 7. Output layout and ordering

- Member artifacts: `build/<profile>/<member>/`; root targets: `build/<profile>/`.
- Members build in topological order of their dependency edges; because `cli` depends on `core:greeter`, `core` builds first.
- Cross-member linking injects the provider artifact's real path (`.a`, `.lib`, `.so`, `.dylib`, or the `.dll` import library), so no library names are hard-coded.
- Member dependency cycles fail immediately with `E_WORKSPACE_CYCLE`.

## 8. Troubleshooting

| Symptom | Fix |
| --- | --- |
| `E_WORKSPACE_INVALID: ... not found` | The member directory lacks `trestle.toml`, or `members` is misspelled. |
| `E_WORKSPACE_INVALID: target "x" is declared by both ...` | Two members share a target name; rename one. |
| `E_WORKSPACE_INVALID: dependency target "a:b" ... unknown target` | The `member:target` reference is wrong or the target belongs to another member. |
| `E_WORKSPACE_CYCLE` | Members depend on each other; break the cycle. |
| A member cannot find a header | Declare `include_dirs` in the member that owns the header and reference it via `member:target`. |
| The linker cannot find a library | Check the dependency scope is `public`/`private` and the provider is a `static`/`shared` target. |

## See also

- [Command-line reference](cli.en.md)
- [`trestle.toml` reference](configuration.en.md)
- [Runnable example](../examples/hello-workspace/)
