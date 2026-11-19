---
title: "Plan sketch: startup tracing"
status: draft
stage: SKETCH
next: "The design is accepted: complete this against the tree (map, traps, the failing tests in full) and restamp it"
depends-on:
  - startup-tracing.md#OQ-TR1
  - startup-tracing.md#OQ-TR2
  - startup-tracing.md#OQ-TR3
  - startup-tracing.md#OQ-TR4
tags: [observability, timing, tracing, plan]
summary: "The implementation plan's seed for startup tracing: the wire contract the units share, a proposed partition into file-disjoint units with their tests, and the notes and searches the design work surfaced."
---

# Plan sketch: startup tracing

**Status:** 2026-11-19. Seeded during design, read against `f6f13e87d`. The design was accepted
the same day with all four questions ruled, so no unit waits on a ruling; this file is still a
sketch because the planner has not completed it against the tree.
**Design:** [`startup-tracing.md`](startup-tracing.md).

Precedence: the design wins on behavior, the tree wins on fact, this file is advice and is the
first thing to be wrong. Stop and report, without committing, when a named symbol is gone, a test
does not fail the way its unit predicts, or a fix needs a file outside the unit's list.

The unit partition below was asked for with the design, so it is fuller than a seed usually is. It
is a **proposal for the planner to check against the tree**, with paths read once at the commit
above. The last column says which ruling shapes each unit.

## The contract every unit shares

