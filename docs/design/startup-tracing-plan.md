---
title: "Implementation plan: startup tracing"
status: in-review
stage: DESIGN
next: "Build the ready U3 exporters and U4a offline list/show slice; clarify the blocked contracts before host wiring"
depends-on:
  - startup-tracing.md
tags: [observability, timing, tracing, plan]
summary: "Tree-grounded handoff for the remaining startup-tracing work: ready offline exporter and CLI slices, followed by fenced host/jail tasks whose material contract gaps remain explicit."
---

# Implementation plan: startup tracing

**Status:** 2026-11-19. Completed by the planner against `fe75af307157947ca1dd997c800a27db5e80b7b9`.
The ready slices below are build handoffs; the whole feature is **not** ready for unconditional dispatch.
**Design:** [startup tracing](startup-tracing.md). The four owner rulings are closed; the
remaining contract gaps below are not new owner rulings or a second question ledger.

Precedence: the design wins on behavior; the tree wins on cosmetic fact; this plan is advice and
is the first thing to be wrong. **Constraint:** stop and report without committing if a named
symbol is gone, a target file is dirty with another writer's work, the prescribed red test does
not fail as predicted, a change needs a file outside its fence, or a gate fails outside scope.
Never twist code to match an overtaken plan.

**Do not re-open:** Chrome trace-event JSON for Chrome/Perfetto and OTLP JSON, no SDK or custom
viewer; recording off by default, using user-scope `perf_logging` and the existing invocation
gates; machine-wide `~/.local/share/yolo-jail/logs/traces`; collector POST only from an explicit
export command. [The decision ledger](startup-tracing.md#decision-ledger) distinguishes owner
rulings from implementation decisions. The plan does not upgrade the latter into owner rulings.

## Built foundations — reuse, do not schedule again

| Unit | Landed surface | Evidence read at the stamped tree |
| :--- | :--- | :--- |
| U1 | Identity, explicit children, per-process sink | [context](../../internal/perf/context.go): `MintTrace`, `Context.Join`, `ParseContext`; [collector](../../internal/perf/perf.go): `NewTraced`, `SpanWith`, `Span.Child`, `Span.Context`; [sink](../../internal/perf/trace.go): `TraceSink`, `TraceFileName` |
| U2 | Reader, parent placement, startup summary | [reader](../../internal/tracefile/read.go): `ReadDir`, `ReadRoot`, `Meta`; [summary](../../internal/tracefile/summary.go): `Summarize`, `Options.Others` |
| U5 | Immediate jail-side writes | [jail writer](../../internal/jailtrace/jailtrace.go): `Open`, `FileName`, `Writer`; [supervisor tests](../../internal/supervisor/trace_test.go): real `Run` propagation and per-spawn spans |
| Environment classification | `YOLO_TRACEPARENT` is recognized already | [launch environment](../../internal/cli/run/unknownlaunchenv.go); do not add a second classification |

These foundations are libraries/writers, **not** proof that production launches create a trace.
`initPerf` still calls `newTimingLogWith`, which constructs `perf.New`, not `NewTraced`.
U1 writes no metadata or retention lock. Its exclusive file create warns and becomes silent for
a duplicate sink for the same lane/pid; a second collector is not a second writing lane.

## Shared wire/API contract

**Constraint:** preserve these already-built spellings. The reader is not an OTLP SDK.

```json
{"t":1795111105285000000,"k":"start","name":"launch.start_loopholes","span":"9f2c41d07be3a815","parent":"03b1c7e2a94d5f60"}
{"t":1795111106570000000,"k":"end","name":"launch.start_loopholes","span":"9f2c41d07be3a815","dur":1285000000}
```

- Host file: `<lane>-<pid>.jsonl`, first line `k=lane`, `name`, `pid`, `trace`, optional `parent`.
  Events are `start`, `end`, `mark`, `note`, `record`; `record` has a duration and no start.
  `attrs` is a flat string map. A note's free-form detail stays out of the trace.
- Metadata: exactly `trace`, `kind`, `start` (Unix nanoseconds), `jail`, `workspace` (short code),
  `version`, `commit`. Reuse U2's `Meta` field/tag spelling; no workspace or home path in it.
- Context: `00-<32 lowercase hex trace id>-<16 lowercase hex span id>-01`.
  `Span.Context()` returns `(perf.Context, bool)`, not a string. `Context.Join()` returns `perf.Trace`.
- Jail **source**: `~/.yolo-trace-<traceid>.jsonl`, id-less `{t,k,name,pid}`, optional `res:"s"`.
  U7 alone parses this source. Do not pass it to `tracefile.ReadDir`.
- Sanitized **destination**: `jail.jsonl`, host-format lines with host-minted span ids and explicit
  parents. U2 accepts an optional lane header here only; normal host files require it.
- U2's `Span.End` is already extended to the trace end for an unfinished span; `Ended` remains
  false. Export that interval without pretending it ended. `Parent` is the resolved parent;
  `Lane.ParentSpan` covers a cross-process parent when the individual span has none.

## Map and file fences

Rows are task-owned fences, not permission for a worker to edit the whole map. New paths are
proposed files, not existing APIs. Existing paths below were read; revalidate at dispatch.

| Slice | Files/hunks owned |
| :--- | :--- |
| U3 (new) | `internal/tracefile/chrome.go`, `otlp.go`, `export_test.go`, `testdata/export-*` |
| U4a/U4b (new plus registration) | `internal/cli/trace.go`, `trace_test.go`; trace rows only in [dispatch](../../internal/cli/dispatch.go), [top help](../../internal/cli/help.go), [subhelp](../../internal/cli/subhelp.go), [hint dispatcher census](../../internal/cli/hintcommands_test.go); regenerated reader census |
| U6a (new) | `internal/paths/traces.go`, `traces_test.go`; `internal/perf/tracestore.go`, `tracestore_test.go` — root, metadata, liveness/retention, aggregate cap |
| U6b | Trace initialization/lifecycle hunks in [run options](../../internal/cli/run/runcmd.go), [keeper plan](../../internal/cli/run/keeperplan.go), [keeper spawn](../../internal/cli/run/keeperspawn.go), [keeper](../../internal/cli/run/keeper.go), [assembly](../../internal/cli/run/assemble.go), [run](../../internal/cli/run/run.go); new `internal/cli/run/trace_test.go` |
| U6c | Build-context hunks in [build pool](../../internal/cli/buildpool.go), [build report](../../internal/cli/buildreport.go), [build child](../../internal/cli/forkbuildchild.go), [capture launch](../../internal/cli/capturehost.go); new `internal/cli/buildtrace_test.go` |
| U7a (new) | `internal/tracefile/jailsource.go`, `jailsource_test.go` — bounded id-less source conversion, no filesystem locator |
| U7b | Ingestion hunks in [jail perf](../../internal/cli/run/jailperf.go), [run](../../internal/cli/run/run.go), [keeper](../../internal/cli/run/keeper.go); new `internal/cli/run/jailtrace_test.go`; backend source exposure and CLI refresh fences fixed only after clarification |
| U8a | Missing observation hunks in [run](../../internal/cli/run/run.go); new `internal/cli/run/tracegaps_test.go`; wrapper start-time hunks in [matched bundle](../../scripts/matched-bundle.py) and its tests |
| U8b | Per-service span/env hunks in [loophole runtime](../../internal/cli/run/loopholesruntime.go); service-own startup span at verified readiness sites in [hostservice](../../internal/hostservice/hostservice.go); new source-named tests |
| U9 | Native context/ingestion hunks in [orchestrator](../../internal/macosuser/orchestrator.go), [native boot reader](../../internal/macosuser/bootperf.go), [Darwin entrypoint](../../internal/entrypoint/darwin.go); native env-file fence must be restamped before dispatch |
| U10 | Act-lifecycle hunks in [scheduler](../../internal/cli/preparationscheduler.go), [background advance](../../internal/cli/backgroundadvance.go); new `internal/cli/preparationtrace_test.go` |
| U11 | [perf reference](../reference/perf-logging.md), [CLI user reference](../../userguide/reference/cli-reference.md), [configuration reference](../../userguide/reference/configuration.md); config-ref/help and authority links owned with the units making their claims true |

**Constraint (tier 3, reviewed diff fence):** each worker reports `git diff --name-only` against its
starting commit. Shared-file slices land serially. No worker edits the accepted design, built U1/U2/U5,
`go.mod`, `vendor`, or unrelated launch behavior merely to get a gate green. A missing necessary
file expands a fence only after coordinator approval.

## Reuse and traps worth carrying forward

- **Advice:** mirror `nixRootsMain`/`nixRootsEnv` and its tests for the command seam: process reads
  happen in the wrapper; the test drives parsing, selection, and rendering with explicit writers.
  Do not make a formatter-only test stand in for `traceMain` or the dispatch registry.
- **Constraint:** `Options` crosses the launch pipeline by value. `PerfRef` publishes the real
  collector back to the front door. New trace state must not be lost on that copy.
- **Constraint:** keeper `run` calls `o.initPerf(p.Cname)` itself. Preserve one sink per process;
  the launcher passes context through the plan, the keeper does not mint another launch trace.
- **Constraint:** final container argv is `insertHostServiceEnv` in the keeper. Trace env insertion
  must survive it. Attach builds its own runtime `exec` argv in `Run` and must override the old
  container context; an unrecorded attach must not inherit recording from an earlier session.
- **Constraint:** `startExternalService` builds `env`, then `newCmd`, then reconstructs `cmd.Env`
  when startup-reason fds are present. Add context to the durable `env`, not only `cmd.Env`.
  Manifest env and ambient context must not override an intentionally handed context.
- **Constraint:** `poolItem.check`/`build` already span `preparation.check`/`preparation.build` via
  `phaseSpan` → `buildReport.preparationSpan`; `runBuildSlot` injects `req.Perf`. Add the key as an
  attribute and explicit concurrent parentage there, not a second layer of duplicate spans.
- **Constraint:** launcher and keeper both mark `jail_main.spawned`. `container.create` must use
  the actual spawning process's event, not sum both marks or infer an earlier synthetic start.
- **Constraint:** the old jail reader's `os.Root` containment is not an all-components no-follow
  guarantee. A source link that stays inside the root must also be refused; do not reuse that
  reader unchanged for the stronger new source contract.
- **Advice:** use event-driven fixtures and `testtmp.ChildEnv` for re-execs; canonicalize minted
  fixture directories with `EvalSymlinks` as the nix-roots fixture does. No elapsed-time pass
  condition, sleeps, agent API/interactive tests, or actual preparation/npm mutations.
- **Constraint:** preserve shared Go caches and explicit owner overrides. Use native edits;
  inspect formatter diff read-only, apply natively, prove `just format` is a NOOP before commit.

## Ready tasks

These slices are **DECIDED locally**, not a claim that all mandatory tracing behavior is ready.
The commands below are future implementer red/green commands; the planner confirmed missing
surfaces by reading the tree, and did not create Go tests or claim to have run their red phase.

### U3 — two offline exporters (independent)

**Settled — do not re-open:** “Both encoders are plain `encoding/json`.” Export existing U2 trees
as Chrome trace-event JSON and OTLP JSON, with real trace/span/parent ids, one process lane, and
open spans extended to the trace end. No network, collector client, viewer, or recording wiring.

**Preconditions:** U2 is present; `chrome.go` and `otlp.go` do not exist; no dirty fence files.
**API contract:** `WriteChrome(io.Writer, *Trace) error`, `WriteOTLP(io.Writer, *Trace) error`.
These are proposed encoder signatures, not symbols already built. Advice: keep them streaming
through `encoding/json` so writer failures propagate to the explicit command, not a launch.

**Tests first:** add this whole file at `internal/tracefile/export_test.go`. Red command:
`go test ./internal/tracefile -run '^TestExportWireFormats$' -count=1` must fail to compile on the
two absent encoder symbols, not on the fixture. Times deliberately exceed JavaScript's exact
integer range; OTLP nanoseconds are decimal strings, Chrome times are microseconds.

```go
package tracefile

import (
    "bytes"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
)

func TestExportWireFormats(t *testing.T) {
    const id = "11111111111111111111111111111111"
    dir := filepath.Join(t.TempDir(), id)
    if err := os.Mkdir(dir, 0755); err != nil { t.Fatal(err) }
    lines := `{"k":"lane","name":"launcher","pid":7,"trace":"11111111111111111111111111111111"}
{"t":1795111105000000000,"k":"start","name":"launch","span":"1111111111111111"}
{"t":1795111105001000000,"k":"start","name":"check","span":"2222222222222222","parent":"1111111111111111","attrs":{"key":"pack/tool"}}
{"t":1795111105003000000,"k":"end","name":"check","span":"2222222222222222","dur":2000000}
{"t":1795111105004000000,"k":"end","name":"launch","span":"1111111111111111","dur":4000000}
`
    if err := os.WriteFile(filepath.Join(dir, "launcher-7.jsonl"), []byte(lines), 0644); err != nil { t.Fatal(err) }
    tr, err := ReadDir(dir)
    if err != nil { t.Fatal(err) }
    var chrome, otlp bytes.Buffer
    if err := WriteChrome(&chrome, tr); err != nil { t.Fatal(err) }
    var c struct { TraceEvents []struct {
        Name string `json:"name"`
        Ph string `json:"ph"`
        Ts float64 `json:"ts"`
        Dur float64 `json:"dur"`
        Args map[string]string `json:"args"`
    } `json:"traceEvents"` }
    if err := json.Unmarshal(chrome.Bytes(), &c); err != nil { t.Fatal(err) }
    found := false
    for _, e := range c.TraceEvents {
        if e.Name == "check" && e.Ph == "X" {
            found = true
            if e.Ts != 1795111105001000 || e.Dur != 2000 || e.Args["key"] != "pack/tool" { t.Fatalf("Chrome check: %+v", e) }
        }
    }
    if !found { t.Fatalf("Chrome lacks check interval: %s", &chrome) }
    if err := WriteOTLP(&otlp, tr); err != nil { t.Fatal(err) }
    var o struct { ResourceSpans []struct {
        ScopeSpans []struct { Spans []struct {
            TraceID string `json:"traceId"`
            SpanID string `json:"spanId"`
            ParentSpanID string `json:"parentSpanId"`
            Name string `json:"name"`
            Start string `json:"startTimeUnixNano"`
            End string `json:"endTimeUnixNano"`
        } `json:"spans"` } `json:"scopeSpans"`
    } `json:"resourceSpans"` }
    if err := json.Unmarshal(otlp.Bytes(), &o); err != nil { t.Fatal(err) }
    found = false
    for _, r := range o.ResourceSpans { for _, scope := range r.ScopeSpans { for _, s := range scope.Spans {
        if s.Name != "check" { continue }
        found = true
        if s.TraceID != id || s.SpanID != "2222222222222222" || s.ParentSpanID != "1111111111111111" ||
            s.Start != "1795111105001000000" || s.End != "1795111105003000000" { t.Fatalf("OTLP check: %+v", s) }
    } } }
    if !found { t.Fatalf("OTLP lacks check span: %s", &otlp) }
}
```

**Change:** encode U2's resolved parent graph, not raw file enclosure a second time.
Use [OTLP JSON's published field/number rules](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding).
**Cheap implementation choices:** encoder struct names and file-local factoring; stable lane ids
need only distinguish lanes within this export, not persist a new identity on disk.

**Additional acceptance cases:** golden outputs for this fixed trace in both formats; empty trace;
multiple lanes including `Lane.ParentSpan`; escaped names/attributes; mark/note instants; unfinished
span; writer error. Goldens must be decoded as well as compared, not copied from the encoder
without semantic assertions. No additional recorded observations are introduced by exporting.
**Gates:** `go test ./internal/tracefile`; `go test -short ./internal/cli ./internal/tracefile`;
`just format` NOOP; build dev bundle and run its absolute emitted launcher with a no-agent
`-- true` command from a throwaway workspace; commit then `just done`. That nested smoke checks
the shipped bundle, not exporter semantics; U4b supplies the real export caller witness.
**Fence:** U3 row only; never change U2 parsing or summary to suit export.
**Commit:** `feat(tracefile): export Chrome trace-event and OTLP JSON`.
**Report:** red compile failure, green gates, golden paths, no new dependencies, changed-file list.

### U4a — offline machine-wide list/show (independent of U3)

**Settled — do not re-open:** bare `yolo trace` lists retained traces; `show [id]` defaults to this
workspace's latest launch. Summary includes window, lane shares, top fifteen own-time spans,
gaps over 100 ms, unfinished spans, missing jail half, and overlapping traces. A missing view
inside a jail gives the command to run on the host. This slice reads **already collected** traces;
it does not satisfy the live source refresh clause in [writers and trust](startup-tracing.md#53-writers-files-and-trust).

**Preconditions:** U2 exists; no registered `trace` verb or command file; fence clean.
**Command seam:** `traceMain(args []string, env traceEnv, out, errw io.Writer) int`, with
`traceEnv{root, workspaceCode string; inJail bool}`. Advice: the root is passed explicitly in tests
and computed as `filepath.Join(paths.LogsDir(), "traces")` in `runTrace`, so U4 does not need U6a.
Workspace matching uses `paths.JailShortHash(runtime.FromWorkspace(ws))`, the existing host-launch
record's project-code scheme; U6 metadata's workspace field must use the same scheme, distinct
from its setup-qualified jail name. No path is read from metadata. Missing workspace must name
bare `yolo trace`, not pick another workspace.

**Tests first:** whole new `internal/cli/trace_test.go` below. Red command:
`go test ./internal/cli -run '^TestTraceOffline' -count=1` must fail on absent command symbols.
The proposed seam receives argv including `trace`, matching `nixRootsMain`.

```go
package cli

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestTraceOfflineRegistered(t *testing.T) {
    if _, ok := registry["trace"]; !ok { t.Fatal("trace is absent from dispatch") }
}

func TestTraceOfflineEmpty(t *testing.T) {
    var out, errs bytes.Buffer
    traceMain([]string{"trace", "show"}, traceEnv{root: t.TempDir(), workspaceCode: "abcd1234"}, &out, &errs)
    text := out.String() + errs.String()
    if !strings.Contains(text, "perf_logging") || !strings.Contains(text, "yolo config set") {
        t.Fatalf("empty trace advice lacks recording next step: %q", text)
    }
}

func TestTraceOfflineSelection(t *testing.T) {
    const id = "11111111111111111111111111111111"
    root := t.TempDir()
    dir := filepath.Join(root, id)
    if err := os.Mkdir(dir, 0755); err != nil { t.Fatal(err) }
    meta := `{"trace":"11111111111111111111111111111111","kind":"launch","start":1795111105000000000,"jail":"yolo-ws-abcd1234","workspace":"abcd1234","version":"test","commit":"test"}`
    if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0644); err != nil { t.Fatal(err) }
    lane := `{"k":"lane","name":"launcher","pid":7,"trace":"11111111111111111111111111111111"}
{"t":1795111105000000000,"k":"start","name":"launch","span":"1111111111111111"}
{"t":1795111105004000000,"k":"end","name":"launch","span":"1111111111111111","dur":4000000}
`
    if err := os.WriteFile(filepath.Join(dir, "launcher-7.jsonl"), []byte(lane), 0644); err != nil { t.Fatal(err) }
    env := traceEnv{root: root, workspaceCode: "abcd1234"}
    for _, args := range [][]string{{"trace"}, {"trace", "show"}, {"trace", "show", id}} {
        var out, errs bytes.Buffer
        if rc := traceMain(args, env, &out, &errs); rc != 0 || !strings.Contains(out.String(), id) {
            t.Fatalf("%v: rc=%d out=%q err=%q", args, rc, &out, &errs)
        }
    }
    var out, errs bytes.Buffer
    if rc := traceMain([]string{"trace", "show", "22222222222222222222222222222222"}, env, &out, &errs); rc == 0 ||
        !strings.Contains(errs.String(), "yolo trace") { t.Fatalf("unknown id: rc=%d err=%q", rc, &errs) }
}
```

**Change:** wire all three CLI registries and the hint command's `verbDispatchers` row for the
actual switch variable used by `traceMain`. Reuse `ReadRoot`'s ascending order and `Summarize`
with `Options.Others`; do not recalculate own time or silently discard skipped/anomalous files.
**Additional acceptance cases:** two workspaces and newer unrelated trace; latest launch vs newer
preparation; no matching workspace; absent/unreadable root in host and jail; invalid id/path
traversal; unfinished window; skipped file diagnostics; overlap; value-consuming help flags.
The empty-state recording advice is `yolo config set perf_logging true --user`, never a file edit.
If user config is absent, name `yolo init-user-config` first; `config set --user` requires it.
**Gates:** `go test -short ./internal/cli ./internal/tracefile`;
`python3 scripts/completion-census.py` (hint test reads outside its package; commit changed output);
`just format` NOOP; build dev bundle and run absolute launcher `trace --help`/`trace` from a
throwaway repo-owned workspace, no agent execution; commit then `just done`.
**Fence:** U4 row, excluding export implementation until U4b; U3 is disjoint and may run in parallel.
**Commit:** `feat(cli): list and summarize collected startup traces`.
**Report:** red command, actual registration/help evidence, fixture selection and empty-state
results, regenerated census status, green gates. State that live jail refresh is still blocked.

### U4b — explicit Chrome file export (after U3 and U4a)

**Settled:** the design's example is `yolo trace export -o launch.json`. This slice implements that
explicit file destination, using the same id/default selection as `show`, calling `WriteChrome`.
It does not settle no-destination behavior, an OTLP selector, or collector sending.
**Preconditions/fence:** U4 row clean, landed encoder APIs unchanged; serialize behind U4a.
**Tests first:** extend the U4 fixture test with `export <id> -o <fixture-dir>/launch.json`, decode
`traceEvents`, assert the `launch` complete event and its 4000-microsecond duration. Run
`go test ./internal/cli -run '^TestTraceOfflineExport' -count=1`; red must be unknown export or
missing file, not a runtime/build failure. Add output-open/write errors and escaped strings.
**Change:** export branch and `-o` value flag in subhelp. No implicit network or viewer launch.
**Gates/report:** U4a's gates plus U3 semantic decode; full test body must be restamped from the
landed U4a fixture before dispatch. This is a sequenced handoff, not permission to guess the
blocked CLI details below.
**Commit:** `feat(cli): export a collected trace to an explicit Chrome JSON file`.

## Remaining tasks — bounded but not dispatch-ready

The following briefs transfer verified seams and required acceptance cases. They are **after the
contract clarifications below** where indicated. They must gain the literal red test and a
restamped fence before an implementer starts; a test name here is not a claim of reproduced red.
Every task uses the shared stop conditions, unchanged gates, native-edit/format rule, and returns
red/green output, changed paths, and any tree drift. No live preparation/npm work in test fixtures.

| Slice / dependency | Settled change and exact observation seam | Acceptance to pin before dispatch | Gate / commit |
| :--- | :--- | :--- | :--- |
| U6a — BLOCKED on aggregate cap | `paths.LogsDir()` root; one metadata write using U2 keys; keeper/launcher-held `lock`; prune oldest beyond 50 only after proving lock unheld, decline unknown | metadata contains only seven keys; held/error lock never removed; two simultaneous minters; byte-cap test spanning two processes; trace I/O failure cannot fail launch | `go test ./internal/paths ./internal/perf`; `feat(perf): manage bounded machine-wide trace storage` |
| U6b — after U6a | `initPerf`/`PerfRef`; both keeper-plan builders and keeper's one constructor; assembly plus final keeper insertion; attach exec override; typed flags/report gate unchanged | `TestInitPerfConstructsOnlyWhenEnabled` extended to verify trace creation; plan JSON round-trip; `assembleTimed` carrying exactly one context on silent recording too; nested launcher mints fresh despite ambient context; disabled attach clears old context; helper process writes one keeper lane | `go test ./internal/cli/run`; `feat(run): wire launch and keeper trace context` |
| U6c — BLOCKED on deliberate build-child carrier; after U6b | `runBuildSlot` → `pool.report.perf`; `poolItem.phaseSpan` key attribute and concurrent parent; `runForkBuildChild` → `runCaptureChild`; capture options handover | two concurrent keys have distinct ids/attrs and explicit common parent; re-exec fixture receives deliberate context while ordinary nested launcher ignores env; in-process capture shares sink instead of re-opening same file | `go test ./internal/cli`; `feat(cli): join build and capture work to launch traces` |
| U7a — after U6a's size contract | Parse id-less source using `(pid,name)` start/end pairing; mint host ids and explicit waiting-span parents; align at boot-ready difference greater than 50 ms | literal bad JSON, link/nonregular at filesystem caller, 2001 rows, last MiB partial row, 64-byte `[a-z0-9_.]` names, out-of-window times, 49/51 ms offsets, grown reread preserving prior spans, unmatched start, second parse no lost intervals | `go test ./internal/tracefile`; `feat(tracefile): sanitize and convert the jail trace source` |
| U7b — BLOCKED on source locator/exposure; after U6b/U7a | Ready callback beside `jailPerf.recordBoot`, refused boot path, session end beside `recordProvision`; attach must read its own source too; atomic replacement of host `jail.jsonl`; `container.create` from actual spawn to first boot event | recorded fresh/attach/refused boot; two rereads grow destination; safe rename; absent source yields JailMissing; host lanes remain independent of source; CLI reread during running session gains provisioning/exec | `go test ./internal/cli/run ./internal/tracefile`; `feat(run): ingest the jail half into startup traces` |
| U8a — after U6b, serialize shared run file after U7b | Add measured missing phase-1 and post-probe sites, wrapper/pre-collector intervals, not duplicate existing phase spans | synthetic `Run` seams capture config/storage/setup/staging spans; wrapper test captures start carrier before exec; OS-start unavailable reported, not invented; no timing pass budget | `go test ./internal/cli/run`; wrapper's source-named Python tests; `feat(run): observe the remaining host startup gaps` |
| U8b — after U6b/U6a | Service-by-service span around `startExternalService`; durable child env through startup-reason reconstruction; own service span at real bind/ready site, reused service does not join | helper service signals readiness on a pipe; parent and child files join; reason-fd path retains context; reused attribute and no child lane; failure leaves unfinished/failed startup evidence without changing retry semantics | `go test ./internal/cli/run ./internal/hostservice`; `feat(run): trace each host service startup` |
| U9 — after U7b, native env-file fence restamp | `Deps.Perf`, bootstrap interval in `RunMacosUser`, native session env file and guest daemon env; read streaming source as well as existing `boot-perf.log`; old native log remains unchanged | fake Deps command/env assertions on Linux; bash 3.2 seconds fallback already U5, do not rebuild it; Mac verifies sandbox-account write location, host safe read, fresh/attach `session.exec` in exported trace | `go test ./internal/macosuser ./internal/entrypoint`; real Mac handoff; `feat(macosuser): join native bootstrap and session traces` |
| U10 — after U6a/U6c; disjoint from U8/U9 | Scheduler act goroutine dispatches ordinary/plain/advance and returns via `done`; mint kind `preparation` per act; standalone `withBackgroundKey` starts only after ownership acquired | injected channel-driven ordinary/plain/advance runners; two acts mint different traces in same process; `done` closes roots; cancellation preserved; lock-skip opens no act; overlapping summary lists act but assigns no launch parent | `go test ./internal/cli ./internal/tracefile`; `feat(cli): record independent preparation act traces` |
| U11 — alongside each observable slice, serialize docs | Correct existing reference's no-children/no-propagation claims when callers land, and add user CLI/root/privacy/export instructions only for built surfaces | source/help checks, Vantage touched files, no SDK/viewer claim, user-only config and explicit POST remain distinct from report flags | `uvx vantage-check <touched-paths>`; `docs(tracing): document collected startup traces` |

**Existing spans, not missing sites:** `launch.refresh_configured_packs`, `launch.fork_builds`,
`preparation.check`, `preparation.build`, `launch.auto_capture`, `launch.resolve_jail_prefix`,
`launch.auto_load_image`, `launch.prepare_ws_state`, `launch.assemble_argv`,
`launch.refresh_jail_briefings`, `macos_user.*`, and `attach.exec` are present. U8 does not add them
again. The phase-1 config read has no surrounding span in `Run`; `probes.done` remains a mark.
Beyond the design's measured families, new instrumentation requires a real summary showing a gap.

## Contract clarifications blocking complete dispatch

These are linked mismatches, not filed new OQs. The accepted design stays authoritative and unchanged.
Do not implement a proposed resolution until the design authority records the clarification.

1. **Build handover drift:** [propagation](startup-tracing.md#52-propagation) says build launches
   run in-process and the only launch-to-launch carrier is options. The tree now uses
   `runBuildSlot` → build pool → `runForkBuildChild` → `runCaptureChild` → hidden
   `internal fork-build-jail`; absent explicit environment, `runCaptureChild` inherits it.
   Smallest clarification: specify the deliberate carrier into that hidden child's launch options.
   Ordinary `yolo` must still never adopt ambient `YOLO_TRACEPARENT`. Blocks U6c, not exporters.
2. **Locator and exposure:** [writers and trust](startup-tracing.md#53-writers-files-and-trust)
   requires CLI refresh while a source exists, but metadata has no workspace/home path or backend.
   `jailPerfRel` only resolves a legacy log from a known `Options` workspace/home. Podman binds
   that legacy file separately; Apple Container binds the whole home. The new dynamic jail-home
   source is not proven exposed by either old-log reader. Smallest clarification: approve a
   host-only source locator/exposure contract without adding paths to trace metadata, plus behavior
   when it no longer resolves. Blocks full U7b/U9/CLI refresh, not offline U4a.
3. **Aggregate storage:** [limits](startup-tracing.md#57-limits-and-failure) promises 200 KB per
   trace and 10 MB retained. U1's 10,000 events per process, attributes and arbitrarily many lanes
   do not enforce that byte limit; U2's reader bounds are not writer storage bounds. Smallest
   clarification: define cross-process admission/accounting and truncation under the trace-wide
   cap, including metadata and jail replacement, and how live traces interact with the total cap.
   Never describe “50 × 200 KB” as enforced merely by pruning 51st trace. Blocks U6a.
4. **Export command surface:** [viewer export](startup-tracing.md#7-the-viewer-decision) specifies
   both encodings and one POST; [the example](startup-tracing.md#9-finding-where-the-time-goes)
   supplies only Chrome `-o`. Smallest clarification: OTLP file selector, default destination
   without `-o`, endpoint-as-exact-URL versus base plus `/v1/traces`, redirects and export failure
   handling. U3 and explicit Chrome U4b are bounded; complete U4 export/POST is not dispatch-ready.

No clarification reopens the four owner rulings. Engineering proposals belong in the recovery
report, not as behavior silently fixed by this plan.

## Parallel waves and landing checks

- **First wave:** U3 and U4a are file-disjoint and independently useful on fixture/existing traces.
  U4b follows both; do not dispatch it with an unstamped red fixture body.
- **After clarification:** U6a unblocks U6b, then U6c/U7a can run on disjoint fences. U7b follows
  them as required. U8a/U7b serialize edits to the shared run file; U8b is separate. U9 and U10
  can run in parallel once their carriers/storage/ingestion exist. Each reference update lands
  with the behavior it describes, not before it.
- **Integration acceptance:** new `integration/startuptrace_test.go` using existing launch helpers,
  no agents: recording off creates no trace; recorded fresh launch has launcher/keeper/jail files;
  keeper header names actual launcher parent; boot and `session.exec` visible before session exit;
  attach gets a different id; killed boot leaves an unfinished step; deliberate build child joins.
  Read source exposure on podman and Apple Container; native Mac verifies its own path.
- **Runtime gates:** code workers build dev bundle and invoke absolute emitted launcher from a
  throwaway workspace. Relevant integration runs are targeted per slice; landing owner runs
  `just check-ci` and full launch-path integration once on the combined tree. Nested verification
  does not prove host-service reachability on rootless/macOS hosts; real host/CI settles that.
- **Performance evidence:** sink benchmark plus same-launch on/off comparison, then three real
  launch classes' summaries on the owner's host. Budgets are evidence, never timed pass conditions.

## Ships with / don't

- [Perf logging reference](../reference/perf-logging.md): vocabulary and “What this does not do”
  still deny children/attributes/propagation. Explain legacy flat host log/table versus new trace
  sink; retain P5's host-only `YOLO_TIMING`/`YOLO_VERBOSE` restriction. Dedicated jail context does
  not forward those opt-ins or enable printed timing tables on silent recording.
- Config-ref's existing `perf_logging` entry must mention trace recording once U6 lands; default
  and user-only validation do not change. CLI help belongs with U4; quiet recording location
  advice in `Run` gains `yolo trace show` only when production tracing works.
- User guide names the machine-wide root, explicit export, and that a read-only host-log mount
  reveals every workspace's trace, not just this one. Do not assume a conventional mount path.
- Authority row in [AGENTS.md](../../AGENTS.md) may gain the trace verb with U11 approval; no
  changelog entry during landing. Review [roadmap sequencing](../plans/roadmap.md) at handoff:
  its plan-sketch prose is stale, but editing the roadmap is outside this planner's sole-file fence.
- No SDK, HTML, custom viewer, new config recording/endpoint keys, new binary, ambient network,
  old log-format edits, speculative spans, baseline speed fixes, or untrusted jail-minted ids.
- Do not call partial libraries “startup tracing built”. Graduation waits for full propagation,
  safe ingestion, hard caps, CLI contract, backend verification, and recorded baseline evidence.
