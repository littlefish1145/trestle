# Desktop protocol v1

Launch `trestle desktop-rpc` as a child process, with independent stdin and
stdout pipes. Each line is one UTF-8 JSON-RPC 2.0 object. Requests use unique
string or integer IDs, and responses echo the ID. A client must wait for
`system.handshake({"versions":[1]})` before sending other calls. The result
contains the chosen version and supported methods. Core input is limited to
1 MiB per frame and 16 concurrent requests. Batches and client notifications
are unsupported. Core stderr is diagnostic output.

| Method | Parameters | Result |
| --- | --- | --- |
| `system.capabilities` | none | Protocol version, host platform and available methods |
| `workspace.list` | none | Open workspace summaries |
| `workspace.open` | `path` | Workspace snapshot for a directory or configuration file |
| `workspace.get` | `workspace_id` | Reload configuration and re-resolve the snapshot |
| `workspace.targets`, `graph.get` | `workspace_id` | Resolved target list with direct dependency target IDs |
| `graph.dependencies` | `workspace_id`, `target` | One resolved target |
| `toolchain.list` | none | Host component inventory |
| `toolchain.detect` | `workspace_id`, optional `member` | The resolved configured C/C++ toolchain |
| `toolchain.select` | `workspace_id`, `compiler`, optional `member` | Updated snapshot after native compiler selection is saved |
| `build.configure`, `build.start` | `workspace_id`, optional `member`, `targets`, `profile`, `all`; `build.start` also accepts `rebuild` | Queued operation with `build_id` |
| `build.cancel`, `build.status` | `build_id` | Operation snapshot |
| `build.log` | `build_id`, optional byte `offset` | Output replay page and `next_offset` |
| `build.history` | `workspace_id` | Retained operation snapshots |
| `analysis.summary` | `build_id` | Operation snapshot, timing records and measured per-manifest analysis |

A workspace ID is the Core's canonical config path. Open is read-only; build
and configure require an existing configuration. Profiles `debug` and
`release` are per-operation overrides. Configure generates Ninja and compile
database files. Rebuild runs the generated manifest's Ninja clean tool under
the build lock, on the chosen outputs, then runs the normal build. A repeat
without rebuild retains incremental behavior. Status is authoritative and
passes through queued and preparing before producing a terminal result.
Running is reported when there is Ninja edge progress. Cancellation requests
are asynchronous and terminal state follows process teardown.

`event.build.status`, `event.build.progress` and `event.build.output` are
notifications. Their params contain `version`, `build_id`, `sequence`,
`timestamp` and `data`. Sequence increases within each build. Events may be
delivered before or after an associated response. When the event queue fills,
notifications may be skipped. Clients should poll `build.status` and read
`build.log` by offset. Replay serves at most 64 KiB per request and retains
up to 8 MiB per run. Core writes the full process logs separately and reports
their paths. EOF cancels connection-owned operations and cleans temporary
buffers.

Errors include a numeric code and human-readable message. JSON-RPC parse,
invalid request, method and parameter errors use the standard codes. Trestle
uses `-32001` for negotiation, `-32003` for concurrency limits, and
`-32004` for a busy workspace. Other Core errors use `-32000`, with
diagnostic type, stage and log path when available.

Example handshake

```json
{"jsonrpc":"2.0","id":1,"method":"system.handshake","params":{"versions":[1]}}
```


## Ninja analysis

Desktop builds enable Ninja's explain mode. `analysis.summary.ninja_invocations`
contains up to 256 records with the member name, outcome, explicit no-work
evidence, reasons reported by Ninja and a measured plan critical path when
one can be attributed. Reason and path retention is bounded. Full explanations
remain in the operation logs. A zero-work invocation is an incremental result.
The optional per-invocation critical path uses the longest measured chain in
the generated action DAG, with timing from the current Ninja journal. Multiple
outputs of the same command contribute one interval. Unmeasured actions preserve
the dependency chain with zero weight. External cache hit rates and an
end-to-end workspace critical path remain null in v1.
