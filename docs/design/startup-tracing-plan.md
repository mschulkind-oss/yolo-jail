---
title: "Implementation plan: startup tracing"
status: accepted
stage: DECIDED
next: "Build bounded trace-store admission and the independent jail-source lexical parser; then wire production writers and refresh before backend claims"
depends-on:
  - startup-tracing.md
tags: [observability, timing, tracing, plan]
summary: "Tree-grounded handoff for the implemented tracing foundations and the remaining bounded store, production wiring, confined jail ingestion, and explicit export behavior."
---

# Implementation plan: startup tracing

**Status:** 2026-11-20. Completed against `d741f9aa603b57e47c0061d40a7c16d61e4bde9b` (tree `402c2977e45c0d7c72a5fce6b4914a336186984b`). The accepted behavior is in [startup tracing](startup-tracing.md). This is a partial implementation plan, not evidence that production tracing is built.

Precedence: the design wins on behavior; the tree wins on cosmetic fact; this plan is advice and is the first thing to be wrong. Stop and report without committing if a named symbol is gone, a task fence has unrelated work, a predicted red does not reproduce, a needed change crosses its fence, or a gate fails outside scope. Never twist code to match this plan.

**Do not reopen** [OQ-TR1–4](startup-tracing.md#decision-ledger) or the engineering contract in the accepted design. Recording remains off by default; existing user-scope `perf_logging` and invocation gates control it. There is no SDK, viewer, new recording key, mount widening, launch-time network request, or claim that runtime/backend/overhead proof has passed.

## Built foundations — reuse, do not schedule again

| Unit | Present in this tree | Limit of the claim |
| :--- | :--- | :--- |
| U1 | [`internal/perf/context.go`](../../internal/perf/context.go), [`perf.go`](../../internal/perf/perf.go), [`trace.go`](../../internal/perf/trace.go): trace/context identity, traced collector and lane sink | `initPerf` still constructs the legacy `perf.New` collector; `TraceSink` is not admitted storage or a safe join primitive. |
| U2 | [`internal/tracefile/read.go`](../../internal/tracefile/read.go), [`summary.go`](../../internal/tracefile/summary.go): bounded host-format reader, graph and summary | No id-less jail-source parsing, private identity bookkeeping or live refresh. |
| U3 | [`chrome.go`](../../internal/tracefile/chrome.go), [`otlp.go`](../../internal/tracefile/otlp.go), [`export_test.go`](../../internal/tracefile/export_test.go), and encoder goldens | Encoders exist and tests are present. The CLI does not call them yet. This is source evidence, not a test run in this plan. |
| U4a | [`internal/cli/trace.go`](../../internal/cli/trace.go), [`trace_test.go`](../../internal/cli/trace_test.go), registry/help wiring | Offline list/show exists. Help explicitly says export and live collection are not implemented. |
| U5 | [`internal/jailtrace/jailtrace.go`](../../internal/jailtrace/jailtrace.go), [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go), [`programphaseperf.go`](../../internal/entrypoint/programphaseperf.go), supervisor tests | Id-less Go and shell producers exist. Their current home-root file path and leaf-only safety are not the accepted dynamic source contract or all-component confinement. |
| Environment classification | [`internal/cli/run/unknownlaunchenv.go`](../../internal/cli/run/unknownlaunchenv.go) recognizes `YOLO_TRACEPARENT` | Recognition is not propagation; do not add another classification or make a launcher adopt ambient context. |

The present launch sink is still `newTimingLogWith` in [`internal/cli/run/runcmd.go`](../../internal/cli/run/runcmd.go); `initPerf` wires the legacy file and notice sinks. The current trace root helper, aggregate store, live claim protocol, private source registry, source converter, and export command are absent. Existing U3 and U4a test files are complete code surfaces, not tasks to recreate.

## Map and fences

Every row is a task-owned fence, not permission to edit an entire package. New files are proposals. Revalidate paths at dispatch.

| Task | Files and seams |
| :--- | :--- |
| U6a — store/admission | New `internal/paths/traces.go` and tests; new `internal/perf/tracestore.go` and tests. Machine-wide root under `paths.LogsDir()/traces`; admission, accounting, live claims, prune/recovery. |
| U6b — production launch/keeper | [`internal/cli/run/runcmd.go`](../../internal/cli/run/runcmd.go), [`run.go`](../../internal/cli/run/run.go), keeper plan/spawn/run and assembly files; source-named run tests. Add the trace as an independent sink without changing `host-perf.log`, report gates or slow-span notices. |
| U6c — deliberate child carriers | [`internal/cli/buildpool.go`](../../internal/cli/buildpool.go), [`buildreport.go`](../../internal/cli/buildreport.go), [`forkbuildchild.go`](../../internal/cli/forkbuildchild.go), [`capturehost.go`](../../internal/cli/capturehost.go); build/capture tests. |
| U7a — pure source reader | New `internal/tracefile/jailsource.go` and tests. Lexical parsing, bounded physical occurrences, exact producer spelling; no filesystem path or backend code. |
| U7b — registration, confinement and refresh | `internal/cli/run/jailperf.go`, `run.go`, `keeper.go`, [`internal/jailtrace/jailtrace.go`](../../internal/jailtrace/jailtrace.go), [`internal/entrypoint/programphaseperf.go`](../../internal/entrypoint/programphaseperf.go), new `internal/paths/traces.go`/store seams, and source-named tests. Includes the `.local/state/yolo-jail-traces` Go+shell producer path, descriptor-relative access, private registry and atomic refresh. Backend exposure tests belong here; do not widen mounts. |
| U8 — remaining host spans | `internal/cli/run/run.go`, matched wrapper `scripts/matched-bundle.py` and its source-named tests. Separate service child spans/env from launch-side service spans in `internal/cli/run/loopholesruntime.go` and verified `internal/hostservice` readiness sites. Split sub-tasks by file fence. |
| U9 — macos-user | [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go), `bootperf.go`, Darwin entrypoint/session environment seams and tests; Mac-only location/containment evidence stays explicit. |
| U10 — preparation acts | Scheduler and background-advance launch seams under `internal/cli`, with channel-driven tests. Each independently owned act gets a trace, not a parent launch. |
| U11a — offline export | Existing `internal/cli/trace.go` and `trace_test.go`; reuse `tracefile.WriteChrome` / `WriteOTLP`. Add explicit output/format selection, no network in this sub-task. |
| U11b — explicit OTLP POST | Same CLI command/test fence as U11a, after it lands; use a fake collector for transport/response tests. Do not edit the encoders to absorb CLI policy. |
| U12 — references | [`docs/reference/perf-logging.md`](../reference/perf-logging.md), user CLI/config references; each claim lands with the behavior it describes. No roadmap, `AGENTS.md`, changelog, or source changes under this plan. |

## Reuse and traps

- **Constraint:** `Options` crosses `launchRunPipeline` by value. `PerfRef` exists to publish the constructed collector to its caller; new trace state must survive that seam without duplicating the legacy collector.
- **Constraint:** `keeper.run` calls `initPerf` itself. Carry a deliberately minted trace/context in its plan; the keeper is another lane of that trace, not another root. Its final argv passes through `insertHostServiceEnv`.
- **Constraint:** the launcher and keeper can both produce `jail_main.spawned`; `container.create` uses the real spawning process, never sums or invents the two starts.
- **Constraint:** `startExternalService` builds `env` and may reconstruct `cmd.Env` for startup-reason fds. Put explicit trace context in the durable environment before reconstruction; ambient or manifest environment must not override deliberate context.
- **Constraint:** in-process capture reuses its collector/sink and starts an explicit child of the waiting span. The separate fork-build process takes only the deliberate hidden `--trace-context` argument, parsed with `perf.ParseContext`; absent/invalid context follows ordinary gates, and failure to join never creates a replacement trace.
- **Constraint:** `poolItem.check`/`build` already use `preparation.check` / `.build` through `phaseSpan` and `buildReport.preparationSpan`. Attach the key and correct concurrent parent there; do not duplicate spans.
- **Constraint:** every admitted writer/refresh uses the store protocol. The current `TraceSink` creates a missing directory, so it cannot be used unchanged to join; no late writer may recreate a pruned trace or join an unlinked lock inode.
- **Constraint:** source occurrence identity is `(host source instance, absolute byte offset)`, not content, timestamp, pid, normalized name or digest. Preserve exact eligible producer spelling privately for pairing; normalization is display-only. Distinct identical rows remain distinct.
- **Constraint:** source updates preserve committed associations, continuity, calibration, frozen explicit parents, prior observations and completed intervals transactionally. Replacement/shortening/conflicting prior bytes marks a source stale; a new registration is the only resume path.
- **Constraint:** replacement admission counts old and temporary copies simultaneously, including private registry/control content. If the peak does not fit, preserve the last good state; never delete evidence to make room.
- **Constraint:** append each admitted host event synchronously as it happens, without fsync, batching or intentional contention waits. Failed/nonblocking join or append degrades tracing with a bounded actionable warning; the legacy sink and launch continue. Never promise durable survival after kill.
- **Constraint:** opening and cleanup walk every untrusted source component descriptor-relative without following links. A final file is nonblocking, no-follow, and regular before reading; retain that descriptor. Uncertain ownership means no cleanup, not recursive deletion.
- **Advice:** use event/channel-driven fixtures, not sleeps or timing pass budgets. Use `testtmp.ChildEnv` for re-execs and canonicalize minted fixture paths where symlink resolution matters.

## Task order

U6a, U7a, and U11a have disjoint fences and can begin independently. U6b follows U6a. U6c follows U6b. U7b follows U6a, U6b and U7a. U8's run-file work serializes with U7b; service work can proceed on its own fence after U6b. U9 follows U6b/U7b. U10 follows U6a/U6b. U11b follows U11a; source refresh on reads is wired by U7b, without changing offline selection or encoder representation. U12 follows each shipped observable surface.

For every task, workers report the exact red and green commands, changed paths, and backend/runtime evidence separately. No test success implies a native backend claim. The implementation is not graduated until the proof obligations at the end of this plan are met.

## Remaining tasks

### U6a — machine-wide admission and retention

**Settled — do not reopen:** At most 50 retained traces, 200,000 content bytes per trace and 10,000,000 machine-wide at physical peak. Include metadata, events, sanitized snapshots, private source registry, ingestion bookkeeping, diagnostics/control files and temporary/crash-left copies. A live or uncertain trace is not prunable; unsupported or already-over-budget stores are not silently cleaned.

**Change:** Add a path helper for `~/.local/share/yolo-jail/logs/traces/` beside the existing global `logs/`. Add a store owner that serializes mint, writer/refresh admission, byte reservations, recovery and prune. Pure admission must decide before an effect creates or extends content. A replacement reserves old+temporary peak; if it cannot fit, leave the old file intact. Account orphan private/control content conservatively. A bounded diagnostic reserve is charged at peak; reservations are not buffered events.

**Tests first:** new store tests for fresh mint, 50-trace limit, equal-start ID tie-break, per-trace and total caps, control/private bytes, replacement peak, failed publication, crash-left temp, concurrent minters, live and unknown liveness, lock identity replacement, no join-mkdir after prune, and inability to account. Add a child-process admission race test rather than a same-process mutex-only witness. Red: `go test ./internal/perf ./internal/paths -run '^TestTraceStore' -count=1` (missing symbols/tests at the new fence).

**Gates:** `go test ./internal/perf ./internal/paths`; `git diff --check`. No launch/runtime claim. **Fence:** U6a rows only; do not wire `initPerf`. **Commit:** `feat(perf): admit and retain bounded traces`. **Report:** red/green, byte-accounting cases, crash recovery, and whether unsupported locking declined safely.

### U6b — traced production launch and keeper

**Settled — do not reopen:** Existing `perf_logging`, `--timing`, `--verbose`, `YOLO_TIMING` and `YOLO_VERBOSE` gates do not change. Construct a trace only when the recording gate is on. The trace sink is independent of `host-perf.log`; report printing remains controlled by the explicit invocation gate.

**Change:** In `initPerf`/`newTimingLogWith`, keep the current `perf.New` path behaviorally intact while adding a store-admitted traced collector for a root launch. Use the recording launch kind and existing workspace code; metadata has exactly the seven design keys and no paths. Carry trace ID and the keeper's waiting-span context in the keeper plan. The keeper acquires its own lane under the admitted-store protocol instead of minting a second root. Add the host trace context only at the intended runtime argv insertion point.

**Tests:** Extend the existing initialization tests to prove recording-off writes no trace; silent `perf_logging` recording creates spans but prints no table; `--timing` both records and reports; legacy log bytes/gates remain unchanged; two concurrent launches get distinct IDs; keeper shares trace identity and owns one lane; fresh launch mints despite ambient `YOLO_TRACEPARENT`; attach explicitly clears/overrides stale context. Exercise the actual assembled argv and keeper plan round trip. Red: `go test ./internal/cli/run -run 'TestInitPerf|Test.*Trace.*Keeper|Test.*Trace.*Attach' -count=1`.

**Gates:** `go test ./internal/cli/run`; targeted launch-path integration after implementation; dev-bundle absolute-launch smoke from a throwaway workspace. **Fence:** U6b only. **Commit:** `feat(run): record launch and keeper traces`. **Report:** red/green and sink/gate/identity evidence; do not claim a jail or backend source is ingested.

### U6c — deliberate build and capture joins

**Settled:** In-process sub-launches reuse the collector/sink and explicit waiting-span parent. The separate fork-build child receives the canonical context only through the hidden self-exec argument. Ordinary/nested launchers never adopt ambient context.

**Tests:** two concurrent build keys have distinct explicit child spans, correct key attributes and common parent; fork-build re-exec receives only the explicit validated context; absent/invalid hidden context follows ordinary gates and does not echo input; in-process capture opens no duplicate sink. Red: `go test ./internal/cli -run 'Test.*Build.*Trace|Test.*Capture.*Trace' -count=1`.

**Change/fence:** `buildpool.go`, `buildreport.go`, `forkbuildchild.go`, `capturehost.go` handover hunks only. Use the existing build phase spans. No inherited environment carrier. **Gates:** `go test ./internal/cli`; targeted integration that reaches a real fork-build child. **Commit:** `feat(cli): join build and capture traces`. **After:** U6b.

### U7a — bounded lexical jail-source reader

**Settled:** This is a pure parser, not a filesystem source opener. Accept producer rows `{t,k,name,pid}` and optional `res:"s"`; ignore unknown fields, including injected ids/parents/paths/credentials. Accept producer ASCII uppercase and hyphens; sanitize only the display name by lowercasing ASCII, replacing bytes outside `[a-z0-9_.]` with `_`, and limiting output to 64 bytes. Preserve coarse-second resolution as evidence, not invented precision.

**Tests:** new parser tests for valid Go/shell spellings, escaped and invalid JSON, nonintegral/out-of-range time and pid, unknown sensitive fields, malformed complete rows, physical byte offsets, identical rows at distinct offsets, last 1 MiB/2,000 complete-row caps, partial first/final rows, 2,000 lifetime occurrences, and exact-spelling eligibility (≤64 bytes and only `[A-Za-z0-9_.-]`). Assert that `Foo`/`foo`, `foo-A`/`foo_A`, and overlength names sharing a 64-byte display prefix remain distinct or ineligible, never falsely paired. Red: `go test ./internal/tracefile -run '^TestJailSource' -count=1`.

**Change:** new `internal/tracefile/jailsource.go`; return bounded observations with byte offset and original eligible spelling kept separate from sanitized output. Do not add filesystem reads or private-state persistence here. **Gate:** `go test ./internal/tracefile`. **Fence:** U7a only. **Commit:** `feat(tracefile): parse bounded jail trace observations`. **Independent of:** U6a and U11a.

### U7b — confined source registration and transactional refresh

**Settled:** Host-private registration stores each source instance's validated workspace/setup/backend and confined source-relative name outside the log root and outside service-retirement state. Limit 16 instances and 4,096 registry bytes per trace at peak. No locator appears in metadata, exported attributes or diagnostics. Fresh/attach source path is `~/.local/state/yolo-jail-traces/.yolo-trace-<traceid>.jsonl`, through the existing writable workspace-backed `.local` surface; no mount widening or per-file bind. Apply the path change consistently to Go and generated shell producers.

**Change:** Implement descriptor-relative no-follow opening, type/ownership/continuity validation, bounded reads, host-minted source-instance keys, private occurrence/pairing/calibration/parent bookkeeping, and serialized atomic snapshot+bookkeeping publication. Pair only byte-offset order within `(source instance,pid,exact eligible original spelling)`, and only when exactly one open start is supported by continuous evidence. Overlap remains unfinished; orphan end is diagnostic. Freeze a selected explicit host-wait parent. Calibrate before window filtering; persist the decision. A replaced, shortened or conflicting old range is stale. Refresh only adds occurrences/completes a supported unfinished span; preserve prior associations and completed intervals. Unsafe/missing source keeps the last good snapshot and reports a bounded category/count.

**Tests:** lexical parser integration with two identical rows at different offsets; ambiguous overlapping exact-name starts; normalized-name collisions; source replacement/shortening and continuity gaps; failure between snapshot and state publication yields old or new complete state only; frozen parent and calibration survive reread; monotonic refresh preserves completed spans; symlink in every path component, nonregular target, moved anchor, cleanup replacement race; no raw spelling/payload/path leaks. Red: `go test ./internal/cli/run ./internal/tracefile -run 'Test.*Jail.*(Source|Refresh|Trace)' -count=1`.

**Backend proof:** establish exact fresh/attach source mapping and private-leaf non-exposure on Podman, Apple Container, macos-user, and sealed capture/build variants. Where mapping or confinement cannot be proven, decline registration and retain a missing-source diagnostic; do not call the feature backend-complete. **Fence:** U7b; coordinate U6a shared store interface, U6b run hunk, U7a parser. **Gates:** `go test ./internal/cli/run ./internal/tracefile`; targeted integration; actual native Mac check when available. **Commit:** `feat(run): safely ingest jail trace sources`. **After:** U6a/U6b/U7a.

### U8 — host gaps and service startup

**Settled:** Add only families listed in design [§5.6](startup-tracing.md#56-what-is-added-to-the-record); do not duplicate present spans (`launch.refresh_configured_packs`, `launch.fork_builds`, `preparation.check/build`, `launch.auto_capture`, `launch.resolve_jail_prefix`, `launch.auto_load_image`, `launch.prepare_ws_state`, `launch.assemble_argv`, `launch.refresh_jail_briefings`, `macos_user.*`, `attach.exec`). `probes.done` remains a mark. Process wrapper and pre-collector intervals use actual available start evidence; unavailable OS-start evidence is reported, not invented.

**Tests/change:** event-driven `Run` seams cover each genuinely dark phase; wrapper source test checks start-time carrier, without invoking a fake timing pass budget. Service spans distinguish a service already running (`reused`, no child lane) from one launched by this invocation; child readiness spans end at the actual readiness event. Service startup reason survives the reconstructed durable environment. Failed startup leaves partial trace evidence without changing retry semantics.

**Red/gates:** source-named run tests; `go test ./internal/cli/run ./internal/hostservice`; wrapper's source-named tests; relevant targeted integration. **Fence:** split host-gap, wrapper, and service files into serial commits where shared files overlap. **Commit:** `feat(run): trace remaining startup phases` (host) and `feat(run): trace host service startup` (service). **After:** U6b; U7b serializes the shared `run.go` changes.

### U9 — macos-user propagation and source

**Settled:** Use the same explicit context in the session environment file for bootstrap and guest daemons; never treat ordinary environment as a launcher carrier. Read the streaming trace source and existing `boot-perf.log` without changing that legacy log's format. Fresh and attach sessions each produce their own expected source behavior.

**Tests/change:** fake `Deps` command/env assertions run on Linux. Native Mac verifies sandbox-account source location, host-safe descriptor read, fresh/attach `session.exec`, and no private registry access. A missing/unavailable Mac mapping degrades to missing source; no cross-platform success claim from Linux fakes. **Gates:** `go test ./internal/macosuser ./internal/entrypoint`; the recorded native Mac handoff. **Fence:** U9 files only. **Commit:** `feat(macosuser): join native startup traces`. **After:** U6b/U7b.

### U10 — independent preparation acts

**Settled:** Each scheduler act mints a `preparation` trace when it owns the act; it is not a child of a launch. A lock-skip opens no act. Overlap is shown by time only.

**Tests/change:** injected channel-controlled ordinary/plain/advance runners prove two acts mint distinct trace IDs, `done` closes each root, cancellation is preserved, and lock-skip mints nothing. Summary overlap lists an act without assigning launch parent. **Red/gates:** `go test ./internal/cli ./internal/tracefile -run 'Test.*Preparation.*Trace' -count=1`; then package tests. **Fence:** scheduler/advance files and source-named tests. **Commit:** `feat(cli): record independent preparation traces`. **After:** U6a/U6b.

### U11a — explicit offline file/stdout export

**Settled:** Reuse the existing reader selection and U3 encoders. The command is `yolo trace export [id] [--format chrome|otlp] [-o <file>]`. No id selects the latest-starting launch for this workspace (metadata `start`, then lexicographically greatest ID); never pick another workspace or a preparation trace. Default is Chrome. `-o` is a literal file, not a directory/suffix/stdout alias; without it, JSON goes to stdout and diagnostics to stderr.

**Tests first:** Extend the actual `traceMain` fixture: explicit/default selection, both encoders decode semantically, `-o` uses atomic publication, existing parent required, symlink/nonregular target refused, failed write leaves old file intact, stdout write errors fail, bad/repeated/missing flags and positional arguments produce no partial output. Red: `go test ./internal/cli -run '^TestTraceOfflineExport' -count=1`.

**Change:** export branch and help in `internal/cli/trace.go`; tests in `trace_test.go`; do not alter U3 wire representation. **Gates:** `go test ./internal/cli ./internal/tracefile`; `git diff --check`; absolute dev-bundle launcher `trace export --help` from a throwaway workspace. **Fence:** U11a only; no POST. **Commit:** `feat(cli): export a collected trace to a file`. **Independent:** U6a/U6b/U7; source refresh is wired later by U7b.

### U11b — one explicit OTLP POST

**Settled:** An endpoint is an exact absolute HTTP(S) URL with host, no userinfo/fragment, valid port and escapes. Preserve path/query byte-for-byte; do not add `/v1/traces` or a suffix. One POST only, no proxy, ambient authorization/cookies/credentials, redirects, retry, or negotiation. Use HTTPS normal certificate validation; total request and response deadline 10 seconds. Request body is existing OTLP JSON.

**Tests:** fake HTTP server/transport verifies exact URL/method/body/content type and no proxy/redirect/retry; invalid endpoints fail before request; test success, connection/timeout/status, oversized/unreadable/truncated/malformed response, decimal-string and integral numeric rejection counts, negative/fractional/overflow, unknown fields, and redaction. A success requires valid OTLP response and zero rejected spans. Nonzero rejected count is failure with count only. Response body limit 65,536 bytes plus one byte to detect excess. Use no real network or credentials. Red: `go test ./internal/cli -run '^TestTraceOTLP' -count=1`.

**Change:** exact `--otlp-endpoint <url>` argument and proxy-free transport in the CLI fence. Reject endpoint with explicit Chrome or `-o`, repeated/missing flags and extra arguments before sending or replacing output. Bounded diagnostics never echo endpoint query, rejected input, response body or raw `url.Error`. **Gates:** `go test ./internal/cli ./internal/tracefile`; help/export from built bundle. **Fence:** U11b only. **Commit:** `feat(cli): send traces to an explicit OTLP endpoint`. **After:** U11a. Collector sends are user-invoked only; no launch opens a connection.

### U12 — docs that ship with behavior

Update [`docs/reference/perf-logging.md`](../reference/perf-logging.md) only when production wiring lands: retain legacy flat log/table claims and P1–P5; explain the independent trace sink and its exact gate. Update CLI/config references with list/show/export and POST only as those behaviors land. User guidance names the machine-wide root, explicit export, possible cross-workspace view, and no runtime/backend guarantees beyond evidence. Do not edit the roadmap, `AGENTS.md`, changelog, or this design from an implementation task. **Gate:** validate every touched Markdown file with the Vantage checker; links and help/source claims must match the landed code. **Commit:** `docs(tracing): document collected startup traces`.

## Ships with and proof still owed

- **Selection and offline read:** existing `internal/cli/trace_test.go` covers list/show, workspace matching, top spans, diagnostics and missing jail guidance; extend it for export selection and failure. It does not witness a production trace source.
- **Ingestion/privacy:** U7 tests cover exact-spelling collisions, identical byte occurrences, source offsets, continuity, calibration, parent freezing, transactional publication, bounded diagnostics and non-destructive refresh. Raw rows, original matching spelling, workspace paths, credentials and rejected payload never reach metadata, attributes, exports or diagnostics.
- **Storage/liveness:** U6 tests cover concurrent processes, hard count/byte limits at physical peak, control/private content, safe lock/directory identity, uninterrupted parent-child handover, no pruned-directory recreation, failures and crash-left recovery. Inability to ask or account declines; it never means “unreferenced.”
- **Runtime integration:** after production wiring, a no-agent integration launch verifies recording off creates no trace; recorded fresh launch joins launcher/keeper/jail sources; attach has its intended trace/source; fork child joins only through explicit handoff; a failed boot remains unfinished. Test the source exposure on Podman and Apple Container, and the native Mac path separately. Do not assert these results until run on those hosts.
- **EV-TR1:** restamp against this tree and reuse U3/U4a rather than scheduling completed surfaces as absent.
- **EV-TR2:** verify fresh/attach/sealed source mapping, private-leaf non-exposure and descriptor-relative producer/reader/cleanup safety on supported backends.
- **EV-TR3:** demonstrate distinct identical occurrences; exact byte-name matching and all collision/overlap cases; unavailable/over-budget evidence; private-evidence exclusion; transactional identity, continuity and calibration; frozen explicit parents; non-destructive monotonic refresh.
- **EV-TR4:** demonstrate concurrent admission, physical-peak accounting, continuous claim handover, safe join/prune, failed/torn writes and crash-left temporary recovery.
- **EV-TR5:** demonstrate file publication and selection; exact-URL request, proxy/credential/redirect/retry exclusion, bounded response/redaction and typed OTLP rejection handling.
- **EV-TR6:** benchmark the sink and record the same launch with recording on/off under authorized runtime containment. **The 10 ms host and 5 ms boot ceilings remain unmeasured.** Nonblocking design and unit tests do not prove either ceiling; never make a timing test's pass condition.

The historical Appendix A runs in the design are not evidence for this implementation's overhead. Completion requires the corresponding code gates, source/backend witnesses and EV-TR evidence; until then call the work partial, not “startup tracing built.”