Fixed by the design ([§5.3](startup-tracing.md#53-writers-files-and-trust)); units build against
this and need not wait for each other.

Host line, one per event, in `<trace dir>/<lane>-<pid>.jsonl`:

```json
{"t":1795111105285000000,"k":"start","name":"launch.start_loopholes","span":"9f2c41d07be3a815","parent":"03b1c7e2a94d5f60"}
{"t":1795111106570000000,"k":"end","name":"launch.start_loopholes","span":"9f2c41d07be3a815","dur":1285000000}
```

- `k` is `start`, `end`, `mark`, `note` or `record`. `record` is an `end` with no `start`.
- `parent` is present only when the caller gave one. `attrs` is an optional flat string map.
- The first line of a file is `{"k":"lane","name":"<lane>","pid":<n>,"trace":"<32 hex>","parent":"<16 hex>"}`.

`meta.json` is one object with exactly these keys: `trace` (32 hex), `kind`, `start` (nanoseconds),
`jail`, `workspace` (the short code), `version`, `commit`.

Two jail formats, and they are different files:

- **The source**, written inside the jail to the jail home's trace file:
  `{"t":<ns>,"k":"start|end|mark","name":"…","pid":<n>}`, with `"res":"s"` when the writer had whole
  seconds only. No ids. Only the host's reader (U7) ever parses it.
- **`jail.jsonl`** in the trace directory, written by U7: host-format lines, with `span` minted and
  `parent` explicit on every span, and optionally a first `lane` line. The reader in
  `internal/tracefile` (U2) reads it exactly as it reads a host file, so U7 must write nothing a
  host line cannot carry.

Context value: `00-<32 hex trace id>-<16 hex span id>-01`.

## Proposed units

Four lanes of work with disjoint files. A, B and C start together from the contract; D starts
when A's sink signature exists.

| Unit | Lane | Files | After | Shaped by |
| :--- | :--- | :--- | :--- | :--- |
| U1 identity and the trace sink | A | `internal/perf/perf.go`, new `internal/perf/trace.go`, new `internal/perf/context.go` | none | nothing |
| U2 reader, parent rule, summary | B | new `internal/tracefile/` | none | nothing |
| U3 exporters | B | new `internal/tracefile/chrome.go`, `otlp.go` | U2 | [OQ-TR1](startup-tracing.md#decision-ledger): both formats, no HTML |
| U4 the `yolo trace` verb | B | new `internal/cli/trace.go`, `internal/cli/dispatch.go`, `internal/cli/help.go` | U2 | [OQ-TR3](startup-tracing.md#decision-ledger): the machine-wide root; [OQ-TR4](startup-tracing.md#decision-ledger): an `--otlp-endpoint` flag on `export`, and no config key |
| U5 jail-side writers | C | `internal/entrypoint/boot.go`, `bootsteps.go`, `programphaseperf.go`, `internal/supervisor/supervisor.go`, the provisioning stage's file | none | nothing |
| U6 host wiring and propagation | A | `internal/paths/paths.go`, `internal/cli/run/runcmd.go`, `keeperplan.go`, `keeperspawn.go`, `keeper.go`, `unknownlaunchenv.go`, the argv assembly file | U1 | [OQ-TR2](startup-tracing.md#decision-ledger): the recording gate is unchanged, off unless user config or a typed flag; [OQ-TR3](startup-tracing.md#decision-ledger): the root, and the retention |
| U7 the widened jail reader | D | `internal/cli/run/jailperf.go`, `internal/perf/foreign.go` | U1 | [OQ-TR3](startup-tracing.md#decision-ledger): writes into the machine-wide trace directory |
| U8 dark host spans | A | `internal/cli/run/run.go`, `loopholesruntime.go`, `buildslot.go`, `internal/hostservice/` | U6 | nothing beyond U6's |
| U9 macos-user | D | `internal/macosuser/bootperf.go`, `orchestrator.go`, `internal/entrypoint/darwin.go` | U5, U7 | as U7 |
| U10 scheduler traces | D | `internal/cli/preparationscheduler.go`, `backgroundadvance.go` | U6 | as U6 |
| U11 the reference and the user guide | any | `docs/reference/perf-logging.md`, `userguide/` | each unit, in its own commit | each unit's |

No unit waits on a ruling. U2's choices are folded into the contract above, so U1 and U7 match
what it reads.

### Tests, by unit

Every wait is on an event: a channel, a closed pipe, a line the writer signals. No sleep and no
elapsed-time bound is a pass condition. No test starts an agent CLI.

- **U1.** A child span's line carries its parent's id. Two spans of one name started concurrently
  get distinct ids, and each `end` matches its own `start`. A context round-trips through its
  string form; malformed strings (wrong length, uppercase, wrong version) yield "no context", never
  a panic. A sink whose directory cannot be created warns once and stays silent. The 10,001st
  event writes one `truncated` mark and nothing after. A nil log and a disabled log write no file.
  A helper process handed a context (re-exec of the test binary, env through `testtmp.ChildEnv`)
  writes its own file whose first line names the parent span. That last one is the test that
  fails if the propagation call site is deleted.
- **U2.** Table tests over literal line sets: enclosure picks the shortest enclosing span; a span
  open across its would-be parent's end goes to the lane root; an explicit parent wins over
  enclosure; an `end` with no `start` is placed by its duration; a `start` with no `end` is
  reported as not ended; unattributed time is duration less the union of children, with
  overlapping children counted once; a file with an unreadable first line is skipped and named;
  unknown fields are ignored. Overlap listing: a second trace directory whose spans fall inside the
  window is listed, one outside is not.
- **U3.** Golden files for one small fixed trace in each format. The OTLP golden is also decoded
  into a struct mirroring the published JSON field names, so a rename fails the test rather than
  only the golden.
- **U4.** The verb against a fixture trace root: `show` with no traces says so and names how to
  record one; `show` in a directory with no trace of its own names the listing; an unknown id is
  refused naming the list command. The dispatch registry's and the help listing's own tests pick up the new verb.
- **U5.** The boot runner over a synthetic step table whose step blocks on a channel: the writer is
  injected and signals on each write, the test receives the `start` line, then releases the step
  and receives the `end`. That is the "a hung boot leaves a line" property. The generated shell's
  phase function, run under `bash` against a temp home: a row is appended only when the trace file
  exists, carries microseconds where `EPOCHREALTIME` is set and `"res":"s"` where it is unset.
- **U6.** The keeper plan round-trips the context. The assembled runtime argv carries
  `YOLO_TRACEPARENT`. A launcher whose own environment holds `YOLO_TRACEPARENT` mints a fresh
  trace (the nested-launch trap). `unknownLaunchEnv` does not warn on the name.
- **U7.** The reader over literal jail files: a link, a non-regular file, more than 2,000 lines, a
  line with a time outside the trace, a name with shell or HTML metacharacters, a 51 ms clock
  difference (offset applied and noted) and a 49 ms one (not applied). A second run over a grown
  source yields a superset.
- **Integration**, one test in `integration/`, joining the suite's existing launch helpers: a
  recorded launch leaves a trace directory holding a launcher file, a keeper file and `jail.jsonl`;
  the keeper's first line names a launcher span as parent; `jail.jsonl` holds a boot step and the
  `session.exec` mark. Nested jails can run it: nothing in it is reachability or rootless-only.

## Notes the design work surfaced

- **Sub-launches run in-process.** A capture jail's launch is `run.Run` called from the capture
  code (`internal/cli/capturehost.go:43` says so), so its context is an `Options` field. Check the
  fork and extension build path the same way before assuming it.
- **`Options` is passed by value** across `launchRunPipeline`; `PerfRef` exists for that reason
  (see `initPerf`, `internal/cli/run/runcmd.go:1068`). A context stored on `Options` after the copy is invisible to
  the front door.
- **The keeper builds its own collector** (`internal/cli/run/keeper.go:399`) from the plan's
  `PerfRecording` (`keeperplan.go:136`). The context belongs beside that field.
- **Two identical keeper headers** were written one after the other in the measured warm launch's
  `host-perf.log`. Find the second constructor before giving each process "its own file": two
  collectors in one process would otherwise both open `<lane>-<pid>.jsonl`.
- **Both processes mark `jail_main.spawned`** (`run.go:2776`, `keeper.go:567`). The summary must
  not count it twice once the lanes are merged.
- **The boot's marks are a slice dumped at the end** (`internal/entrypoint/boot.go:43`, `:809`).
  The printed profile block reads the last block of `yolo-perf.log`, so that dump stays as it is and
  the trace lines are an additional, immediate write.
- **`appendProvisionPerf` writes no header on purpose** (`boot.go:50`). The trace file has no
  blocks, so the constraint does not carry over.
- **macos-user's shell may be bash 3.2**, which has no `EPOCHREALTIME`. That is what `"res":"s"` is
  for. Check which shell the generated launchers run under there before choosing how rows are
  written.
- **Process start time.** Linux has it in `/proc/self/stat`; Darwin needs a `sysctl`. `golang.org/x/sys`
  is already vendored. If neither is cheap, `process.pre_collector` is absent on that platform and
  the summary says so.
- **The development wrapper** is Python (`scripts/matched-bundle.py`). It needs one line setting a
  start-time variable before its exec at `:586`. `scripts/test_launcher_slot.py` and its siblings
  are where that would be tested.
- **Retention uses a lock file.** The repository has flock helpers (`internal/pidlock`,
  `internal/cli/run/flock.go`). A reaper that cannot take or test the lock declines.
- **No new dependency.** If a unit reaches for one, `go mod vendor` must be committed with it or the
  image build breaks while `go test` passes. A new package under `internal/` is inside the `goSrc`
  fileset already; a new `cmd/` binary is not wanted here.
- **`yolo trace` inside a jail.** The owner's own config mounts the host log directory read-only at
  `/ctx/host-yolo-logs`. Traces are machine-wide ([OQ-TR3](startup-tracing.md#decision-ledger)), so a jail with that mount can read every
  workspace's traces. Say so in the user guide's section on that mount.

## Ships with

- `docs/reference/perf-logging.md`: its "What this does not do" says spans have no children,
  attributes or propagation, and that nothing crosses the host and jail boundary. Both become false
  and are rewritten, in the unit that makes them false.
- `yolo config-ref`: the `perf_logging` entry says it also records the trace. Its default and its user-only scope do not change ([OQ-TR2](startup-tracing.md#decision-ledger)).
- `AGENTS.md`, "Where things live": the `--timing` row gains the verb.
- The quiet line `yolo: timings recorded in …` names `yolo trace show`.
- `python3 scripts/completion-census.py`, if a new test reads files outside its package.

## Don't

- Don't add a config key that turns recording on from a workspace, or one that sends traces anywhere.
- Don't write an HTML report or any viewer code.
- Don't import a tracing library. The design's [§7](startup-tracing.md#7-the-viewer-decision) has the measurement.
- Don't change `host-perf.log`'s or `yolo-perf.log`'s line formats; other readers and tests parse
  them.
- Don't convert existing span call sites to explicit parents in these units. Enclosure covers them.
- Don't add a span to a stretch because it looks slow. Add it when the summary shows unattributed
  time there on a real launch.
- Don't make a budget a test. The time ceilings are a benchmark and one recorded comparison.
