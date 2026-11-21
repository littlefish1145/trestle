# Desktop limitations

- Linux X11 has been exercised with a virtual display and software Vulkan.
  Physical GPU, Wayland, Windows and macOS runtime checks remain outstanding.
  The Linux native-file picker depends on a desktop portal. Path entry is
  available independently.
- The target inspector shows Core-resolved sources and dependencies, configured
  compiler selectors, effective graph options and configured output names. After generation, it also shows the Core's planned artifact path.
  It does not reconstruct every generated compiler command, resolved artifact
  path before generation, or per-target SDK selector.
- Dependency edges reflect the resolved target graph. The graph highlights
  direct, transitive and reverse dependencies. The selected-chain view hides
  unrelated nodes. The measured-artifact filter uses exact Ninja output records
  to show the final artifact action duration where available. Per-target total
  timing and cache status are not aggregated. Arbitrary subgraphs cannot be
  collapsed.
- Ninja timing records describe completed outputs. The journal cannot identify
  failed or queued actions, task wait time, equivalent multi-output actions,
  frontend compiler cache hits, reasons for rebuilding, or a critical path.
  A journal replacement or incompatible format produces no attributed timing.
  A future recorder can correlate a stable Core plan action ID, predecessor
  action IDs, Ninja output keys and per-action intervals before computing
  a DAG critical path. Cache status needs explicit provider telemetry.
- Full logs use the project's existing Go Core log archive. Desktop summaries
  are capped but the underlying Core log retention policy is unchanged.
- Search and path entry support basic key input and clipboard paste. Native IME,
  complete keyboard traversal, persisted UI settings, DPI validation and
  accessibility testing need further work.
- A future Rust release may reject a construct in the transitive
  `proc-macro-error2` package. The checked locked toolchain builds it.
