# Trestle Desktop architecture

## Repository audit (G0)

`cmd/trestle/main.go` owns command parsing and the terminal entry point. Its
dashboard adapter passes `internal/app` functions to the Bubble Tea model in
`internal/tui`. `internal/config` loads and validates TOML, merges workspace
members and protects writes against concurrent edits. `internal/policy` applies
conditional settings. The Core resolves dependencies and cross-member order in
`internal/graph`, which produces `internal/model` targets. `internal/toolchain`
discovers compiler components and resolves configured toolchains, including
Windows and WSL. `internal/plan` constructs actions, `internal/ninja` emits the
manifest, and `internal/runner` launches Ninja and decodes progress.
`internal/processx` owns process cancellation and `internal/runlog` archives
operation output. `internal/state` tracks Trestle state. Ninja owns incremental
execution. A skipped action or a Ninja output timestamp does not establish a
cache hit.

The existing CLI and TUI call non-interactive application functions for most
operations. Some commands print directly or require a terminal. In particular
`AnalyzeProject` summarizes source scanning and is not a build timeline API.
The alpha workflow runs Go tests, native compilation, race and platform
integration checks. CUDA and WSL have their own workflows.

## Boundary and data flow

- `internal/app/desktop.go`: typed inspectors and operations over configuration,
  toolchain detection, graph resolution and the existing build entry points.
- `internal/desktoprpc`: connection-owned workspace registry, operation
  lifecycle, protocol validation, a bounded event stream and log replay.
- `cmd/trestle`: `desktop-rpc` binds stdio before the normal CLI dispatch.
- `desktop/src`: Rust process transport, state and GPUI views. The desktop
  sends paths and selections; all configuration and build interpretation stays
  in Go.

The desktop starts the Core, negotiates a version, opens a local project
directory or `trestle.toml`, and receives the loaded configuration and resolved
targets. A build submits a workspace and optional member, targets and profile.
The Core assigns an operation ID, emits lifecycle events, dispatches the same
build pipeline used by CLI/TUI, and notifies completion once the subprocess
has returned. Cancellation signals that operation's Go context. Closing the
transport cancels all connection-owned work. Windows process termination uses
the existing taskkill integration; Linux terminates an owned process group.

## Stage order

| Stage | Scope |
| --- | --- |
| G1 | Project snapshots, configuration and toolchain selection, native operation dispatch |
| G2 | Versioned stdio transport, cancellable jobs, log replay and client supervision |
| G3 | GPUI window shell, navigation, themes and keyboard commands |
| G4 | Native directory picking, recents, target search and inspection |
| G5 | Build controls, progress, logs and diagnostic adapters |
| G6 | Core graph projection, cached layout, interaction and scale checks |
| G7 | Timing collection, history and explicit data availability |
| G8 | Linux/Windows runtime, shutdown, stress and performance checks |
| G9 | End-to-end tests and platform build documentation |

Linux needs a supported graphics stack and window system. Windows needs the
MSVC Rust toolchain and separate runtime verification; a Linux test cannot
validate Win32 GPU, DPI, or cancellation behavior. Paths are passed as process
arguments or JSON values, and the transport is local to an explicitly opened,
trusted project.
