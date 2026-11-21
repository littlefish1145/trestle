# Desktop performance observations

Measured on Debian 13 x86-64, with a four-CPU cgroup quota, Go 1.26.0,
Rust 1.100.0, Ninja 1.13.0 and GPUI 0.2.2. The desktop was an unoptimized
build on Xvfb with Mesa software Vulkan. These measurements are development
samples, without OS page cache eviction.

## Native app

Three new desktop processes were launched with separate temporary project
and application state directories.

| Scenario | Process start to window map | Process start to workspace ready | Desktop RSS | Core RSS |
| --- | ---: | ---: | ---: | ---: |
| Successful build | 312 ms | 574 ms | 98,412 KiB | 13,948 KiB |
| Compiler error | 358 ms | 660 ms | 98,104 KiB | 13,824 KiB |
| Close during build | 314 ms | 629 ms | 97,932 KiB | 13,700 KiB |

Window mapping was detected through X11. The recent-workspace file was
observed after the desktop received the workspace snapshot and accepted
the selection. Both times include a 20 ms polling interval. RSS was read
from `/proc/<pid>/status` before starting the build. The successful run
took 966 ms and produced four Ninja timing records. The compiler-error run
took 447 ms. Closing during the stalled Ninja fixture reaped the Core and
Ninja processes and persisted the cancelled status.

The Rust transport integration measured 12.28–16.05 ms across two workspace-open calls
including deserializing three targets and a 12 ms polling interval.

## Graph layout and traversal

One debug run of `cargo test --locked --no-default-features scale_samples --
--nocapture`. The synthetic inputs are ten independent dependency chains.

| Nodes | Prepare inputs | Layer layout | Transitive highlight |
| --- | ---: | ---: | ---: |
| 100 | 0.956 ms | 0.087 ms | 0.007 ms |
| 1,000 | 4.063 ms | 0.915 ms | 0.041 ms |
| 10,000 | 47.699 ms | 8.700 ms | 0.645 ms |

Input preparation includes synthetic target construction. These timings
cover the data model and traversal. They exclude GPUI painting, GPU frame
time and pipe transfer. Targets, logs and timing rows use virtual lists.
The graph caches layer positions across frames and clips offscreen geometry.

Replay of 10,000 synthetic log lines (427,780 bytes) took 6.05 ms in a
debug unit test and left 4,000 visible-buffer rows. This measures Rust log
processing only.

## Remaining measurement work

Measure frame latency and RSS while scrolling representative multi-megabyte
logs, panning a 10,000-node native graph, and rapidly switching real
workspaces. Compare several fresh and warm runs. Repeat on Windows with
its native GPU backend before setting a platform performance budget.

## Larger native Workspace load

The Linux debug application was also launched against generated standalone
Trestle configurations containing real C++ targets. Each project uses a
shared source file and independent groups of 99 provider targets per
consumer. The observation ends after the window accepts the Core snapshot
and writes the recent-workspace entry.

| Targets | Process start to accepted snapshot | Desktop RSS on acceptance |
| ---: | ---: | ---: |
| 100 | 649 ms | 98,672 KiB |
| 1,000 | 588 ms | 101,948 KiB |
| 10,000 | 1,996 ms | 132,916 KiB |

These are separate single-run samples. They include framework startup,
Go resolution, transfer, Rust decoding and graph layout. Xvfb capture
did not reliably expose the large graph's rendered frames, so the samples
do not establish scrolling or panning frame latency.
