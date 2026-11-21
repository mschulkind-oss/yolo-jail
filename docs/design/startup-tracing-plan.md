---
title: "Implementation plan: startup tracing"
status: accepted
stage: DECIDED
next: "Build U6b2 pending publication and public-root resolution; then complete the U6b3 production-caller map before dispatch"
depends-on:
  - startup-tracing.md
tags: [observability, timing, tracing, plan]
summary: "Tree-grounded handoff for source-built store, admitted writer, lexical parser and export foundations; the remaining work is late publication/root resolution, production wiring, and confined source registration and refresh."
---

# Implementation plan: startup tracing

**Status:** 2026-11-21. Completed against `eedb4b30194ea23020b4630e35cde2ddc80c1507` (tree `7e972ee3c447828b414e855353177d00e8808ea3`). The accepted behavior is in [startup tracing](startup-tracing.md). U6b3 remains a source-grounded seed pending the U6b2 API. This is a partial implementation plan, not evidence that production tracing is built or that tests, endpoints, backends, runtime containment, or overhead gates passed.

Precedence: the design wins on behavior; the tree wins on cosmetic fact; this plan is advice and is the first thing to be wrong. Stop and report without committing if a named symbol is gone, a task fence has unrelated work, a predicted red does not reproduce, a needed change crosses its fence, or a gate fails outside scope. Never twist code to match this plan.

