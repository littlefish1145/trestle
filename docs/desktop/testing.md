# Desktop validation

## Automated checks

```sh
TRESTLE_TEST_NATIVE=1 TRESTLE_TEST_COMPILER=g++ go test -count=1 ./...
go vet ./...
TRESTLE_TEST_NATIVE=1 TRESTLE_TEST_COMPILER=g++ go test -race ./internal/desktoprpc ./internal/app
cargo fmt --manifest-path desktop/Cargo.toml --check
cargo test --locked --no-default-features --manifest-path desktop/Cargo.toml -- --nocapture
```

The Go native tests use a disposable copy of the workspace. They check
handshake and malformed input, workspace membership and dependencies,
manifest generation, real build success, compiler failure, streaming output,
log replay, timing records and process cancellation. The Linux cancellation
fixture deliberately stalls a Ninja stand-in so the test can cancel a
deterministically running process.

Run the Rust-to-Go integration with an isolated state directory and a clean
example copy.

```sh
case_dir=$(mktemp -d)
cp -R examples/hello-workspace "$case_dir/project"
TRESTLE_CORE="$PWD/dist/desktop/trestle" \
TRESTLE_TEST_WORKSPACE="$case_dir/project" XDG_STATE_HOME="$case_dir/state" \
  cargo test --locked --no-default-features --manifest-path desktop/Cargo.toml \
  --test core native_workspace_round_trip -- --ignored --nocapture
```

## Window smoke test

On Linux/X11, open the example, select a target, run Configure then Build,
and confirm the output contains a compiler invocation and the build completes.
Run `build/debug/cli/bin/hello-workspace` and confirm it prints
`Hello, workspace!`. Use Dependency graph to trace `hello` to `greeter`.
Open Analysis to see the measured output timings. Close with Ctrl+W and
verify the particular Core child has exited. In a temporary copy, introduce
a C++ compiler error and confirm Problems links it to a file and location.
Repeat the smoke test on Windows before claiming Windows support.

## Recorded results

- Go's repository-wide tests and vet passed on Linux with native GCC coverage
  enabled. The application and RPC packages also passed the race detector.
- The Windows x64 Go CLI and RPC executable cross-compiled. Its runtime and
  the Windows GPUI application still need a Windows host.
- The locked GPUI debug build linked on Linux. Six Rust unit/model tests and
  one explicitly enabled Rust-to-Go native integration passed. The transport
  test checks persisted history after closing and starting another Core.
- Native X11 smoke tests used Xvfb and Mesa's software Vulkan. The desktop
  built the real example, rendered its graph and analysis, and survived a
  compiler failure. The CLI then reused the desktop's outputs in the same
  workspace and reported no work for both members. Closing during an active
  Ninja fixture left no Core or Ninja process and persisted a cancelled run.
- Terminating the Core process left the X11 window responsive. The desktop
  showed a disconnected state and closed normally afterward.
