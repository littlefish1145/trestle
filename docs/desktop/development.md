# Build and run Trestle Desktop

The desktop builds as a separate Rust crate against GPUI 0.2.2. A locked
dependency graph is in `desktop/Cargo.lock`. The Linux development build was
checked with Rust 1.100.0 and Go 1.26.0. Build the Core and desktop from the same
checkout. The app resolves Core through `TRESTLE_CORE`, an adjacent
`trestle` executable, then PATH. Ninja and the compiler selected by the
workspace must be available to Core.

## Linux (Debian/Ubuntu)

Install Rust with rustup and Go using their official installers. On Debian
13 the system dependencies include the following packages.

```sh
sudo apt-get update
sudo apt-get install build-essential pkg-config ninja-build libxcb1-dev \
  libxkbcommon-dev libxkbcommon-x11-dev libfontconfig1-dev libfreetype-dev \
  libvulkan1 mesa-vulkan-drivers
```

Use a working X11 or Wayland session and Vulkan driver. Native file picking
on Linux uses the desktop portal and needs a running session D-Bus and a
suitable `xdg-desktop-portal` backend. The **Enter path…** action is also
available.

From the repository root:

```sh
mkdir -p dist/desktop
go build -mod=readonly -o dist/desktop/trestle ./cmd/trestle
cargo build --locked --manifest-path desktop/Cargo.toml
TRESTLE_CORE="$PWD/dist/desktop/trestle" \
  desktop/target/debug/trestle-desktop examples/hello-workspace
```

Use `cargo build --release --locked` for an optimized build. For a local
two-binary distribution, place the resulting desktop binary and Go CLI in
the same directory. Use a copy of the example if the checkout should remain
free of generated build files.

## Windows 11 (PowerShell)

Install Go 1.26.0 or newer, VS 2022 Build Tools with **Desktop development with
C++** and the Windows SDK, Rust via rustup with the
`x86_64-pc-windows-msvc` host toolchain, and Ninja on PATH.
Use Developer PowerShell when required by the selected build tools.

```powershell
New-Item -ItemType Directory -Force dist/desktop | Out-Null
go build -mod=readonly -o dist/desktop/trestle.exe ./cmd/trestle
cargo build --locked --manifest-path desktop/Cargo.toml
$env:TRESTLE_CORE = (Resolve-Path dist/desktop/trestle.exe).Path
& desktop/target/debug/trestle-desktop.exe examples/hello-workspace
```

Windows GPUI builds and runtime interactions require verification on Windows.
macOS runtime verification is also pending.

## Local files

Recent paths and up to 24 terminal operation summaries are stored under
`$XDG_STATE_HOME/trestle-desktop` (default `~/.local/state/trestle-desktop`),
`%LOCALAPPDATA%\trestle-desktop`, or
`~/Library/Application Support/trestle-desktop`. The history is versioned JSON
with one file per run. Desktop replay files are capped and temporary.
Full logs use the existing per-project `.trestle/runs` archive, with the
same retention behavior as the CLI. Clear those archives separately when
they are no longer needed.

GPUI 0.2.2 currently depends on a package path that uses `xattr` 0.2.3.
The lockfile retains a compatible `libc`, `mio`, and `command-fds`.
Keep builds locked when checking out or packaging.
