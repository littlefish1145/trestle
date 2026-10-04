# Build a C++ project with fmt from scratch

This walkthrough installs tools, initializes a project, configures a compiler, declares and installs fmt, builds and runs the program, and creates a release ZIP. A runnable copy is in [examples/hello-fmt](../examples/hello-fmt/). Downloading vcpkg and building dependencies requires network access; the first installation can take several minutes.

## 1. Install Trestle and the build tools

Download the package matching your host OS and CPU from [Trestle Releases](https://github.com/littlefish1145/trestle/releases), run its installer, and open a new terminal. Alternatively, install Go 1.26+ and build from source:

```sh
git clone https://github.com/littlefish1145/trestle.git
cd trestle
go build -o trestle ./cmd/trestle
```

On Windows, use `go build -o trestle.exe ./cmd/trestle`. Add the executable's directory to PATH and check `trestle version`.

### Windows: PowerShell and the MSVC ABI

Install Visual Studio Build Tools with the **Desktop development with C++** workload and Windows SDK. Install LLVM if using clang-cl. Install Git, CMake and Ninja, and verify them:

```powershell
git --version
cmake --version
ninja --version
git clone https://github.com/microsoft/vcpkg.git C:\src\vcpkg
& C:\src\vcpkg\bootstrap-vcpkg.bat
$env:VCPKG_ROOT = 'C:\src\vcpkg'
```

The example uses an x64 compiler and `x64-windows`. For an ARM64 target use `arm64-windows` and the corresponding compiler environment. MinGW needs `x64-mingw-dynamic` or `x64-mingw-static`; MSVC libraries are incompatible. Explicit triplets take priority; ambiguous automatic selection requires a choice.

### Linux: Ubuntu/Debian and Bash

```sh
sudo apt-get update
sudo apt-get install -y build-essential git cmake ninja-build curl zip unzip tar pkg-config
git clone https://github.com/microsoft/vcpkg.git "$HOME/vcpkg"
"$HOME/vcpkg/bootstrap-vcpkg.sh" -disableMetrics
export VCPKG_ROOT="$HOME/vcpkg"
```

Use `x64-linux` on x64 and `arm64-linux` on ARM64. On other distributions install equivalent tools using your package manager.

### macOS: Bash/zsh

```sh
xcode-select --install
brew install git cmake ninja pkg-config
git clone https://github.com/microsoft/vcpkg.git "$HOME/vcpkg"
"$HOME/vcpkg/bootstrap-vcpkg.sh" -disableMetrics
export VCPKG_ROOT="$HOME/vcpkg"
```

Apple Silicon uses `arm64-osx`; Intel uses `x64-osx`. Rosetta and native terminals may have different architectures: match your compiler and dependencies. See the official [vcpkg host requirements](https://learn.microsoft.com/en-us/vcpkg/concepts/supported-hosts) and [triplet documentation](https://learn.microsoft.com/en-us/vcpkg/concepts/triplets).

## 2. Initialize the project

Run in the directory where you want the project:

```sh
trestle init -C hello-fmt -name hello-fmt
cd hello-fmt
```

This creates `trestle.toml`, `src/main.cpp` and `.gitignore`. Existing configuration is protected from replacement. Replace `src/main.cpp` with:

```cpp
#include <fmt/core.h>
int main() {
    fmt::print("Hello from Trestle + fmt!\n");
    return 0;
}
```

## 3. Configure the compiler and dependency target

Windows x64, PowerShell:

```powershell
trestle configure -toolchain clang-cl -msvc "14.30~14.50" -profile debug -vcpkg-root $env:VCPKG_ROOT -triplet x64-windows
```

`-msvc` takes a version range rather than a path, so `trestle.toml` records `msvc = "14.30~14.50"` instead of one machine's `VC/Tools/MSVC/14.44.35207` directory and the file stays usable on other machines. `trestle toolchain` lists every version you could pin; `trestle toolchain --refresh` rescans after installing a new toolset. Bounds compare only the components they spell out, so `14.30~14.50` accepts `14.44.35207`. `-cuda` works the same way, for example `-cuda "12.0~12.9"`.

You can also use `-toolchain cl`. LLVM must be compatible with the installed Visual Studio STL: if STL1000 reports an old Clang version, update LLVM or select MSVC. Trestle finds the Visual Studio environment automatically from the `msvc` range; if discovery fails, add `-setup 'C:\actual-VS-install\VC\Auxiliary\Build\vcvars64.bat'`. Use an existing script for the target architecture.

Linux x64:

```sh
trestle configure -toolchain g++ -profile debug -vcpkg-root "$VCPKG_ROOT" -triplet x64-linux
```

macOS Apple Silicon; substitute `x64-osx` on Intel:

```sh
trestle configure -toolchain clang++ -profile debug -vcpkg-root "$VCPKG_ROOT" -triplet arm64-osx
```

For a Windows dynamic CRT triplet, add `crt_linkage = "dynamic"` inside the existing `[vcpkg]` table. This selects `/MDd` for debug and `/MD` for release.

## 4. Declare and install fmt

Append these tables to `trestle.toml`. Edit existing tables instead if already present:

```toml
[[targets.app.dependencies]]
package = "fmt"
scope = "private"

[packages.fmt]
provider = "vcpkg"
port = "fmt"
```

The package inherits `[vcpkg].triplet`. Install on Windows:

```powershell
trestle vcpkg -root $env:VCPKG_ROOT -install -ports fmt -triplet x64-windows
```

Linux/macOS:

```sh
# On macOS substitute arm64-osx or x64-osx; ARM64 Linux uses arm64-linux.
trestle vcpkg -root "$VCPKG_ROOT" -install -ports fmt -triplet x64-linux
```

The package is installed under `$VCPKG_ROOT/installed/<triplet>/`. CLI `vcpkg -install` installs ports; the TOML above declares project dependencies. Installing through the TUI Packages page also adds the dependency to the project.

## 5. Build, run and release

```sh
trestle doctor
trestle build app
```

Windows: `& .\build\debug\bin\hello-fmt.exe`.

Linux/macOS: `./build/debug/bin/hello-fmt`.

Expected output:

```text
Hello from Trestle + fmt!
```

Generated files include `build/debug/build.ninja` and the root `compile_commands.json`. Required Windows DLLs are copied beside the executable. After editing a source, rerun `trestle build app` for an incremental build.

```sh
trestle release
```

The default outputs are `build/release/bin/hello-fmt[.exe]` and `dist/hello-fmt.zip`. The archive contains the program and runtime dependencies under `bin/`. Release persists its profile and optimization options; return to debug with `trestle configure -profile debug`.

## 6. The complete dependency workflow in WSL

Open the target distribution and install **Linux Trestle inside it**, then follow all Linux steps above. Prefer `~/hello-fmt` for the project and `~/vcpkg` for dependencies, with `x64-linux` or `arm64-linux`. Windows libraries in `C:\src\vcpkg\installed\x64-windows` cannot link into Linux binaries.

To check Windows-driven WSL compilation, use a separate project without dependencies. Substitute your actual distribution name:

```powershell
wsl --list --verbose
wsl -d Ubuntu-24.04 --exec sh -lc 'command -v g++; command -v gcc; command -v ar'
trestle configure -mode wsl -wsl-distribution Ubuntu-24.04 -toolchain /usr/bin/g++
trestle doctor
trestle build app
```

C/C++ compilers, the archiver and the linker must exist inside the selected distribution. Trestle translates Windows source and output paths. Paths into a different distribution, such as `\\wsl.localhost\OtherDistro\...`, are rejected. Windows-driven WSL does not install Linux ports; the complete fmt workflow runs inside Linux.

Explicit real WSL verification, from the Trestle checkout in PowerShell:

```powershell
$env:TRESTLE_TEST_WSL = '1'
$env:TRESTLE_WSL_DISTRIBUTION = 'Ubuntu-24.04'
go test -v ./internal/app -run '^TestRealWSLBuild$' -count=1
```

The test reports `SKIP` when disabled, WSL is unavailable or no distribution is specified. A skip is not successful WSL verification.

## 7. Logs, the TUI and recovery

Open `trestle tui`, select Toolchains, then Doctor → Packages → Build. In Settings, Enter opens a category; `/` searches keys, labels and values live; Esc returns. Tab/Shift+Tab changes focus. F2 opens full-screen output, F3 shows complete errors or field values, F4 opens history, and F5 collapses output. Scrolling up pauses following; End resumes. `/` searches logs, `n` finds the next match, and `?` opens scrollable help.

Each operation stores `.trestle/runs/<ID>/output.log`; `record.json` contains its times, configuration fingerprint and final status. `trestle logs` lists records and `trestle logs <ID>` prints a complete log. Logs are retained until manually deleted. Remove old ID directories only after their operations finish.

| Problem | Recovery |
| --- | --- |
| Compiler/Ninja discovery | Check PATH, run `trestle toolchain`, and configure an actual compiler. For MSVC check the VS C++ workload and setup script. |
| Package resolution/linking | Match vcpkg root, target OS/architecture/ABI, triplet, CRT and debug/release. Install fmt for that target and retry. |
| Resource busy | Read the resource, operation, PID and start time; cancel in its owning terminal or wait, then retry. Deleting lock files is unnecessary. |
| Configuration conflict | Refresh and review external changes, then edit or preview again. A stale snapshot cannot overwrite the latest file. |
| Migration failure | The original remains intact. First migration persistence saves `trestle.toml.schema-v*.bak`; verify schema/TOML before restoring it. |
| Failed/interrupted build | Fix the first error in the full log and rerun `trestle build app`. Incomplete generation is regenerated; existing compiled objects can be reused. |
| Interrupted task | Refresh and preview again; review external command effects before running. Those effects are not automatically rolled back. |
| Failed release | The previous ZIP remains intact; fix the error and rerun `trestle release`. |
| Log write failure | The operation refuses to start or is cancelled. Check disk space and permissions at the reported log path, then retry. |

CLI Ctrl+C and TUI Esc cancel managed process trees and wait for output and log finalization. After forced termination, history identifies interrupted records using OS locks. A lock file's existence does not imply ownership.