**Do not reopen** [OQ-TR1–4](startup-tracing.md#decision-ledger) or the engineering contract in the accepted design. Recording remains off by default; existing user-scope `perf_logging` and invocation gates control it. There is no SDK, viewer, new recording key, mount widening, launch-time network request, or claim that runtime/backend/overhead proof has passed.

## Built foundations — reuse, do not schedule again

| Unit | Present in this tree | Limit of the claim |
| :--- | :--- | :--- |
| U1 | [`internal/perf/context.go`](../../internal/perf/context.go), [`perf.go`](../../internal/perf/perf.go), [`trace.go`](../../internal/perf/trace.go): trace/context identity, traced collector, and independent-root support | `initPerf` still constructs the legacy `perf.New` collector; no production caller installs the admitted trace sink. |
| U2 | [`internal/tracefile/read.go`](../../internal/tracefile/read.go), [`summary.go`](../../internal/tracefile/summary.go): bounded host-format reader, graph and summary | No jail-source filesystem opening, pairing, private persistence, registration, or refresh. |
| U3 | [`chrome.go`](../../internal/tracefile/chrome.go), [`otlp.go`](../../internal/tracefile/otlp.go), [`export_test.go`](../../internal/tracefile/export_test.go), and encoder goldens | Encoders and source tests exist; this is not a test run. |
| U4a | [`internal/cli/trace.go`](../../internal/cli/trace.go), [`trace_test.go`](../../internal/cli/trace_test.go), registry/help wiring | Offline list/show source exists; export and POST are inventoried under U11. No launch collection is wired. |
| U5 | [`internal/jailtrace/jailtrace.go`](../../internal/jailtrace/jailtrace.go), [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go), [`programphaseperf.go`](../../internal/entrypoint/programphaseperf.go), supervisor tests | Id-less Go and shell producers exist. Their current home-root file path is not the accepted dynamic source contract. |
| U6a | [`internal/paths/traces.go`](../../internal/paths/traces.go), [`internal/perf/tracestore.go`](../../internal/perf/tracestore.go), [`tracestore_test.go`](../../internal/perf/tracestore_test.go): pure trace roots, immediate-metadata bounded store, independent claims, inventory, writer admission and prune | Source-built store core. It recognizes public metadata immediately; it does not recognize pending admission or perform late publication. Existing tests were not run in this amendment. |
| U6b1 | [`internal/perf/admittedtrace.go`](../../internal/perf/admittedtrace.go), [`admittedtrace_test.go`](../../internal/perf/admittedtrace_test.go), [`independenttrace_test.go`](../../internal/perf/independenttrace_test.go): checked admitted sink and independent root/sink adapters | Source-built library adapters only. No production caller installs them; they do not implement the pending/late-kind contract. Existing tests were not run here. |
| U7a | [`internal/tracefile/jailsource.go`](../../internal/tracefile/jailsource.go), [`jailsource_test.go`](../../internal/tracefile/jailsource_test.go): bounded pure lexical parser and caller-owned occurrence budget | Lexical parsing only: no filesystem reads, source registration, continuity, pairing, or durable bookkeeping. Existing tests were not run here. |
| U11 | [`internal/cli/trace.go`](../../internal/cli/trace.go), [`internal/cli/trace_test.go`](../../internal/cli/trace_test.go): offline list/show/export and explicit OTLP POST | Command implementation and test declarations exist. No test or endpoint pass is claimed; these commands do not imply production collection. |
| Environment classification | [`internal/cli/run/unknownlaunchenv.go`](../../internal/cli/run/unknownlaunchenv.go) recognizes `YOLO_TRACEPARENT` | Recognition is not propagation; do not make a launcher adopt ambient context. |

The production launch sink remains `newTimingLogWith` in [`internal/cli/run/runcmd.go`](../../internal/cli/run/runcmd.go); `initPerf` wires the legacy file and notice sinks. No scoped production caller under `internal/cli` uses `OpenTraceStore`, `AdmittedTraceSink`, `InstallIndependentTrace`, or `IndependentRoot`. U6a, U6b1, U7a and U11 are existing source foundations; they are not production recording. Pending admission/publication and designated-root resolution are the U6b2 delta; invocation/keeper integration is U6b3; confined source registration and refresh remain U7b.

## Map and fences

Every row is a task-owned fence, not permission to edit an entire package. New files are proposals. Revalidate paths at dispatch.

| Task | Files and seams |
| :--- | :--- |
| U6b2 — pending publication and public root resolution | `internal/perf/tracestore.go`, `tracestore_test.go`, `perf.go`, `trace.go`, `admittedtrace.go`, `independenttrace_test.go`, `admittedtrace_test.go`; `internal/tracefile/line.go`, `read.go`, `tree.go`, `tracefile_test.go`, `export_test.go`. New package-local Linux/Darwin no-replace helpers only as specified in U6b2. No `internal/cli` caller wiring. |
| U6b3 — production invocation and keeper integration (seed only) | Existing caller seams: `internal/cli/run/runcmd.go`, `run.go`, `keeperplan.go`, `keeperspawn.go`, `keeper.go`, [`internal/cli/commands.go`](../../internal/cli/commands.go), and source-named tests. This is not yet a builder map: after U6b2 lands, reread the actual API and complete exact changed hunks, tests and red commands before dispatch. |
| U6c — deliberate child carriers | [`internal/cli/buildpool.go`](../../internal/cli/buildpool.go), [`buildreport.go`](../../internal/cli/buildreport.go), [`forkbuildchild.go`](../../internal/cli/forkbuildchild.go), [`capturehost.go`](../../internal/cli/capturehost.go); build/capture tests. |
| U7b — registration, confinement and transactional refresh | `internal/cli/run/jailperf.go`, `run.go`, `keeper.go`, [`internal/jailtrace/jailtrace.go`](../../internal/jailtrace/jailtrace.go), [`internal/entrypoint/programphaseperf.go`](../../internal/entrypoint/programphaseperf.go), and new U7b-owned registration/refresh/producer files and tests. Consume the already-built `tracefile.ParseJailSource`; do not recreate `jailsource.go`. No mount widening. |
| U8 — remaining host spans | `internal/cli/run/run.go`, `scripts/matched-bundle.py` and its source-named tests; separate service child-span/environment work in `internal/cli/run/loopholesruntime.go` and verified `internal/hostservice` readiness sites. Split shared-file work into serial fences. |
| U9 — macos-user | [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go), `bootperf.go`, Darwin entrypoint/session environment seams and tests; native Mac evidence stays explicit. |
| U10 — preparation acts | Scheduler and background-advance launch seams under `internal/cli`, with channel-driven tests. Each independently owned act gets a trace, not a parent launch. |
| U12 — references | [`docs/reference/perf-logging.md`](../reference/perf-logging.md), user CLI/config references; document source-present offline commands accurately and production collection only when wired. No roadmap, `AGENTS.md`, changelog, or source changes under this documentation task. |

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

U6b2 is the next implementation task. U6b3 follows U6b2 and remains a seed until its exact source map and tests are completed against the landed library API. U6c follows U6b3. U7a is source-built; U7b follows U6b2, U6b3 and the already-built parser. U8 host-gap work follows U6b3; its `run.go` work serializes with U7b, while service work stays on its separate fence. U9 follows U6b3/U7b. U10 follows U6b2/U6b3. U11a/U11b are source-built, not remaining implementation tasks. U12 follows the observable surfaces it documents; production-collection claims wait for U6b3/U7b.

For every task, workers report the exact red and green commands, changed paths, and backend/runtime evidence separately. No test success implies a native backend claim. The implementation is not graduated until the proof obligations at the end of this plan are met.

## Remaining tasks

### U6b2 — pending publication, launcher designation and public root resolution

**Settled — do not reopen:** Use the accepted contract in [early recording and late launch truth](startup-tracing.md#early-recording-and-late-launch-truth), [pending admission and once-only publication](startup-tracing.md#pending-admission-and-once-only-publication), and [root resolution and compatibility](startup-tracing.md#root-resolution-and-compatibility). Existing immediate `TraceStore.Mint` remains compatible for known-kind acts; do not turn an empty kind into pending state or use `Replace("meta.json", ...)`. A pending launch has one actual observed `invocation` root. The launcher’s root start carries only the exact top-level JSON boolean `launcher_root: true`; no header root ID, second event, synthetic root, or later root/name/time/parent rewrite. Only the original pending owner may publish `launch`, `attach`, or its live-finalizer-selected `incomplete` metadata. Public metadata remains exactly seven keys.

**Existing seams and their limits:**
- `internal/perf/tracestore.go`: `TraceStore.Mint`, `inventory`, `Join`/`join`, `prune`; `TraceClaim.validate`, `admit`, `CreateWriter`, `TraceWriter.Write`, `replace`; `safeStoreFile`, `storeCreate`, `storeJSON`, and `storeTreeBytes`. `OpenTraceStore` retains `*os.Root`, validates owner/mode/inode identity and nonblocking admission locking; its `os.Root` does not expose a directory fd.
- `internal/perf/admittedtrace.go`: `AdmittedTraceSink` calls `TraceClaim.CreateWriter`, writes the existing lane header, and uses checked synchronous event appends. `internal/perf/perf.go`: `InstallIndependentTrace` installs before ordinary events; `IndependentRoot(name)` emits its immutable start immediately. `internal/perf/trace.go`: `traceLine`, `eventLine`, and the existing header have no root descriptor.
- `internal/tracefile/line.go`: `rawLine`, `parseLine`, `cleanAttrs`; `read.go`: `readMeta`, `readLane`, `laneReader.line/add`, `mergeAttrs`, `ReadDir`; `tree.go`: `Trace.assemble`. `chrome.go`/`otlp.go` already consume resolved `Span.Name`, IDs and parents.
- The immediate-metadata store and current reader have different compatibility requirements: keep immediate Mint and ordinary/undesignated reader behavior; apply stricter public-metadata validation only when deciding designated-root authority.

**File map and prospective API shape** (names/signatures are mechanical proposals, not new behavior):

1. `internal/perf/tracestore.go` — extend private `storeEntry`, `TraceClaim` and `inventory`; add a private versioned `pendingRegistration` binding trace ID, immutable positive original start, reserved launcher-lane authority, fixed metadata-slot size, and the slot/seal inode identities. Proposed fields: `Version uint8`, `Trace string`, `Start int64`, `LauncherLane bool`, `MetaSlotBytes int64`, `MetaSlot [2]uint64`, `SealSlot [2]uint64`. Keep `.identity` bound to its current root/private/admission/directory/lock identities.
   
   Proposed entry points:

   ```go
   func (s *TraceStore) MintPending(id string, originalStart int64) (*TraceClaim, error)
   func (c *TraceClaim) PublishPending(meta TraceMetadata) (TracePublicationResult, error)
   func (c *TraceClaim) CreateLauncherWriter() (*TraceWriter, error)
   ```

   `MintPending` returns the original publisher claim, not a joined claim. `TraceStore.Join`/`join` returns a nonpublisher claim. The publisher capability is private claim state; no caller-supplied lane string grants it. Generic `CreateWriter` and existing `AdmittedTraceSink` remain unchanged for known-kind/non-reserved lanes. For a pending trace they refuse the reserved `launcher` lane; only the original claim’s `CreateLauncherWriter` can allocate it. An internal writer authority bit lets the specialized sink emit the designation; a joined writer cannot.

2. `internal/perf/tracestore.go` — initialize and account controls before releasing the existing mint/admission protection. Create the existing `.identity` and diagnostic reserve plus a physically allocated 452-byte `.meta-slot`, a physically allocated 33-byte `.publication-seal`, and bounded versioned `.pending` (use existing `identityLimit` = 512 bytes as its strict maximum). Initialize the metadata slot to 452 JSON-whitespace bytes; initialize the seal to 33 zero bytes (unsealed). Once slot/seal identities are known, write `.pending` binding them to the registered trace/start. These private bookkeeping controls remain inside the existing admitted host trace directory, beside `.identity` and `.diagnostic`; they are not guest paths, source-registry inputs or export inputs. Keep them in this directory for descriptor-relative same-directory rename: no new mount/root, source-root relocation or cross-filesystem copy. Preflight trace-count and all maximum content bytes before effects; the admission lock and initial shared claim cover creation through return. The files physically consume their reservations; lane writers cannot use dot-control names or infer reserved credit. No unaccounted temp or future-credit subtraction.

   Extend `inventory` to recognize only:

   - Existing valid immediate entries with `.identity`, lock and `meta.json`.
   - Recognized pending entries with valid `.identity`, lock, `.pending`, a 452-byte slot and a 33-byte unsealed seal, with exact file identities.
   - Pending-published entries whose renamed `meta.json` is the registered slot inode and whose sealed digest validates the exact 452 bytes and metadata.

   The publication retains `.pending` and seal. Count all actual public/private/control/temp/crash-left bytes through existing physical-tree accounting; if `.meta-slot` and `meta.json` coexist, count both and mark publication unavailable. Unknown fields/version, wrong modes/owners/link counts, malformed/torn controls, inconsistent identities, invalid seal, extra conflicting slot state or unaccountable bytes decline admission and remain charged; never classify them as inactive or pruneable. Old valid immediate-Mint traces require no pending controls.

   Pending metadata validation must require the exact seven public keys, matching trace ID and immutable registered positive start, allowed launch kind, and the existing bounded string alphabet/length. Do not globally tighten `Mint` or alter its current seven-key behavior. The pending slot reserves 452 bytes at physical peak, not a writable allowance: writers’ current `admit`/inventory checks see and retain the actual slot bytes.

3. `internal/perf/tracestore.go` and new platform files `internal/perf/rename_noreplace_linux.go`, `rename_noreplace_darwin.go`, and a non-Linux/non-Darwin decline stub — `PublishPending` takes the store admission lock nonblocking, the original claim’s nonblocking mutex, and revalidates claim, registration, private ownership/mode, slot/seal identities and exact sizes through `safeStoreFile`. Write the complete padded JSON to the existing slot in place, without truncate/extend/recreate; check the full write and close. Write state byte `1` plus SHA-256 of those exact 452 bytes to the existing seal in place; check the full write and close. Then expose that same inode as `meta.json` with no overwrite. Proposed helper signatures:

   ```go
   func openClaimDirFD(c *TraceClaim) (*os.File, error)
   func renameNoReplaceAt(dirfd int, old, new string) error
   ```

   `openClaimDirFD` obtains the descriptor from the retained claim root (`c.dir.Open(".")`), checks `Stat`, owner/mode and `os.SameFile` against `c.dirInfo`, and leaves that opened file owned by the caller until the syscall completes; do not reopen `c.dir.Name()` or walk an absolute path for the rename. Revalidate the claim while the store admission lock is held. Linux calls vendored `unix.Renameat2(dirfd, old, dirfd, new, unix.RENAME_NOREPLACE)`; Darwin calls vendored `unix.RenameatxNp(dirfd, old, dirfd, new, unix.RENAME_EXCL)`. The other-target stub returns unsupported. Any syscall error—including `EXDEV`—declines publication; never copy, overwrite, retry, or fall back to check-then-rename. Close the directory descriptor and check the result. If the rename may already have happened, reconcile only the exact registered inode, seal and bytes; otherwise preserve the actual objects and mark unavailable. Never infer success or rewrite after uncertainty.

   `TracePublicationResult` and `TracePublicationState` are proposed internal/public mechanical types with distinct published, already-published, unavailable and conflict outcomes. `PublishPending` makes at most one physical attempt per original claim. Identical repeated requests after success return already-published without rewriting; after a failed attempt return its recorded unavailable outcome without retry; a conflicting snapshot is refused and freezes publication. A joined claim has no publication method authority. U6b3’s invocation owner coalesces identical branch/provenance reports before the single store call and freezes conflicting classifications; do not move branch classification into the store.

   Preserve current prune safety: inventory supplies pending original start as its ordering start; tie-break stays the current lexicographically smaller ID first. Prune only recognized valid entries after an independently opened exclusive lock claim proves inactivity, and hold it continuously through validated private/root removal. Malformed or partially removed state remains charged and declines; no PID, age, absent-meta or guessed-death cleanup.

4. `internal/perf/perf.go`, `trace.go`, and `admittedtrace.go` — preserve `InstallIndependentTrace` and `IndependentRoot(name)` semantics. Add an internal event designation bit and optional top-level wire field `launcher_root,omitempty`, plus:

   ```go
   func (l *Log) IndependentLauncherRoot() *Span
   func AdmittedLauncherTraceSink(
       claim *TraceClaim, tr Trace, warn func(string),
   ) (Sink, io.Closer, error)
   ```

   `IndependentLauncherRoot` makes the existing real trace-only root start named `invocation`, with the current generated nonzero span ID, zero parent and actual clock time; it emits one designated start through installed independent sinks, not a fabricated header descriptor or a second event. Existing `IndependentRoot(name)` continues to emit an undesignated start. `AdmittedLauncherTraceSink` uses the original claim’s reserved writer, actual `launcher` lane/PID and existing checked writer. The admitted state accepts the designation only once on that lane’s actual first root start, with the expected neutral name and zero parent; generic/joined writers cannot designate. Do not change the existing lane header fields, writer’s 10,000-event cap, truncation semantics, or legacy sinks/report rows.

5. `internal/tracefile/line.go` — extend `rawLine` and `parseLine` to distinguish absent `launcher_root` from exact JSON `true`, using raw JSON or an equivalent presence/type representation. `false`, `null`, string, number, array and object are invalid designation values, but must not invalidate an otherwise valid base event: keep the event neutral and add a bounded diagnostic. Invalid base events still follow ordinary line rejection. A `true` on a non-start, end/orphan, or non-launcher lane supplies no authority; preserve an otherwise valid event and diagnose it. Do not turn the marker into an attribute. Keep `MaxAttrs = 8`, `MaxAttrValue = 240`, file/line bounds and existing line-kind parsing.

   The existing producers reach attribute enforcement at `perf.boundAttrs`/`Log.SpanWith`; the reader reaches it at `line.go:cleanAttrs` and `read.go:mergeAttrs`. Preserve eight attributes **per span across all its records**, not eight per record: a start/end merge remains capped at eight. The root marker consumes none of this allowance.

6. `internal/tracefile/read.go` and `tree.go` — keep `ReadDir`, `ReadRoot`, the existing `readMeta` behavior for undesignated legacy traces, graph placement, and public APIs. Compute an additional strict-root-validity bit from the already-read public metadata bytes: require exactly seven keys, valid field types/bounds, matching trace ID, positive `start`, and designated kind `launch`, `attach`, or `incomplete`. Do not require private controls, read a private store, lock, create/adopt files, or make missing private metadata a reader error. Keep old metadata’s established behavior when no valid designation exists.

   Add a package-local resolver at the existing `Trace.assemble` boundary (proposed `func (t *Trace) resolveLauncherRoot()`) after lanes/spans are available. Resolve only one actual observed `launcher_root:true` start in a `launcher` lane whose header trace matches public metadata and trace ID, with nonzero span ID, zero parent, matching in-memory metadata validity and no cross-lane ID ambiguity or duplicate designated/start IDs. A conflicting/unknown kind, missing metadata, invalid metadata, malformed designation, wrong lane, duplicate/ambiguous ID, or missing observed start means no canonical rename and a bounded diagnostic; preserve usable neutral evidence. Never infer a root from longest/parentless spans, start/end coincidence, or metadata alone. Do **not** compare metadata `start` to the event time: public `start` is positive validated original-start evidence, not a retiming input.

   On valid designation only, set the reader’s display name for that observed span from the public kind (`incomplete` remains displayed as `invocation`). Keep the file’s start/end names, IDs, timestamps and parents unchanged; do not add a span. `WriteChrome` and `WriteOTLP` continue to consume the resolved `Trace`/`Span.Name`, preserving IDs/times/parent relationships. Tests must read a copied trace containing only public `meta.json` and lane files and export it without private controls. No change to `internal/cli/trace.go` is in this task. Defer optional host `list`/`show` inspection of private admission status to a separately mapped task; it must not affect canonical reads, public selection or export.

**Existing tests to extend:** `TestTraceStoreFreshMint`, `TestTraceStoreCountAndStartIDTie`, `TestTraceStoreLiveAndUnknownLiveness`, `TestTraceStorePerTraceExactBoundary`, `TestTraceStoreGlobalExactBoundaryAndUnknownPrivate`, `TestTraceStorePrivateControlCrashLeftAndOverBudget`, `TestTraceStoreReplacementPeakAndFailedPublication`, `TestTraceStoreLateJoinAfterPrune`, `TestTraceStoreContinuousChildHandover`; `TestIndependentTraceRootAndOrdinarySpansUseSeparateSinks`, `TestIndependentTraceInstallationDeclinesInvalidOrLateIdentity`, `TestAdmittedTraceSinkWritesEncodedEventsThroughItsClaim`; `TestMetaIsReadAndOneForAnotherTraceIgnored`, `TestParentPlacement`, `TestAStartWithNoEndIsReportedAndEndsWithTheTrace`, `TestASpanIDUsedInTwoFilesResolvesToTheFirst`, `TestExportWireFormats`, and `TestExportResolvedGraphAndEvents`.

**Proposed new tests (not existing declarations):**
- `TestTraceStorePendingAdmissionAndPhysicalReservation` — call actual `OpenTraceStore`/`MintPending`; verify count, `.pending`, slot/seal physical lengths and identity bindings; assert joined claims cannot publish/create launcher lane, writer admission cannot consume the slot, exact caps and existing `Mint` compatibility.
- `TestTraceStorePendingPublicationOnceAndConflict` — call actual `PublishPending`; valid launch/attach/incomplete; identical result/no rewrite; conflicting result/frozen state; one attempt after checked write/close/rename failure; no overwrite on an existing `meta.json`.
- `TestTraceStorePendingCrashInventoryAndPrune` — real store reopen/inventory; valid pending original-start ordering and ID tie-break, independent claims through prune, malformed/partial/torn/unknown controls retained and admission declined, no PID/age cleanup and no directory recreation.
- `TestAdmittedLauncherTraceSinkWritesDesignatedRoot` — real store → `MintPending` → `AdmittedLauncherTraceSink` → `Log.InstallIndependentTrace` → `IndependentLauncherRoot` → real closed JSONL file; assert the single header has no descriptor and the first event is the neutral observed start with top-level boolean true.
- `TestIndependentLauncherRootPreservesOrdinaryRootSemantics` — real collector tests for exactly one designated root, unchanged legacy event/report/sink behavior, joined root refusal and ordinary `IndependentRoot` without marker.
- `TestLauncherRootTypesAndAuthority` — actual `ReadDir` fixtures for absent/true/false/null/string/number/array/object, non-start/end/orphan, wrong lane, duplicate/cross-lane IDs, malformed base event and truncated/missing start; invalid designation preserves a valid base event neutrally.
- `TestCanonicalLauncherRootUsesPublicMetadataOnly` — public-only copied files; exact seven keys, positive start, trace/header agreement and allowed kind produce one display-name resolution; invalid/unknown kind or missing metadata yields neutral read. Confirm no requirement that metadata time equal event time and no private control input.
- `TestCanonicalRootNameReachesExistingEncoders` — `ReadDir` then existing `WriteChrome` and `WriteOTLP`; assert observed IDs, time and parents unchanged and no synthetic span/private control data.
- `TestAttributesRemainEightPerSpanAcrossRecords` — actual producer `SpanWith` and actual reader merge with distinct start/end attributes; assert eight per span and no allowance increase from the root marker.

The success-path tests must call the production library entry points and inspect emitted files, not substitute fake sinks/writers or test only private callees. Failure injection may exercise an existing narrow syscall boundary, but must still assert the store’s observable state and never test a pretend publication implementation. Use event/channel/process-driven concurrency fixtures, not sleeps or elapsed-time pass conditions.

**Proposed red selectors** (add the tests first; these names are proposed):
`go test ./internal/perf -run '^TestTraceStorePending' -count=1`;
`go test ./internal/perf -run '^(TestAdmittedLauncherTraceSinkWritesDesignatedRoot|TestIndependentLauncherRootPreservesOrdinaryRootSemantics)$' -count=1`;
`go test ./internal/tracefile -run '^(TestLauncherRootTypesAndAuthority|TestCanonicalLauncherRootUsesPublicMetadataOnly|TestCanonicalRootNameReachesExistingEncoders|TestAttributesRemainEightPerSpanAcrossRecords)$' -count=1`.

**Gates:** `go test ./internal/perf ./internal/paths`; `go test ./internal/tracefile`; `git diff --check`; compile the Linux and Darwin helper variants under their admitted source-validation profile. These are future gates, not run or authorized by this planning pass. No runtime, endpoint, integration, CLI, or backend test belongs in U6b2.

**Fence:** only the listed U6b2 methods/types and named test files. Do not touch `internal/paths` (U6a is built), `internal/cli` production callers (U6b3), `internal/tracefile/jailsource.go` (U7a is built), U7b registration/refresh, CLI private-status inspection, settings, mounts, environment policy, export transport, legacy logs, or no-overhead claims.

**Commit:** `feat(perf): publish late-kind traces from reserved admission`.

**Report:** red/green results; exact slot/seal/control physical accounting; each failed publication phase and inventory result; proof a public-only copy reads/exports without controls; Linux/Darwin compile status. Do not claim production collection or backend/runtime behavior.

### U6b3 — production invocation and keeper integration (seed only; not a builder hand-off)

**Status:** Wait for U6b2 to land. Then reread its actual API and these source callers before completing the map: `(*Options).initPerf`/`PerfRef` in `internal/cli/run/runcmd.go`; `Run` and actual attach-restart/fresh/attached branch boundaries in `run.go`; the actual waiting spans and keeper plan in `keeperspawn.go`/`keeperplan.go`; keeper initialization, final argv and all exits in `keeper.go`; post-`Run` `process.title_restore` in [`internal/cli/commands.go`](../../internal/cli/commands.go). Current S/C/V/R keeper frames are not a trace-claim acknowledgement.

The accepted design fixes OFF behavior, late branch truth, actual-provenance failure, invocation-scope ownership through frontend title restoration, keeper context capture only after the real waiting span starts, final executed argv insertion after service-env insertion, explicit empty attach carrier when tracing is off/unavailable, and once-only close ordering. No early storage lookup/rescue, ambient context adoption, fallback trace, fabricated event, or status-frame-as-ack.

**Not ready to build:** after U6b2 lands, write the complete changed-file map, exact production test names/callsite deletion witnesses, red selectors, ownership teardown map and gates. U6b3 does not implement or speculate about new library signatures. Runtime containment remains a separate hold.

**Commit:** none for this seed.

### U6c — deliberate build and capture joins

**Settled:** In-process sub-launches reuse the collector/sink and explicit waiting-span parent. The separate fork-build child receives the canonical context only through the hidden self-exec argument. Ordinary/nested launchers never adopt ambient context.

**Tests:** two concurrent build keys have distinct explicit child spans, correct key attributes and common parent; fork-build re-exec receives only the explicit validated context; absent/invalid hidden context follows ordinary gates and does not echo input; in-process capture opens no duplicate sink. Red: `go test ./internal/cli -run 'Test.*Build.*Trace|Test.*Capture.*Trace' -count=1`.

**Change/fence:** `buildpool.go`, `buildreport.go`, `forkbuildchild.go`, `capturehost.go` handover hunks only. Use the existing build phase spans. No inherited environment carrier. **Gates:** `go test ./internal/cli`; targeted integration that reaches a real fork-build child. **Commit:** `feat(cli): join build and capture traces`. **After:** U6b3.


### U7b — confined source registration and transactional refresh

**Settled:** Host-private registration stores each source instance's validated workspace/setup/backend and confined source-relative name outside the log root and outside service-retirement state. Limit 16 instances and 4,096 registry bytes per trace at peak. No locator appears in metadata, exported attributes or diagnostics. Fresh/attach source path is `~/.local/state/yolo-jail-traces/.yolo-trace-<traceid>.jsonl`, through the existing writable workspace-backed `.local` surface; no mount widening or per-file bind. Apply the path change consistently to Go and generated shell producers.

**Change:** Implement descriptor-relative no-follow opening, type/ownership/continuity validation, bounded reads, host-minted source-instance keys, private occurrence/pairing/calibration/parent bookkeeping, and serialized atomic snapshot+bookkeeping publication. Pair only byte-offset order within `(source instance,pid,exact eligible original spelling)`, and only when exactly one open start is supported by continuous evidence. Overlap remains unfinished; orphan end is diagnostic. Freeze a selected explicit host-wait parent. Calibrate before window filtering; persist the decision. A replaced, shortened or conflicting old range is stale. Refresh only adds occurrences/completes a supported unfinished span; preserve prior associations and completed intervals. Unsafe/missing source keeps the last good snapshot and reports a bounded category/count.

**Tests:** lexical parser integration with two identical rows at different offsets; ambiguous overlapping exact-name starts; normalized-name collisions; source replacement/shortening and continuity gaps; failure between snapshot and state publication yields old or new complete state only; frozen parent and calibration survive reread; monotonic refresh preserves completed spans; symlink in every path component, nonregular target, moved anchor, cleanup replacement race; no raw spelling/payload/path leaks. Red: `go test ./internal/cli/run ./internal/tracefile -run 'Test.*Jail.*(Source|Refresh|Trace)' -count=1`.

**Backend proof:** establish exact fresh/attach source mapping and private-leaf non-exposure on Podman, Apple Container, macos-user, and sealed capture/build variants. Where mapping or confinement cannot be proven, decline registration and retain a missing-source diagnostic; do not call the feature backend-complete. **Fence:** U7b; coordinate the U6b2 pending-store API, U6b3 production context, and the existing U7a lexical parser. **Gates:** `go test ./internal/cli/run ./internal/tracefile`; targeted integration; actual native Mac check when available. **Commit:** `feat(run): safely ingest jail trace sources`. **After:** U6b2/U6b3; U7a is source-built.

### U8 — host gaps and service startup

**Settled:** Add only families listed in design [§5.6](startup-tracing.md#56-what-is-added-to-the-record); do not duplicate present spans (`launch.refresh_configured_packs`, `launch.fork_builds`, `preparation.check/build`, `launch.auto_capture`, `launch.resolve_jail_prefix`, `launch.auto_load_image`, `launch.prepare_ws_state`, `launch.assemble_argv`, `launch.refresh_jail_briefings`, `macos_user.*`, `attach.exec`). `probes.done` remains a mark. Process wrapper and pre-collector intervals use actual available start evidence; unavailable OS-start evidence is reported, not invented.

**Tests/change:** event-driven `Run` seams cover each genuinely dark phase; wrapper source test checks start-time carrier, without invoking a fake timing pass budget. Service spans distinguish a service already running (`reused`, no child lane) from one launched by this invocation; child readiness spans end at the actual readiness event. Service startup reason survives the reconstructed durable environment. Failed startup leaves partial trace evidence without changing retry semantics.

**Red/gates:** source-named run tests; `go test ./internal/cli/run ./internal/hostservice`; wrapper's source-named tests; relevant targeted integration. **Fence:** split host-gap, wrapper, and service files into serial commits where shared files overlap. **Commit:** `feat(run): trace remaining startup phases` (host) and `feat(run): trace host service startup` (service). **After:** U6b3; U7b serializes the shared `run.go` changes.

### U9 — macos-user propagation and source

**Settled:** Use the same explicit context in the session environment file for bootstrap and guest daemons; never treat ordinary environment as a launcher carrier. Read the streaming trace source and existing `boot-perf.log` without changing that legacy log's format. Fresh and attach sessions each produce their own expected source behavior.

**Tests/change:** fake `Deps` command/env assertions run on Linux. Native Mac verifies sandbox-account source location, host-safe descriptor read, fresh/attach `session.exec`, and no private registry access. A missing/unavailable Mac mapping degrades to missing source; no cross-platform success claim from Linux fakes. **Gates:** `go test ./internal/macosuser ./internal/entrypoint`; the recorded native Mac handoff. **Fence:** U9 files only. **Commit:** `feat(macosuser): join native startup traces`. **After:** U6b3/U7b.

### U10 — independent preparation acts

**Settled:** Each scheduler act mints a `preparation` trace when it owns the act; it is not a child of a launch. A lock-skip opens no act. Overlap is shown by time only.

**Tests/change:** injected channel-controlled ordinary/plain/advance runners prove two acts mint distinct trace IDs, `done` closes each root, cancellation is preserved, and lock-skip mints nothing. Summary overlap lists an act without assigning launch parent. **Red/gates:** `go test ./internal/cli ./internal/tracefile -run 'Test.*Preparation.*Trace' -count=1`; then package tests. **Fence:** scheduler/advance files and source-named tests. **Commit:** `feat(cli): record independent preparation traces`. **After:** U6b2/U6b3.

### U12 — references that match existing CLI and future collection

The offline `yolo trace list|show|export` and explicit `--otlp-endpoint` POST implementation already exists in [`internal/cli/trace.go`](../../internal/cli/trace.go), with test declarations in [`internal/cli/trace_test.go`](../../internal/cli/trace_test.go). Update user-facing CLI/performance references to describe the existing offline reader/export surface accurately, including workspace selection, output behavior and the explicit-network boundary. Do not claim production collection until U6b3 lands; document its separate trace sink/gate and unchanged `host-perf.log` only after production wiring lands. Check overlapping behavior claims, not only links, in [`docs/reference/perf-logging.md`](../reference/perf-logging.md), CLI/config references and user guidance. Do not edit the roadmap, `AGENTS.md`, changelog, or startup-tracing design from an implementation task. **Gate:** validate each touched Markdown file with the admitted Vantage checker after the relevant documentation work is authorized; source/help claims must match landed behavior. **Commit:** `docs(tracing): document collected startup traces`.

## Ships with and proof still owed

- **Selection and offline read/export:** `internal/cli/trace_test.go` already declares list/show selection and diagnostics tests, `TestTraceOfflineExportSelectionAndFormats`, `TestTraceOfflineExportArgumentsFailBeforeOutput`, `TestTraceOfflineExportAtomicFiles`, `TestTraceOfflineExportRejectsUnsafeTargets`, `TestTraceOfflineExportPropagatesStdoutWriterError`, and explicit-POST tests including `TestTraceOTLPPostsExactEndpointWithoutAmbientCredentials`, `TestTraceOTLPRedirectIsNotFollowed`, and `TestTraceOTLPTransportPolicyThroughTraceMain`. They are source declarations, not test-pass evidence, and do not witness production trace creation or U6b2 pending publication.
- **Ingestion/privacy:** Existing U7a test declarations cover lexical parser spellings, physical source offsets, occurrence budgets and the privacy projection. U7b still owns persisted continuity/pairing state, calibration/parent freezing, transactional refresh and backend confinement. Raw rows, original matching spelling, workspace paths, credentials and rejected payload must never reach metadata, attributes, exports or diagnostics.
- **Storage/liveness:** Existing U6a test declarations cover concurrent processes, hard count/byte limits at physical peak, control/private content, safe lock/directory identity, uninterrupted parent-child handover, no pruned-directory recreation, failures and crash-left recovery. These are source declarations, not a test-pass claim; U6b2 pending admission/publication remains unimplemented. Inability to ask or account declines; it never means “unreferenced.”
- **Runtime integration:** after production wiring, a no-agent integration launch verifies recording off creates no trace; recorded fresh launch joins launcher/keeper/jail sources; attach has its intended trace/source; fork child joins only through explicit handoff; a failed boot remains unfinished. Test the source exposure on Podman and Apple Container, and the native Mac path separately. Do not assert these results until run on those hosts.
- **EV-TR1:** restamp against this tree and reuse U3/U4a/U6a/U6b1/U7a/U11 source foundations rather than scheduling completed source surfaces as absent. No test or backend pass is implied.
- **EV-TR2:** verify fresh/attach/sealed source mapping, private-leaf non-exposure and descriptor-relative producer/reader/cleanup safety on supported backends.
- **EV-TR3:** demonstrate distinct identical occurrences; exact byte-name matching and all collision/overlap cases; unavailable/over-budget evidence; private-evidence exclusion; transactional identity, continuity and calibration; frozen explicit parents; non-destructive monotonic refresh.
- **EV-TR4:** demonstrate concurrent admission, physical-peak accounting, continuous claim handover, safe join/prune, failed/torn writes and crash-left temporary recovery.
- **EV-TR5:** demonstrate file publication and selection; exact-URL request, proxy/credential/redirect/retry exclusion, bounded response/redaction and typed OTLP rejection handling.
- **EV-TR6:** benchmark the sink and record the same launch with recording on/off under authorized runtime containment. **The 10 ms host and 5 ms boot ceilings remain unmeasured.** Nonblocking design and unit tests do not prove either ceiling; never make a timing test's pass condition.

U7a's parser test declarations are `TestJailSourceParsesGoAndShellProducerRows`, `TestJailSourceRequiresExactRepresentableIntegralTimeAndPID`, `TestJailSourceIgnoresUnknownSensitiveFieldsAndOmitsPrivateMatchNameFromJSON`, `TestJailSourceBoundsTailBytesAndCompletePhysicalRows`, `TestJailSourceSkipsPartialFirstAndIncompleteFinalRows`, `TestJailSourceLifetimeBudgetCountsDistinctSourceOccurrences`, `TestJailSourceDefaultLifetimeBudgetStopsAtTwoThousandDistinctOccurrences`, `TestJailSourceDisplayNamesDoNotCollideWithPrivateMatchingNames`, and `TestJailSourceMatchingEvidenceRequiresProducerAlphabetAndLength`. These assert source parsing behavior only; U7b still owns filesystem registration/confinement, persisted continuity and pairing state, refresh, producer-path changes and backend exposure proof. U11a/U11b are inventoried as source-built in [§3](./startup-tracing.md#3-what-is-dark-today); their test source exists, but no test result is claimed.

The historical Appendix A runs in the design are not evidence for this implementation's overhead. Completion requires the corresponding code gates, source/backend witnesses and EV-TR evidence; until then call the work partial, not “startup tracing built.”
