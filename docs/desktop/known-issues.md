# Desktop limitations

- Linux X11 has been exercised with a virtual display and software Vulkan.
  Physical GPU, Wayland, Windows and macOS runtime checks remain outstanding.
  The Linux native-file picker depends on a desktop portal. Path entry is
  available independently.
- Generated command inspection requires a configure or build operation in the
  current Core session. The inspector can display planned artifacts, executable
  paths, argument arrays and outputs for its retained plans. Before generation
  it shows configuration, sources and resolved dependencies.
- The graph can collapse a selected target's dependency branch while keeping
  shared providers visible for other branches. The measured-artifact filter
  shows the final artifact action duration where available. Per-target total
  timing and external cache hits remain unavailable.
- The analysis distinguishes Ninja's explicit incremental no-work result
  from compiler cache statistics. Ninja's explain output provides rebuild
  reasons. Its timing journal, correlated with the generated dependency DAG,
  provides a measured critical path per Ninja invocation. Dynamic dependencies
  outside that DAG, scheduling delays, failed-action durations and external
  cache hits remain unavailable. A replaced or incompatible journal produces
  no attributed timing.
- New Desktop logs expire according to the retention limits in the development
  guide. A persisted build summary can outlive a project log.
- Path entry and search use the platform text input handler with UTF-16
  composition and clipboard support. Tab and Shift+Tab navigate controls.
  Native IME sessions, high-DPI behavior and accessibility still need
  platform-specific manual testing.
- A future Rust release may reject a construct in the transitive
  `proc-macro-error2` package. The checked locked toolchain builds it.
