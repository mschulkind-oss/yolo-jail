---
title: "One trace per launch: where startup time goes, and how to see it"
status: accepted
stage: DECIDED
next: "Complete the U6b3 production-caller map against the landed U6b2 API; reuse source-built foundations and retain runtime and overhead proof obligations"
tags: [observability, timing, tracing, startup, perf]
summary: "Turns the existing timing spans into one trace per launch that follows the launch across every process it starts, fills the stretches no span covers today, and reads it back as a text summary and an exported timeline for an off-the-shelf viewer."
---

# One trace per launch: where startup time goes, and how to see it

**Status:** 2026-11-21. [OQ-TR1–4](#decision-ledger) remain settled; **Needs your ruling: None**.
Source inventory at `0d8fef21db9a22f30e63b3376cc9f2e4af23f288`
(tree `d0e5a8d57daa679105602577722024ebc8654b48`) establishes partial implementation:
identity/recorder, independent trace installation/root, admitted sink, bounded store/live-claim,
host reader/graph/summary, immediate jail-writing, Chrome/OTLP encoder, and offline CLI/export
foundations exist. U6b2 adds private pending admission, original-owner once-only publication,
reserved launcher designation and public-only canonical root resolution; existing encoders reuse
the resolved graph. Production collector initialization remains legacy-only. Production collection
and invocation/keeper lifecycle integration, safe live ingestion/refresh, and backend propagation
remain pending. Library or CLI existence is not production feature completion.

The engineering contracts below remain accepted; only the inventoried source slices are built.
Source evidence is not a test, backend, runtime-containment, endpoint, or overhead pass.
The two measured launches in [Appendix A](#appendix-a-two-measured-launches) remain historical.
The 10 ms host and 5 ms boot budgets are measurement budgets, not launch pass/fail guards, and
remain unmeasured for this integration under [§8](#8-gates-and-the-overhead-budget).

> **In short.** yolo already times most of a launch, but as flat lines in six files with no way to
> say which process, which launch or which parent a line belongs to. Giving every line a trace
> identity, and carrying that identity into each process a launch starts, is the whole design: the
> summary, the timeline and the list of dark stretches all fall out of it.

**Why it matters.** A warm launch with nothing to build took about 12 s to reach the agent on
2026-11-19, and one that rebuilt a single fork took about 69 s. The existing record names where
roughly half of the warm launch went and cannot say why for any of it.

**The shape.** The existing collector in `internal/perf` gains identity and a second sink, a
per-launch trace directory with one file per writing process. A reader merges the files, and a
`yolo trace` verb prints a summary or exports a timeline.

**Cost.** One new host directory, one variable in the jail's environment, a second small write per
event, and a jail half that writes each step as it happens. No new dependency and no change to the
two existing log formats.

**Start at [§5](#5-the-trace-model)**, the trace model. Why there is no viewer of yolo's own is
[§7](#7-the-viewer-decision).

**Needs your ruling:** None.

**Reads with:** [`startup-tracing-plan.md`](startup-tracing-plan.md) (a tree-grounded partial
implementation plan whose status, completed U3/U4a slices, and remaining handoffs must be
restamped against the candidate; this design amendment does not rewrite its tasks),
[`perf-logging.md`](../reference/perf-logging.md) (the reference for the timing system this
extends; its principles P1 to P5 hold unchanged).

---

## 1. Verdict

- **Keep the recorder, add identity.** `internal/perf` stays the one recorder. It is a leaf package
  that cannot fail a launch and writes each event the moment it happens, and both properties are
  ones the OpenTelemetry SDK does not give ([§7](#7-the-viewer-decision)).
- **Make the record OpenTelemetry-shaped, without the SDK.** Trace ids, span ids and parents use
  the W3C formats, so an export to OTLP is a re-spelling and never a remodel.
- **Read with a text summary first, a timeline second.** The first question, "which five things
  took the time, and how much is in no span at all", is answered in the terminal. The timeline is
  an exported file for a viewer that already exists; yolo ships no viewer.
- **Off unless the user turns it on, and local.** Recording is switched on in the user's own
  config, never by a project's. Traces live in yolo's machine-wide log directory under a hard size
  cap, and one leaves the machine only when a command naming an endpoint is typed.
- **Measure before fixing.** This design names no fix. It builds the instrument, takes a baseline,
  and lets the summary rank what to attack ([§9](#9-finding-where-the-time-goes)).

## 2. What exists today

The timing system in [`perf-logging.md`](../reference/perf-logging.md) is the starting point, and
it is further along than "add performance logging" suggests:

- **A host collector** records named spans and marks and writes each to
  `<workspace>/.yolo/logs/host-perf.log` as it happens. More than a hundred call sites across the
  launch, the keeper and the teardown use it.
- **Two gates.** A persistent opt-in (`perf_logging = true`, `YOLO_TIMING`) records silently; a
  typed `--timing` or `--verbose` also prints the table.
- **A jail half.** The container's entrypoint marks every boot step and appends a block to
  `~/.yolo-perf.log`; the host reads that block back and records each step as
  `container.boot.<step>`.
- **Sub-records.** Fork and extension checks and builds are spanned as `preparation.*`; a
  macos-user launch's backend steps as `macos_user.*`; the host notch into its own machine-wide
  file.

What it lacks is stated in its own reference: spans have "no children, no attributes, and no
propagation". Every gap in [§3](#3-what-is-dark-today) is one of two kinds: a stretch with no span,
or a span whose lines cannot be joined to the launch they belong to.

## 3. What is dark today

Each row was checked in the tree at `f6f13e87d`. "Measured" figures are from
[Appendix A](#appendix-a-two-measured-launches).

### Host, before and inside the launcher

| Stretch | State | Evidence |
| :--- | :--- | :--- |
| A development launcher's wrapper: the Python helper that validates or rebuilds the bundle, then execs `yolo` | **Dark.** No yolo code is running | `scripts/dev-wrapper.sh` hands off to `scripts/matched-bundle.py`, whose `command` execs through `os.execvpe` (`matched-bundle.py:586`) |
| Process start to the collector's construction: defaults, the user-config read, the launch record, three guards, the launch log | **Dark.** The run header has one-second resolution | `Run` opens at `internal/cli/run/run.go:54`; the collector is built at `run.go:156` |
| Phase 1: repo root, storage, pack refresh, config load and validation, setup and runtime resolution | **One mark**, `probes.done` (`run.go:260`), with two spans inside it. Config load (`run.go:211`) has none | Measured: 0.18 s and 0.35 s between the pack refresh ending and the runtime check starting |
| `probes.done` to the workspace lock: pack staging (`run.go:426`), the launch channel (`run.go:512`), the grant, the jail daemons' plan | **Dark.** The next span is the lock at `run.go:1845` | Measured: 0.36 s and 1.27 s |
| Fork and extension checks and builds | **Spanned, but unjoinable.** Seven concurrent `preparation.check` spans share one name; the key is a separate note line | `internal/cli/run/buildslot.go:114`; measured 4.05 s on a launch that built nothing |
| A build jail's own launch (its image, its boot, its build) | **Recorded elsewhere.** Its lines go to `build-jail-<id>.log` with no reference to the launch that waited 46.8 s on it | `internal/cli/buildjaillogs.go` |
| Host services start | **One span for all of them**, `launch.start_loopholes` (`internal/cli/run/keeper.go:497`); the stop has one span per service (`loopholesruntime.go:809`) | Measured: 1.28 s and 3.31 s across eleven services |

### The container

| Stretch | State | Evidence |
| :--- | :--- | :--- |
| The runtime creating the container, before pid 1 runs | **Derivable only**, by subtracting the boot's total from the host's wait | No span between `jail_main.spawned` (`keeper.go:567`) and the first boot step |
| Pid 1's boot steps | **Marked per step, but buffered.** The marks are held in memory and written once at the end, so a boot that hangs leaves no line | `internal/entrypoint/boot.go:43` (mark), `boot.go:809` (the single dump) |
| The first session's own boot pass | **Not on the host timeline.** It is in the jail's file only | [`perf-logging.md`, the jail half](../reference/perf-logging.md#the-jail-half) |
| The provisioning stage: `mise install`, then the bootstrap that installs each program | **One number**, recorded on the host only when the session ends, so a running session's record has no provisioning at all | Measured: 2.3 s and 8.0 s |
| Each program's readiness check and install | **Whole seconds**, from bash's `SECONDS`, and never read back by the host | `internal/entrypoint/programphaseperf.go:13`; `internal/cli/run/jailperf.go:173` skips these rows |
| In-jail daemons | **Dark.** The boot step marks the supervisor's start; no daemon's start or readiness is timed | `internal/supervisor` has no reference to `internal/perf` |
| The agent's exec | **No mark.** Nothing records the moment startup ends | The session command's timers are print-only ([`perf-logging.md`](../reference/perf-logging.md#the-jail-half)) |

### Detached work

| Stretch | State | Evidence |
| :--- | :--- | :--- |
| Host service daemons' own startup, inside each process | **Dark.** Only `internal/cli`, `internal/cli/run`, `internal/image`, `internal/macosuser`, `internal/darwinpkg` and `internal/entrypoint` import `internal/perf` | Import census, 2026-11-19 |
| The preparation scheduler and its background advance | **Dark.** Outcomes are recorded; durations and overlap with a launch are not | `internal/cli/preparationscheduler.go` and `internal/cli/backgroundadvance.go` hold no span |

### The record itself

- **Lines carry no process.** The launcher and the keeper each write a run header into one file,
  and every later line lands under whichever header came last. In the measured launch
  `child.spawned`, a launcher mark (`internal/cli/run/proxy_linux.go:43`), sits in the keeper's
  block, and `jail_main.spawned` appears twice, once from each process (`run.go:2776`,
  `keeper.go:567`).
- **Six files, four formats.** `host-perf.log`, the jail's `yolo-perf.log`, macos-user's
  `boot-perf.log`, `host-notch-perf.log`, `build-jail-<id>.log` and the tree build logs.
- **Nothing reads them back.** The only rendering is the `--timing` table, printed by the launch
  that asked for it, at its exit.

## 4. Vocabulary

- **Trace**: every span one launch caused, in whichever process it ran. The field's usual meaning,
  as in [OpenTelemetry](https://opentelemetry.io/docs/concepts/signals/traces/). One launch here
  means one `yolo` invocation that runs the launch pipeline, attaches a session, or runs a host
  notch command. Not a jail's lifetime: a second session attaching later is its own trace.
- **Span**, **mark**, **note**, **record**: as defined in
  [`perf-logging.md`, Vocabulary](../reference/perf-logging.md#vocabulary). A span gains an id, a
  parent and optional attributes here; the other three are unchanged.
- **Lane** *(coined here)*: one writing process within a trace, such as the launcher, the keeper,
  one host service, or the container's pid 1. Not a thread: goroutines of one process share a lane.
- **Jail half**: what processes on the jail side of the boundary record. The term is
  `perf-logging.md`'s. The host treats it as untrusted input.
- **Startup window** *(coined here)*: from the earliest instant the launch is known to have begun
  (a development wrapper's start if there is one, otherwise the launcher process's start) to the
  moment the first session's shell execs the agent. Not the launch's whole life, which includes a
  session of hours.
- **Unattributed time** *(coined here)*: the part of a span's duration that none of its children
  cover. Not idle time: it is work that happened in a stretch nobody named.

## 5. The trace model

### 5.1 Identity

Every trace has a 16-byte id and every span an 8-byte id, both random, written as lowercase hex.
The pair that names "the span I am a child of" is carried in the
[W3C `traceparent`](https://www.w3.org/TR/trace-context/#traceparent-header) form,
`00-<trace id>-<span id>-01`. These are the formats OpenTelemetry uses, so exporting needs no id
translation.

The launcher mints one trace identity when it constructs the invocation's collector. Recording
begins with one immutable, trace-only root named `invocation`; actual `launch` or `attach` truth
is committed later without restarting that root. Other acts retain their settled kind semantics
(`host`, `host_apply`, `stop`). The scheduler mints one trace per act it runs
([§5.5](#55-work-that-is-not-a-child-of-a-launch)).

### Early recording and late launch truth

**Existing mechanism and insufficiency.** `perf.New`, `InstallIndependentTrace`,
`IndependentRoot`, and `AdmittedTraceSink` provide reusable collector, identity, root, and
checked-write foundations. They do not implement this protocol: `Mint` immediately publishes
metadata, inventory requires it, the sink writes its header before the root exists, and root
names cannot change. Installation must precede `probes.done`, the first ordinary production
event; the same collector retains its legacy sinks. An installation refusal disables only new
tracing. The legacy `TraceSink` is not an admitted substitute.

**Chosen representation.** A *pending admission* is a privately registered trace with its
original start and live claims, but no public `meta.json`. A *branch commit* is the existing
decision after which this invocation actually proceeds as fresh launch or attached execution,
not an attempted attach. A *launcher-root designation* is optional top-level
`launcher_root: true` on the launcher's root-start event, identifying the existing span eligible
for canonical display naming. These are accepted engineering protocols, not current APIs.

A header descriptor would require a predetermined root ID or delayed header, neither supported
by the current ordering. Instead, root creation emits the designation in the same checked append
as its immutable `invocation` start. It cannot be added retrospectively after `IndependentRoot`
returns. The header keeps its existing fields; metadata keeps exactly its seven keys. The
designation is not an attribute, a second start, or a new span.

| State | Allowed transition and single authority | Public evidence and selection |
|---|---|---|
| OFF | Frozen existing recording gate is false | No trace-root lookup/creation, admission, carrier adoption, installation, or lifecycle |
| Pending | Invocation owner admits once, installs once, and starts one root before ordinary events | `list` identifies a recognized pending entry; explicit-ID `show` renders available events neutrally |
| Branch committed | Invocation owner records actual kind and already-resolved provenance once | Pending remains until checked publication; kind does not imply success or readiness |
| Published | Sole publisher exposes complete seven-key metadata once | Ordinary metadata-start selection includes the trace; valid designated root receives its canonical display name |
| Uncommitted finalization | Owner publishes kind `incomplete` if no branch committed | Selectable incomplete invocation; available root remains named `invocation` |
| Publication unavailable | Owner freezes publication after a checked failure or conflicting existing record | Pending/unavailable diagnostic, no invented public kind; explicit-ID inspection remains possible |
| Closed or interrupted | Claims are released by their owners, or process termination releases protection | Retained evidence only; no promise of completed root or survival after kill |

**Actual branch truth.** Container attach attempts, including the post-lock race and stale-removal
wait, remain pending when the attacher can return “restarted.” Fresh launch commits only after
all attach alternatives are exhausted, before fresh payload preparation. Attach commits at the
existing attached-outcome boundary after teardown capture and before execution; refresh start
and session acquisition are not commits. Every actual native sandbox invocation is fresh launch;
native keeper sharing does not make it attach or retroactively join an older trace.

Storage/configuration/setup refusal, refresh failure, dry run, sealed sublaunch, or interruption
before either commit is `incomplete`, meaning only “no launch/attach branch committed.” Failure
after commit preserves its actual kind. Earlier timestamps, IDs, names, and parents never change;
there is no remint, second root, or end-name renaming.

**Metadata provenance.** The publisher uses the original collector start, workspace short code
and non-executing baked version/commit stamps. It captures actual setup-qualified jail identity
from the existing resolution when available, without an extra early config lookup or runtime
Git invocation. `runtime.FromWorkspace(workspace)` alone is not that identity. An uncommitted
invocation may publish `jail: "unknown"` when identity was never resolved; it never publishes a
path. A committed invocation lacking the required actual jail identity stays publication-unavailable
with a warning rather than substituting a guessed identity. Joined processes cannot publish.

**Pure decisions and effects.** Classification, legal metadata construction, descriptor validation,
and capacity calculation are pure decisions over captured facts. Admission, checked append,
publication, propagation, and close are effects owned separately. Missing or conflicting facts
produce the defined unavailable state, not an implementer's classification guess.

### Pending admission and once-only publication

The existing store is extended, not bypassed. Its private registration recognizes matching
`.identity` plus a versioned `.pending` control record without public metadata. That immutable
control records the trace ID, original positive Unix-nanosecond start, reserved launcher-lane
authority, and bounded metadata-slot size. Existing valid ID/identity/public-metadata records
remain admitted without this control. An empty public kind or `Replace(meta.json, ...)` is not
pending admission.

Admission charges a trace-count slot and all peak bytes before creating content. The compact
seven-key metadata has a fixed maximum encoded length of 452 bytes: a nonzero 32-character
lowercase-hex trace ID; five other strings, each at most 64 bytes in the existing unescaped
`[a-zA-Z0-9._+-]` alphabet; a positive signed-int64 Unix-nanosecond start of at most 19 digits;
and the seven existing keys. A private `.meta-slot` is physically filled to those 452 bytes;
lane writes cannot consume it. Admission also reserves a fixed 33-byte publication-seal slot.
Before publication effects, matching `.identity`, private modes/ownership, immutable `.pending`
registration, metadata-slot identity and seal-slot identity must validate. The seal's one state
byte distinguishes unsealed from sealed; its other 32 bytes hold a SHA-256 digest, a fixed-length
fingerprint of the exact padded metadata. The owner validates every metadata field and pads
shorter encodings with JSON whitespace.

At publication, the sole invocation owner writes the complete JSON plus padding in place through
its validated slot descriptor, covering all 452 bytes without truncating, extending, recreating,
or adding an unaccounted temporary file. It then writes the sealed-state byte and digest into the
fixed seal slot. It checks every write and both closes before exposure. It exposes only that
registered metadata inode as `meta.json` by atomic no-overwrite rename, changing neither its
charged size nor its content. If atomic no-overwrite support is unavailable, or any checked step
fails, publication becomes trace-only unavailable with the shared warning: no overwriting
fallback, check-then-overwrite, retry, or deletion to manufacture credit.

Retained `.pending`, the seal, identity, locks, diagnostics, padding, and any partial/crash-left
content remain charged. If both `meta.json` and `.meta-slot` are found, both entries' content is
charged conservatively without an assumed alias/copy discount. Any different effect
representation must admit all simultaneous copies before using them; deletion plans earn no
credit.

A publisher performs at most one publication attempt after choosing its final snapshot. At actual
branch commit, the sole original invocation owner immediately makes its once-only publication
attempt from the captured final snapshot when all required metadata provenance is resolved;
publication does not wait for keeper return, invocation return, the last title event, or
`root.End`. A committed branch missing required actual jail identity remains charged and
publication-unavailable, with no later guess, finalizer reclassification, or publication retry.
Only a valid live original owner's uncommitted finalizer selects and publishes `incomplete`;
dead/crashed pending entries are not automatically finalized, and a joined keeper's root or
scope never classifies or publishes the original owner's metadata.

Repeated requests with identical facts before that attempt coalesce; conflicting
branch/provenance facts freeze publication and warn. After publication, identical requests are
no-ops and conflicting requests are rejected without changing metadata. A joined claim has no
publication authority. A pre-rename write/close failure retains the private slot and disables
publication; a rename failure retains whichever objects actually exist. If publication may have
occurred, checked identity/content reconciliation may recognize the exact snapshot, never rewrite
or retry it. Malformed or conflicting public content is retained, diagnosed, and excluded from
selection.

Pending traces count toward retention and use their immutable original start, then trace ID,
for prune ordering. A known valid pending entry becomes prune-eligible only when an exclusive
claim proves no participating live claim remains. An interrupted entry is not automatically
converted to `incomplete`; missing/malformed identity or control is unknown state and is retained
with admission declined. Crash recovery does not infer process death from PID, age, or absent
metadata, and cannot recreate a pruned directory.

### Root resolution and compatibility

Only the original publisher may create the reserved `launcher` lane; its header identifies that
lane, actual process PID, and the trace. Joined keepers and jail writers cannot create it or emit
a launcher designation. Exactly one root start may carry the designation, with a nonzero span ID,
zero parent, matching trace/header, and an actually observed start. No header root ID is inferred.

Before Chrome or OTLP encoding, the reader resolves that existing span's display name from valid
public metadata: committed kinds use their kind name; `incomplete` retains `invocation`. The
stored start/end names, IDs, timestamps, and parents remain unchanged. No synthetic root or
metadata-based renaming of an undesignated span is allowed.

No designation means established name rendering, including old traces and old headers. Old
readers ignore the new event field and honestly show `invocation`; they do not show a guessed
canonical kind. An absent `launcher_root` supplies no designation; only the exact JSON boolean
`true` is a candidate, while a present `false`, `null`, string, number, array, or object is
invalid designation. Invalid designation or non-launcher authority produces a bounded diagnostic
and removes only canonical-root authority from an otherwise valid observed event/span; malformed
base events still follow normal rejection and diagnosis without inventing a span.

Canonical naming of a designated launcher requires matching trace identity between valid public
seven-key metadata and the launcher-lane header, plus the actually observed designated root start
with its valid nonzero span ID and zero parent; metadata kind must be exactly `launch`, `attach`,
or `incomplete`. Public metadata start is validated as positive Unix nanoseconds, never used to
retime the span. Private registration, slot identity and publication seal are not decoder or
export inputs: a valid copy of public metadata and lanes remains canonically readable without
them. This validates recorded format and relationships, not publisher authenticity or store
admission.

A syntactically valid unknown kind or another act's kind such as `host`, `host_apply`, or `stop`
is conflicting authority for that designated launcher and leaves its observed name neutral;
undesignated old traces retain established rendering and metadata-kind behavior. Cross-lane ID
ambiguity, duplicate designation/start IDs, or a missing matching observed root start likewise
denies authority; designation on an end record or orphan end never supplies it. Joined lanes have
no launcher authority, and readers never infer designation from a parentless or longest span or
create a synthetic span.

For entries created by pending admission, store recognition/admission of publication requires the
registered slot identity, a valid sealed digest matching the exact public snapshot, matching
trace and immutable original start, and the declared slot bound; `.pending` remains private.
Plausible external metadata without that evidence is preserved but never adopted by the store as
its publisher's snapshot; private inconsistency declines admission and diagnoses source state.
These predicates govern inventory, claims, writes, crash recovery and prune, not offline metadata
selection, span resolution or export; store diagnosis never rewrites an independently valid
public metadata record or graph, and private controls never enter exported data or attributes.

Zero events or missing metadata yields no inferred kind/root. One valid designated start and
metadata yields one resolved root. Many ordinary zero-parent events remain enclosure-resolved,
not extra roots. Oversize, malformed, duplicate, and conflicting records follow bounded parsing
and partial-evidence diagnostics rather than allocation growth or encoder-specific guesswork.

Default offline metadata-start selection uses valid public metadata start only, excluding missing
or invalid metadata; public traces need no private controls to be selected, read or exported.
Explicit-ID `show` preserves available neutral evidence and diagnoses metadata unavailability
when metadata is absent; absence alone does not establish registered pending state or claim
safety. Host `list`/`show` may additionally report bounded, read-only pending/unavailable
admission status when private evidence is available, separately from observed public trace
evidence. That optional status never determines canonical naming, export identity or graph
construction; generic offline reads without private controls require no admission inspection,
locks, file creation, adoption or recording effects, and do not dump private registry/control
contents.

### Caller ownership, propagation, and finalization

An *invocation scope* is a once-owned lifetime handle separate from copied options and `Log`. The
frontend owns it when it must cover normal `process.title_restore` after `Run`. `Run` borrows an
explicitly supplied scope; a direct or nested call without one owns and defers its own scope. A
nested operation in the same invocation explicitly borrows that scope and collector; a distinct
invocation owns a distinct scope. Ownership is never inferred from copied options or transferred
implicitly. The current `PerfRef` contains only `Log`; its lifetime extension is accepted design,
not an implemented API.

Normal finalization records the final ordinary/title-restoration event, publishes `incomplete`
if still uncommitted, then performs `root.End`, writer close, original claim close, and store close,
in that order. Every constructed resource is registered immediately with this one owner; partial
construction closes only acquired resources. Each non-idempotent writer close executes once.
Other teardown paths request the same once-finalization, not independent copies of its closers.
Keeper outer invocation ownership covers container/native exits and hold/finish/unwind early
returns, rather than only one finish path.

Before existing explicit-exit paths, outer termination hooks perform their existing once-only
terminal restoration and real outcome events, then request this finalization before `os.Exit`.
They do not fabricate normal `process.title_restore` when that path did not run. Uncatchable
termination can bypass all cleanup; no fsync, kill-flush, complete-end, or survival guarantee is
made. Close failure never changes launch status: record bounded partial-accounting diagnostics
when possible, continue the remaining release order, and issue the shared safe warning once.

Keeper plans carry a private context captured from the actual waiting span after it starts and
before plan serialization; constructing the plan earlier does not fix its context early. A keeper
joins with its own independently acquired claim, never by descriptor duplication/inheritance.
An intentional join that is absent, malformed, conflicting, or unavailable disables tracing,
never mints a fallback root. The launcher retains its original claim for its whole invocation;
existing S/C/V/R frames are not claim acknowledgements. If it releases earlier in a future path,
that path needs an explicit acquired-claim acknowledgement first, without an intentional wait.

Context insertion targets the final executed fresh runtime argv and actual image reference after
service-environment insertion, not a stale plan copy. Missing insertion position disables only
propagation with the safe warning. First-session and attach argv receive the authoritative context
before their container argument; OFF or unavailable tracing supplies an explicit empty carrier
to clear stale container context. Ambient launcher `YOLO_TRACEPARENT` and user/manifest overrides
are never parent authority. `YOLO_JAIL_TIMING` remains report-only. No carrier enters public
metadata, attributes, or general jail configuration.

### 5.2 Propagation

A process joins a trace only when its parent hands it the context deliberately:

| Boundary | Carrier |
| :--- | :--- |
| Launcher to keeper | a field in the keeper's plan, beside the recording gate it already carries |
| Launcher or keeper to a host service it starts | an explicitly populated child environment, not ambient launcher context |
| Launcher to an in-process capture or other in-process sub-launch | a field on the launch options carrying the explicit waiting-span context; the sub-launch reuses the collector and sink |
| Launcher to the separate fork-build child | the hidden self-exec argument `--trace-context <context>`, passed deliberately with the existing build arguments and parsed only by that child entrypoint |
| Keeper to the container: pid 1, and each session's exec | `YOLO_TRACEPARENT` on the runtime's command line |
| macos-user launcher to the bootstrap and the guest's daemons | the same variable, in the session's environment file |
| Development wrapper to launcher | the wrapper's start time, in the environment; the launcher enters it as a completed interval |

The fork-build path is a separate writing process, not an in-process capture. The hidden
argument is an engineering addition to its existing argument contract, not a symbol already
built. It carries the waiting span's `perf.Context`; validation uses `perf.ParseContext`, and
joining uses the resulting `Context.Join()`. Deliberate producers emit the canonical
`00-<32 lowercase hex trace>-<16 lowercase hex span>-01` form. The existing parser also accepts
other valid lowercase two-hex flags bytes; that accepted input is not evidence that producers
emit a different canonical form. Trace and span IDs must be nonzero.

Each separate writing process owns one collector and one exclusive lane. An in-process
sub-launch starts its root as an explicit child of the waiting span and never opens another sink
for that lane. A valid child context joins only an admitted live trace under
[§5.7](#57-limits-and-failure); it cannot recreate a missing trace directory. An absent or invalid
hidden context falls back to ordinary recording gates, never fails the build, and never echoes
the invalid value. Failure to join a valid context degrades tracing rather than silently
creating a replacement trace with that ID.

> [!WARNING]
> **An ordinary or nested launcher never adopts a trace context from its own environment.**
> The launch-to-launch handovers are explicit launch options for an in-process sub-launch and
> the validated hidden argument for the fork-build child. Neither entrypoint reads ambient
> `YOLO_TRACEPARENT` to choose its trace. The entrypoint, in-jail daemons, and generated shell
> consume the deliberately supplied jail variable. A host service's explicit child environment
> is a separate service-entrypoint carrier, not permission for a launcher to adopt ambient context.

Scheduler acts remain independent traces, not children selected through inherited context.
A joined build's staging workspace is a source location, not a replacement for the originating
trace's metadata workspace.

`YOLO_TRACEPARENT` shares no prefix with `YOLO_TIMING`, which
[P5](../reference/perf-logging.md#principles) requires of anything that crosses into the jail. It
is an identifier and no secret, so it is left in the session's environment. This amendment adds
no recording gate or configuration key.

### 5.3 Writers, files and trust

A trace is a directory, `<trace root>/<trace id>/`. The trace root remains machine-wide,
`~/.local/share/yolo-jail/logs/traces/`, beside `launches.log`. No jail can write there.
`meta.json` retains exactly seven keys: `trace`, `kind`, `start`, `jail`, `workspace`, `version`,
`commit`. `workspace` is the originating workspace's short code, never its path. A deliberately
joined capture/build source can have another workspace, including a build staging workspace;
registering it never changes that metadata. `host-perf.log` stays in its existing workspace.

> [!WARNING]
> Workspace `.yolo/logs/` is guest-writable. Neither its location nor a valid trace ID makes
> its bytes trusted host trace data. Private locators and bookkeeping must not become metadata,
> exported attributes, or absolute-path diagnostics.

| File or state | Writer / owner | Contents |
| :--- | :--- | :--- |
| `meta.json` | the original invocation owner, once at publication; never a joined writer | exactly the seven keys above; `start` is the immutable original Unix-nanosecond start; recognized pending admission has no public metadata |
| `<lane>-<pid>.jsonl` | exactly one host writing process | a lane header and admitted events, appended synchronously |
| `jail.jsonl` | serialized host refresh | sanitized host-format observations from registered sources |
| Ingestion bookkeeping | serialized host refresh | occurrence-to-span associations, continuity and calibration state, committed transactionally with the sanitized snapshot |
| Private source registry | host registration under store admission | confined host locators and host-minted source-instance keys; never an export input |
| `lock` | every participating host writer or refresh | no content; its live claims prevent pruning |

**Existing foundations and their limits.** U2 reads host-format lines and resolves their graph;
it is not an id-less jail-source converter. The legacy jail reader and current jail writer's
leaf-only no-follow open do not provide the all-component confinement specified below.
The built `AdmittedTraceSink` supplies claim-backed checked append, trace identity checking,
bounded event truncation, and contention refusal. The built bounded store supplies admission
and independent live claims, but currently requires immediate public metadata. Neither supplies
pending admission, late publication, launcher designation, or production lifetime integration.
The legacy `TraceSink` creates directories and ignores write counts/errors and must not be wired
as their fallback. These foundations are reused, not treated as a complete lifecycle.

**Host-private multisource registration.** A source instance is one host-registered lifetime of
one append-only jail source. Its opaque key is minted by the host, never read from jail content.
A trace may register at most 16 source instances, with at most 4,096 content bytes of registry
state per trace, including replacement copies at peak. Registration declines when either limit
or the aggregate store admission limit would be exceeded.

The registry belongs in a dedicated host-private `trace-sources` leaf beneath global storage,
outside the log root and outside service-retirement state. This names a new storage leaf, not an
existing source API. Each entry identifies the trace, its opaque source instance, its own
validated host workspace/setup and backend, and its confined source-relative name. Original,
capture, and staging workspaces therefore coexist rather than overwrite a single locator.
Jail records supply none of those fields.

Registration requires evidence that generated mounts and context links expose neither this
private leaf nor an ancestor. If that cannot be established, decline registration; do not widen
mounts. Registry and ingestion-control content is charged to its trace and the retained-store
budget even when physically outside the trace directory. Orphan or unattributed private content
must be accounted for conservatively before admitting more recording. Registry paths never enter
the seven-key metadata or exports. Missing, stale, unavailable, or full registration produces a
bounded diagnostic and preserves the last good snapshot.

**Fresh and attach exposure.** Jail processes append to the trace-named source at
`~/.local/state/yolo-jail-traces/.yolo-trace-<traceid>.jsonl`, using the existing writable
workspace-backed `.local` surface, not a new mount. A workspace-backed surface means storage
already mapped from that source's host workspace into the guest home; it is terminology, not a
new `workspaceLocal` API. This changes the current Go and generated-shell home-root filename
contract and must be applied consistently to both producers.

A later attach creates its per-trace source through that existing writable anchor, without
changing a running jail's mounts or home skeleton. An individual file bind at initial container
creation cannot satisfy this contract. Do not use `~/.yolo/bin`, which is reset on boot.
The supplied Podman mapping makes `.local` a narrow candidate; exact host mappings and safe
opening on Apple Container, macos-user, and sealed capture/build variants remain proof obligations.
Unavailable exposure, pre-creation, or seal compatibility means a missing source, never a launch
or mount failure. No common-backend success is claimed here.

**Producer grammar and lexical reading.** Jail producers mint no span or parent IDs. They emit
`{t,k,name,pid}` with optional `res:"s"`; `k` is `start`, `end`, or `mark`. Existing Go/shell
producer spellings allow ASCII uppercase letters and hyphens. The reader accepts those spellings,
then lowercases ASCII, replaces each byte outside `[a-z0-9_.]` with `_`, and limits the result to
64 output bytes. Unknown fields, including injected IDs, parents, paths, and credentials, do not
become sanitized attributes. Times and pids must be integral and representable. Coarse `res:"s"`
is preserved as a resolution observation, not fabricated subsecond precision.

Every admitted source read examines at most its last 1,048,576 bytes and latest 2,000 complete
physical rows. Malformed complete rows consume that row budget. A partial first row and an
incomplete final row are not complete occurrences. Across all source instances, a trace admits
at most 2,000 source occurrences over its lifetime; rereading an occurrence consumes no additional
slot. This is separate from U2's host-format parser bounds. Limits on reading and normal producers
do not bound arbitrary bytes a hostile guest can write.

A producer's short append is not proof that multiple processes or failed writes can never leave
torn lines. Readers tolerate incomplete/malformed rows. Go/shell writing and source cleanup must
honor the same confined source contract; an existence test followed by pathname redirection is
not a race-free no-follow guarantee. If a producer cannot use a safely established append target,
it omits tracing and leaves the legacy timing output unchanged.

**Confined opening and cleanup.** Starting from the validated host workspace anchor, open each
untrusted directory component descriptor-relative, without following links, and verify directory
type. Open the final source non-blocking and without following links; check it is regular before
reading bytes. Retain that descriptor for reading: validation never authorizes a path-based
reopen. Reject links, nonregular files, moved or substituted anchors, and unverifiable continuity.

Cleanup uses only the registered source name beneath that same confined anchor, with validated
ownership and continuity. It never recursively removes workspace content, follows a substituted
link, resolves a moved workspace to another location, or deletes a replacement user file.
Uncertain ownership means skip source cleanup and report retained auxiliary content. Pruning a
host trace does not independently authorize workspace deletion.

**Occurrences are not spans.** An occurrence is a complete physical source row. Its ingestion
key is `(host source instance, absolute byte offset)`, not its time, name, pid, normalized bytes,
or content digest. Two byte-identical rows at different offsets remain distinct, including
whole-second shell rows. Rereading the same verified occurrence reuses its persisted association.
Host-minted random span IDs remain nonzero and stable. A start and its unambiguously matched end
share one span ID; marks and diagnostics do not become invented observed spans.

**Display names are not matching names.** A matching name is the exact original decoded
producer spelling in the source row, before ASCII case folding, replacement, or output truncation.
Matching evidence is eligible only when that spelling is at most 64 bytes and every byte belongs
to `[A-Za-z0-9_.-]`, the existing producer alphabet. Equality is exact byte equality of those
bounded spellings; no normalized name, shortened prefix, digest, or probabilistic comparison
establishes it. An empty spelling, if accepted by the lexical reader, compares only with another
empty spelling. This identifies the emitted spelling, not an unknowable name before a producer
itself shortened it.

The existing transactional ingestion bookkeeping retains only the bounded exact spelling needed
for admitted pairing state, alongside the existing source-instance, pid, and occurrence
associations. It is host-private matching evidence, never a display name, exported attribute,
metadata field, or diagnostic value. Do not retain the rest of a raw source row or an unrestricted
payload for this purpose. At most one at-most-64-byte matching spelling is stored per admitted
source occurrence; the existing 2,000-occurrence trace ceiling therefore also bounds this state.
All actual serialized spelling, association, and control bytes, including old/temporary copies
at peak, remain charged under the 200,000-byte trace and 10,000,000-byte machine limits.
This is part of the existing committed ingestion state, not an unaccounted private cache.

A spelling outside that alphabet or longer than 64 bytes is ineligible for pairing; truncating,
normalizing, or hashing it cannot repair eligibility. If exact matching evidence cannot be
retained, validated, or admitted within the budget, do not fall back to display-name equality.
Preserve earlier evidence and decline the new pairing-dependent ingestion, or retain an admitted
start as unfinished and report an end as orphan/unmatched. Diagnostics expose only bounded
categories and counts, never the original spelling, rejected payload, or credential-like content.
Raw matching evidence is excluded from exports even when the sanitized display name differs.

Pairing follows byte-offset source order within `(source instance, pid, exact matching name)`.
An end completes a start only when exactly one matching open start is supported by continuous,
unambiguous source evidence. Overlapping starts with the same exact matching name are retained
as unfinished starts and diagnosed; no FIFO/LIFO rule guesses their completions. Different exact
names remain different pairing streams even when their display names collide. Orphan ends and
missing-window evidence are diagnostics, not completed spans. After ambiguity or a continuity
gap, an affected pairing stream cannot assert completion without unambiguous history. Timestamp
or display-name equality alone neither pairs nor deduplicates rows. An end before its start
cannot produce a negative interval.

**Monotonic refresh and recorded parents.** The host refresh owner serializes ingestion and
commits sanitized observations together with their occurrence associations, continuity,
calibration, and parent decisions. Recovery exposes either the prior complete state or the new
complete state, never a snapshot paired with different identity bookkeeping. A missing or failed
refresh leaves the prior snapshot readable.

Refresh preserves every previously admitted occurrence, its span association, and every completed
interval. It may add new occurrences or complete an unfinished span with an unambiguous newly
observed end; it cannot replace a completed interval, renumber spans, or discard prior evidence
to make room. Only additional evidence that fits [§5.7](#57-limits-and-failure) is admitted.

A jail span's recorded explicit parent is selected once from host waiting-span evidence belonging
to that source and covering the observed start. Ambiguous or absent waiting evidence does not
authorize an invented parent: omit it and diagnose the missing relationship. Recorded explicit
parents are frozen, including after an end arrives. U2 continues resolving known explicit parents
before its derived enclosure rule. Derived enclosure means the reader's display relationship
inferred from interval containment; it and unfinished display extents may change with new
evidence. Those changes are not rewritten recorded causality.

The refresh owner persists continuity evidence for captured source ranges and the established
source identity. Replacement, shortening, or conflicting previously captured content makes the
source stale; earlier offsets are not reinterpreted. Resume a replaced source only through a new
host registration. No jail-chosen identity or content deduplication bypasses this rule.
Continuity/bookkeeping byte costs must fit admission; inability to retain or validate them means
decline new ingestion, not weaken identity.

**Clock calibration.** Persist each source's calibration before admitting its timed observations.
For a source sharing the host clock, establish zero offset only with supported backend evidence.
Otherwise compare that source's jail "boot done" observation with its corresponding host ready
event. Apply a measured difference over 50 ms and retain a sanitized alignment note; smaller
differences use zero offset. Apply calibration before trace-window filtering. If calibration
cannot be established unambiguously, report the unavailable source and preserve any prior
snapshot rather than admit observations whose timestamps must later be rewritten. Established
calibration is not recomputed merely because refresh runs again.

Refresh is attempted at the keeper's ready frame, session end, and explicit trace reads when
registered sources remain available. It requires the live-claim and admission protocol below.
An offline readable store remains usable without a source registry or refresh access.
`host-perf.log` and `yolo-perf.log` keep their formats and readers; tracing is an additional sink,
not their replacement.

### 5.4 Parents

Across processes the parent is explicit, from [§5.2](#52-propagation). Inside one process the
existing call sites are flat, and they stay flat: a span that was given no parent is placed, when
the trace is read, under the shortest span of the same lane that was open for its whole duration,
or under the lane's root. A caller that runs work concurrently, where enclosure in time would
mislead, starts the span as an explicit child. Same-named concurrent spans (the seven
`preparation.check`) are told apart by their ids, and carry their key as an attribute.

### 5.5 Work that is not a child of a launch

The preparation scheduler runs on a timer and serves every launch, so its acts are not children of
any one. Each act is its own trace of kind `preparation`. A launch's summary lists the other
traces whose spans overlap its startup window, by time, which answers "what else was running when
I started" without inventing a parent.

A host service that was already running when the launch asked for it joins no trace; the launcher's
own span for that service carries a `reused` attribute.

### 5.6 What is added to the record

These are span families, not a file list. Each closes a row of [§3](#3-what-is-dark-today):

- `process.wrapper` and `process.pre_collector`: the two stretches before the first span, entered
  as completed intervals from the wrapper's start time and the operating system's record of the
  process's start.
- Named spans for each phase 1 step, for pack staging, and for the steps between `probes.done` and
  the workspace lock.
- One span per host service inside `launch.start_loopholes`, and inside each service's own lane a
  `service.startup` span ending when it is ready.
- `container.create`: from the runtime's start to pid 1's first line.
- In the jail half: each boot step as a start and an end written as it happens, for pid 1 and for
  each session's pass; `provision.mise_install` and `provision.bootstrap`; one span per program's
  readiness check or install, in microseconds where the shell has them; one span per in-jail daemon
  from its start to its readiness; and the `session.exec` mark that ends the startup window.

Beyond this list, a stretch gets a span when the summary shows it as unattributed time on a real
launch. That is the standing lesson of the existing reference: a span table's holes are only
visible on a real launch.

### 5.7 Limits and failure

**Store admission, not parser limits.** The retained store admits at most 50 traces, at most
200,000 decimal content bytes attributable to any trace, and at most 10,000,000 retained content
bytes machine-wide. Content bytes mean actual file lengths, not allocated filesystem blocks.
The host admission owner serializes admission and accounting; pure admission decisions determine
whether an effect may occur before any writer creates or extends content. Every host writer and
refresh participates. Reader bounds alone cannot enforce these ceilings.

Count metadata, lane headers/events, sanitized observations, ingestion bookkeeping, private
registry state, diagnostic/control content, and temporary or crash-left files. Empty lock files
add zero content bytes. Private content outside the trace directory is still charged.
Unattributed control content must be included in machine accounting; if safe attribution or
accounting is unavailable, decline new recording.

Admission reserves **peak physical occupancy before writing**. During replacement, both the
old file and the temporary new file count until the old content is safely removed. The same
rule applies to registry, bookkeeping, and truncation-diagnostic replacements, and to any files
needed for transactional publication. There is no transient excess allowance and no discount
for a file that is merely scheduled for deletion. If a non-destructive replacement cannot fit,
preserve the old snapshot. Previously admitted observations are not an expendable fallback.

A bounded diagnostic reserve is included in admission, with enough peak credit for every
simultaneously present copy. Public/private/control content, metadata slots, diagnostic reserves,
temporary or old copies, and crash-left content all count at physical peak, together with
outstanding credit that could become content. The fixed caps remain 50 traces, 200000 decimal
content bytes per trace, 10000000 machine-wide, 10000 events per process, eight attributes per
span, 240 bytes per attribute value, and 2000 admitted jail occurrences. They are not knobs.
The attribute allowance belongs to the span, not separately to each start/end or other record
for that span. The top-level boolean `launcher_root` designation is not an attribute and grants
no additional attribute allowance. Existing wire-line bounds and partial-record handling remain
unchanged.

A truncation mark uses admitted credit only; no room preserves prior evidence and warns outside
the store. Short/torn tails remain charged. Metadata-slot credit is unavailable to event writers;
scheduled deletion, pending publication, or failed cleanup never discounts present bytes.

Admission, publication, and append serialize ownership without intentional lock or queue waits;
contention disables the affected tracing effect. Append remains synchronous and checked, with
no fsync, batching, retries, or delayed replay. A process admits at most one truncation mark when
its event cap is reached, then stops that lane; a missing root end remains visibly incomplete.
Effect failures preserve checked partial accounting and continue launch and legacy logging.
The invocation shares one warning latch, using only:
`trace lane unavailable; continue with host-perf.log and inspect trace-store admission`.
No raw path/cause is printed and no success-looking completion is fabricated.

`TracesDir` and `TraceSourcesDir` remain pure path calculations. Admission may create only their
permitted private leaf roots beneath already-existing validated ancestry. If an early fresh-host
parent is missing, tracing declines and emits the same actionable warning; provision existing
storage through the normal initialization path before a later recording attempt. Do not move
`ensureStorage` earlier, add a root-mkdir shortcut, adopt/chmod existing roots, widen mounts, or
perform extra early configuration lookup to rescue tracing. OFF performs none of these effects.

**Mint, join, handover, and prune.** A live claim is independently acquired host protection on
the exact admitted directory and lock object, preventing prune while its owner uses them.
Immediate public mint remains compatible for acts with already-final metadata; launcher pending
admission adds the recognized private registration described above. Both serialize count/byte
admission, identity registration, and initial protection without a protection gap.

A writer or refresh independently joins only while validating and claiming that same registered
identity, whether public or recognized pending. Join never creates a directory, adopts unknown
state, follows substitution, or joins an old removed lock inode. Each child/writer owns its own
claim and release; duplication, inheritance, or a parent's unlock is not independent protection.

Prune uses original start, public or pending, then trace ID as its deterministic tie-break.
It holds an exclusive claim continuously through removal and releases accounting only for
verified removal. A failed/partial removal retains the remaining charge and diagnostic state;
unknown or possibly live state is not pruned. No writer recreates an entry after pruning.
All participating launcher, keeper, build, service, preparation, and refresh owners are covered.

The launcher retains its claim throughout its invocation, including child acquisition. A child's
acquired-claim acknowledgement is required before any earlier parent release; ordinary keeper
liveness/status frames prove no such acquisition. No handover requires intentional waiting.
Store close follows every claim and writer owned by that store.

Unknown liveness, identity, locking support, or accounting means keep the trace and decline unsafe
recording. No writer, including a late child, recreates a pruned trace. These rules require changing
the current `TraceSink` missing-directory creation path; it is not already a safe join primitive.

At capacity, mint may remove safely claimable inactive traces before admitting a new one.
If live or uncertain traces prevent room, do not mint. Unsupported, incompatible, or already
over-budget stores are not automatically shrunk or deleted by this protocol. Decline new recording
and report the condition until safe accounting and room exist; refusal does not retroactively
bring legacy excess within the cap.

**Immediate admitted writes.** An admitted host event is appended synchronously as it happens,
without `fsync`, a delayed batch, or an intentional contention wait. A failed non-blocking claim
or admission degrades tracing with one actionable warning; it does not add a 50 ms append wait
or 250 ms mint wait. The launch and legacy sink proceed independently. Append visibility is the
guarantee, not durable-storage survival.

A short or failed append is not reported as stored. Disable that lane after the first failure,
do not retry into its torn tail, and keep all physically written bytes charged. Another process
never appends to that process's lane. Failed snapshot/control publication preserves the prior
complete state. Partial temporary content remains charged until safely removed; unknown cleanup
means retain and account for it, not reuse its credit.

| When | What happens |
| :--- | :--- |
| Creation, safe join, admission, or locking fails | one bounded actionable warning; tracing degrades, the launch proceeds, and `host-perf.log` remains independent |
| A process exits through `os.Exit` or is killed | successfully appended lines required no exit flush; an unfinished start remains unfinished, and an interrupted/failed append may leave a rejected torn tail; no SIGKILL or durable-storage guarantee |
| A process reaches 10,000 trace events | stop trace-event emission and use admitted diagnostic credit for at most one `truncated` mark |
| A span carries more than 8 attributes, or a value over 240 bytes | drop the excess |
| A source is absent, stale, unsafe, malformed, or outside the read window | retain earlier sanitized evidence, admit only safely supported new evidence, and report missing, stale, or incomplete collection |
| A trace reaches 2,000 admitted jail occurrences, or source/registry limits | retain previous evidence and decline additional occurrences or registrations |
| Two independent launches use one workspace | distinct trace IDs and host directories; deliberately joined children register separate source instances within their parent's trace |
| The retained store lacks count or byte room | prune only safely claimed inactive traces; otherwise decline recording |
| A trace has unknown fields or an unreadable first line | ignore unknown fields; skip an unreadable file and identify it by basename |

The 10,000-event trace-sink limit is not a collector-memory ceiling. At the inspected pin,
`perf.Log.emit` appends to its in-memory event slice without such a ceiling. No bounded-memory
claim follows from this design's file limits, and no buffering remedy depends on that false
premise.

Attribute values remain names, counts, exit statuses, and keys yolo already prints, never
environment values, tokens, home paths, or command output. A readable mounted log root may expose
every workspace's traces, which is why exported data contains only approved names and timings.

The retained-store cap does not bound arbitrary guest-writable workspace content. Normal source
producers keep their 10,000-event limit; each source scan, source count, admitted occurrence count,
and private registry has the separate bounds in [§5.3](#53-writers-files-and-trust).
A hostile guest can exceed producer limits. Cleanup remains confined and ownership-checked;
uncertain auxiliary source content is reported and left in place, never recursively deleted.

The 10 ms host and 5 ms boot budgets remain unchanged. A non-blocking protocol is not a measured
overhead pass: benchmark and recorded on/off evidence are still required by
[§8](#8-gates-and-the-overhead-budget).

## 6. Reading a trace

A new verb, `yolo trace`, with three acts:

- `yolo trace` lists the retained traces: when, which kind, which workspace code, the startup
  window's length.
- `yolo trace show [<id>]`, defaulting to this workspace's latest launch, prints the **summary**:
  the startup window's length; each lane's share; the fifteen spans with the most time of their
  own (duration less children); every stretch of unattributed time over 100 ms, named by the
  spans on either side; spans that did not end; and the other traces that overlapped.
- `yolo trace export [<id>]` writes the timeline ([§7](#7-the-viewer-decision)).

The verb reads the trace root. Where it cannot (inside a jail with no view of the host's logs), it
says so and prints the command to run on the host.

The printed `--timing` table and its gates are unchanged. A launch that records silently names the
verb in the one line it already prints.

## 7. The viewer decision

The export writes a file for a viewer that already exists, and yolo ships no viewer of its own.
Three ways to draw a timeline were weighed:

| | Dependencies | Shows a hang | Works offline | yolo maintains |
| :--- | :--- | :--- | :--- | :--- |
| **Export a file for an existing viewer** | none | in the summary; the viewer draws the open span to the trace's end | the export yes; Perfetto's page loads from the web | two encoders |
| **Also a single-file HTML report** | none | yes, drawn as yolo chooses | yes | the encoders and a viewer |
| **OpenTelemetry Go SDK, exporting live** | 20 new modules | no | no; needs a running collector | the wiring |

**Exporting a file.** The existing plain-`encoding/json` encoders write
[Chrome trace-event JSON](https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU)
for [Perfetto's UI](https://ui.perfetto.dev), and
[OTLP JSON](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding)
for compatible HTTP collectors. No SDK or custom viewer is added. The CLI source at EEDB
implements offline export and explicit OTLP POST; production trace collection and live refresh
remain unbuilt. This is source evidence only, not a native-runtime, real-endpoint, or overhead
pass. The command reuses those encoders without redesigning their representations.

**Command contract.**

`yolo trace export [id] [--format chrome|otlp] [-o <file>] [--otlp-endpoint <url>]`

| Argument or condition | Behavior |
| :--- | :--- |
| Explicit `id` | select that retained trace; reject an invalid or unknown ID |
| No `id` | use show's existing selection: kind `launch` in the current workspace, greatest metadata `start`, then lexicographically greatest trace ID |
| No workspace identity or matching launch | actionable failure; do not select another workspace or a preparation trace |
| No endpoint and no explicit format | Chrome JSON |
| `--format chrome` or `--format otlp` | choose that existing encoder; reject other values |
| `-o <file>` | a literal file path, not a directory, suffix rule, or stdout alias |
| No `-o` and no endpoint | JSON on stdout; diagnostics on stderr |
| `--otlp-endpoint <url>` without an explicit format | OTLP JSON, sent only to that explicit URL |
| Endpoint with explicit `chrome`, or endpoint with `-o` | argument error before sending or replacing output |
| Repeated value flags, missing values, or extra positional arguments | argument error; no output replacement or request |

Selection is based on metadata start, not latest observation or refresh time.
A readable read-only host-store view inside a jail can support offline export. When the view is
missing or unreadable, guidance is conditional on being in a jail, as for existing list/show.
Unavailable refresh preserves the last snapshot with a diagnostic; it is not an unconditional
in-jail refusal. When refresh is possible, it obeys [§5.3](#53-writers-files-and-trust) and
[§5.7](#57-limits-and-failure) before export reads a consistent committed snapshot.

For file output, require an existing parent directory and refuse symlink or nonregular targets.
Publish through an exclusive temporary file in the destination directory, with race-safe
validation and replacement. Encoding/write/close/publication failure leaves a previous regular
output intact and returns failure; no partial output is called successful. Do not create parent
directories or follow a substituted target. Explicit user-selected export files are outside the
retained-store budget. Stdout cannot be rolled back: propagate a write failure and return failure.
Success leaves stdout containing only the selected JSON representation.

**Explicit collector POST.** Validate the endpoint before any request: it must be an absolute
`http` or `https` URL with a host, no userinfo, and no fragment. Reject invalid ports, malformed
escapes, and unsupported schemes. Preserve its validated path and query exactly; do not insert
`/v1/traces`, add a suffix, or derive a different endpoint. Never echo userinfo, rejected input,
query values, response bodies, or `url.Error` verbatim.

Encode and validate the selected trace before sending. Make one HTTP POST with
`Content-Type: application/json`, using the OTLP encoder's JSON body. Use a proxy-free transport:
no environment proxy, ambient authorization, cookies, client credentials, or new credential
configuration. HTTPS uses normal server-certificate verification. No redirects, application
retries, authentication negotiation, or follow-up requests. The total request, including response
reading, has a 10-second deadline. A failed or uncertain send is not retried.

Read at most 65,536 response bytes, using one extra byte only to detect excess. An oversized,
truncated, unreadable, non-2xx, or malformed response is an export error, not unchecked success.
A success response must decode as an OTLP JSON `ExportTraceServiceResponse` object.
Decode `partialSuccess.rejectedSpans` as a nonnegative 64-bit count according to protobuf JSON:
decimal strings and valid integral numeric representations are accepted; negative,
fractional, or overflowing values are errors. Absent partial success or a zero rejected count
means no reported rejection. A nonzero count returns failure and reports only the bounded count,
never the collector's free-form error text. Unknown response fields do not become diagnostics.

On successful POST, stderr reports the accepted destination as its scheme and host only, and the
number of submitted spans; stdout contains no payload or response. Failures use bounded,
redacted categories such as validation, connection, timeout, HTTP status, response decoding, or
rejected spans. Do not print transport errors containing the URL. The file-export fallback is
`yolo trace export [id] --format otlp -o <file>` for users whose collector access needs a proxy or
credentials outside this command's authority.

Command failure returns nonzero; successful file/stdout export or a fully accepted POST returns
zero. None of these command failures affect launch success. No launch opens a tracing network
connection, and no recording or endpoint configuration key is added. These command defaults are
engineering decisions under the settled explicit-export owner ruling.

**Export representation, clarified 2026-11-19.** Encoders consume the reader's resolved graph,
without changing its span identities or parents. A nil trace or writer, an invalid required id,
a nil span/lane entry, or a negative span interval returns an error; a valid empty trace exports
empty collections. An OTLP export with duplicate span ids returns an error instead of emitting
ambiguous identities. Chrome export may retain those observations, with the reader's anomalies
in diagnostic metadata. Neither encoder invents an observed span to repair the input.

Chrome marks and notes are instant events in their process lane. OTLP marks and notes become
span events on the shortest span in that same lane whose interval contains the event; equal
intervals use the reader's stable span order. This association is an export convention, not a
new recorded parent. An event outside every span is preserved in its lane's resource attribute
`yolo.unparented_events`: a JSON string containing an array of objects with `timeUnixNano`
(decimal string), `kind`, `name`, and `attrs`. It is not drawn as a span or silently dropped.
The event's kind is retained as `yolo.event_kind` when represented as an OTLP span event.

An unfinished interval ends at the reader's trace end for display and carries
`yolo.unfinished=true`; that flag never asserts the operation finished. Export diagnostics
retain skipped-file reasons and graph anomalies without adding absolute source paths: skipped
files are named by their basename only. Encoder write failures are returned to the explicit
command, not retried or hidden. These are engineering decisions about the two representations,
not amendments to the owner's recording or export opt-ins.

**The SDK.** Measured 2026-11-19 in a scratch module importing the SDK and its OTLP HTTP exporter
at v1.47.0: `go mod vendor` produced 22 modules, 942 Go files and 19 MB, against this repository's
7 modules, 400 files and 9.5 MB; 20 of the 22 are new, gRPC and protobuf among them, and every one
would be committed under `vendor/` and compiled into `yolo`, the entrypoint and the in-jail daemon.
Weight is the smaller objection. The SDK hands a span to its exporter when the span **ends**, so a
launch that hangs exports nothing for the step it hung in, and the dangling `start` line is the
one diagnostic the existing system was built around. Its batching exporter loses what it holds on
`os.Exit`, the path the tty proxy's signal arm takes, and its unbatched one does network I/O inside
the launch. The shell on the jail side cannot use it at all.

> [!WARNING]
> "Use OpenTelemetry" reads as "import the SDK". The interoperable part is the data model and the
> wire format, and both are available without it. Importing the SDK later would replace the
> recorder that cannot fail a launch with one that can block one.

**A report page of yolo's own** could draw unattributed time as bars of its own, mark a span that
never ended, and work with no network. It is not built: it is a viewer to maintain, in JavaScript
no test here drives, and the summary already reports those things as text.

## 8. Gates and the overhead budget

**Recording is off by default.** The trace follows the existing recording gate unchanged and is
written exactly when `host-perf.log` is. The persistent switch is `perf_logging = true` in the
**user's** config. A workspace's or project's config cannot turn recording on: the validator
already refuses the key there, and the workspace is editable from inside the jail. A typed
`--timing` or `--verbose`, and the host-process variables `YOLO_TIMING` and `YOLO_VERBOSE`, record
the invocation they are given to, as they do today. An absent or unreadable user config records
nothing.

The launch prints nothing new: the table still needs a typed flag, and a silently recording launch
keeps its one line, which now names `yolo trace show`.

The jail half's lines are written whenever the launch handed the jail a trace context, and not
otherwise. `yolo-perf.log` is written on every boot, as today.

**Budget.** A recorded launch today writes about 200 host lines, measured as two header blocks of
78 to 81 and 121 or 122 lines. With the additions in [§5.6](#56-what-is-added-to-the-record)
the ceiling is 600 host events and 400 jail lines, each one short append with no sync.

| Budget | Ceiling |
| :--- | :--- |
| Wall time added to a launch's startup window, host side | 10 ms |
| Wall time added to the container's boot | 5 ms |
| Content bytes attributable to one trace, including temporary/control copies at peak | 200,000 bytes |
| Retained content bytes machine-wide, including private/control content at peak | 10,000,000 bytes |
| Admitted retained traces | 50 |

The time ceilings are checked by a benchmark of the sink (cost per event times the event ceiling)
and by one recorded comparison of the same launch with recording on and off, entered in the
reference at landing. Neither is a test's pass condition.

## 9. Finding where the time goes

**Capturing a slow launch.** With recording on, there is nothing to do in advance. After the launch
that felt slow, on the host:

```console
$ yolo trace show
$ yolo trace export -o launch.json     # then open launch.json in the viewer
```

**Reading it.** The summary is ordered for one question each: the startup window (how slow was
it), the lanes (host, keeper or jail), the top spans by own time (what to open in the timeline),
unattributed time (what to instrument next), overlapping traces (was something else competing).

**Chipping away**, as a loop with one rule: no fix without a span that prices it.

1. **Baseline.** Record three launches of each class on the owner's host: the first launch after a
   reboot, a warm launch with nothing changed, and a launch after a fork's upstream moved. Enter
   the summaries in the reference.
2. **Close the unattributed time** until it is under 5% of the startup window. This is
   instrumentation only.
3. **Rank** spans by own time across the warm class, which is the launch paid most often.
4. **For each of the top three, state the hypothesis and the span that would confirm it,** then fix
   it as its own change, with the before and after summaries in its commit.
5. **Re-baseline** and repeat while the top span is worth a change.

[Appendix A](#appendix-a-two-measured-launches) lists what the existing record already shows.
Those are observations to confirm in step 2, and none is a diagnosis.

**What done looks like.** `yolo trace show` on a warm launch prints a startup window whose
unattributed time is under 5%; the exported file opens in the viewer with a lane for the launcher,
the keeper, each started host service and the jail; a launch killed during boot shows the step it
was in as "did not end"; a build jail's spans sit under the launch span that waited for it.

## 10. What this does not do

- It does not instrument the agent. The startup window ends at the agent's exec; what the agent
  does before its first prompt is its own.
- It does not make anything faster. Each fix is a separate change, chosen from a recorded baseline.
- It is not a profiler. It answers which step, never which function.
- It adds no metrics, no log shipping and no ambient network connection.
- It does not change the `--timing` table, the two existing files or their retention.

## 11. Alternatives considered

- **Add ids to `host-perf.log`'s lines and keep one file.** Rejected: the file is shared by every
  launch of a workspace and by two processes per launch, its retention splits on headers, and a
  build jail and a host command have no workspace to find it in.
- **Let the jail side mint ids and write the host's file format.** Rejected: it would give
  untrusted input a say in the trace's structure, and the shell writers cannot mint ids cheaply.
- **Make the scheduler a child of the launch that registered demand.** Rejected: its acts serve
  several launches and outlive all of them; overlap by time says what the user needs.
- **Convert every existing call site to explicit parents.** Rejected for now: more than a hundred sites,
  for a nesting that enclosure in time recovers in all but the concurrent cases.

## 12. Risks

| Risk | Mitigation |
| :--- | :--- |
| Enclosure in time puts a concurrent span under the wrong parent (the housekeeping slot under the child window) | the summary ranks by own time, which a wrong parent shifts but does not lose; such spans become explicit children as they are noticed |
| The jail writes misleading timings | bounded, sanitized, clamped to the trace's window, and drawn as its own lane; the host's lanes never depend on it |
| A new variable in the jail's environment is taken for a contract | it is named in the launcher's list of known launch variables, and the warning in [§5.2](#52-propagation) is pinned by a test |
| A slow launch on a machine that was not recording leaves no trace | `yolo trace show` with nothing recorded names the one user-config line that turns recording on; the next launch is traced |
| Traces fill the disk of a user who turned recording on and forgot | 50 traces and 10 MB for the machine, pruned whenever a trace is minted |
| Perfetto's page is a web page | the export is a local file, the summary needs no viewer, and the OTLP form goes to any collector the user runs locally |

## 13. What I would build, in order

1. **Identity, the trace directory and the summary.** Ids on the collector, the second sink under
   the machine-wide root with its retention, launcher-to-keeper propagation, the reader with its
   parent rule, and `yolo trace show`. This
   alone turns today's spans into a ranked list with the unattributed time named, on the next
   launch.
2. **The jail half.** Steps written as they happen, the split provisioning stage, per-program and
   per-daemon spans, the session's pass, `session.exec`, and the widened reader.
3. **The dark host stretches** from [§5.6](#56-what-is-added-to-the-record), and the build jail's
   join to its parent.
4. **The export**: the two file formats, then the endpoint flag.
5. **The baseline**, entered in the reference, and the first ranked list.

macos-user and Apple Container take every step in the same change as podman. Each step lands with
its part of the reference rewritten.

## Decision Ledger

The `OQ-TR` rows are the owner's settled rulings; the `TR-D` rows are engineering decisions.
TR-D15–22 clarify contracts without reopening [OQ-TR1–4](#decision-ledger). TR-D20–21 have partial
U6b2 library implementations; TR-D22 production ownership/finalization remains unbuilt.
Built entries below describe source existence at `0d8fef21db9a22f30e63b3376cc9f2e4af23f288`,
not tests run or backend/runtime-containment/endpoint/overhead passes.
A partial entry is not feature completion.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-TR1 | The timeline is an exported file for an existing viewer: Chrome trace JSON for Perfetto, and OTLP JSON. yolo ships no viewer and links no SDK | 2026-11-19 | [§7](#7-the-viewer-decision) | Partial: offline export CLI exists; no shipped viewer or SDK decision changes |
| OQ-TR2 | Recording is off by default and turned on by user-scope config (`perf_logging = true`); a workspace or project config cannot turn it on. Traces stay small by TR-D11's caps | 2026-11-19 | [§8](#8-gates-and-the-overhead-budget) | Existing timing gates; production trace admission pending |
| OQ-TR3 | Traces are kept machine-wide, under `~/.local/share/yolo-jail/logs/traces/`, naming the workspace by short code | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | Partial: offline reader/CLI understand the store; production lifecycle pending |
| OQ-TR4 | A trace is sent to a collector only on demand, by `yolo trace export --otlp-endpoint` | 2026-11-19 | [§7](#7-the-viewer-decision) | Partial: explicit POST export source exists; no endpoint/runtime pass claimed |
| TR-D1 | `internal/perf` stays the recorder; no tracing SDK is linked | 2026-11-19 | [§7](#7-the-viewer-decision) | Existing recorder and U1 tracing foundations |
| TR-D2 | Ids and the parent reference use the W3C trace-context formats | 2026-11-19 | [§5.1](#51-identity) | U1 identity/context APIs exist |
| TR-D3 | The jail receives the context as `YOLO_TRACEPARENT`; a launcher never adopts one from its environment | 2026-11-19 | [§5.2](#52-propagation) | Partial: U5 consumers/writers exist; complete deliberate host wiring pending |
| TR-D4 | A trace is a directory with one file per writing process, each admitted line appended synchronously as it happens | 2026-11-20 | [§5.3](#53-writers-files-and-trust) | Partial: admitted-sink library exists; production wiring pending |
| TR-D5 | Jail writers mint no IDs; host conversion persists random span IDs and publishes a non-destructive sanitized snapshot with matching bookkeeping | 2026-11-20 | [§5.3](#53-writers-files-and-trust) | Partial: U5 id-less producers exist; conversion/publication pending |
| TR-D6 | Establish and persist per-source clock calibration before admission; apply ready-event differences over 50 ms before trace-window filtering | 2026-11-20 | [§5.3](#53-writers-files-and-trust) | — |
| TR-D7 | A span given no parent is placed by enclosure in time within its lane; existing call sites stay flat | 2026-11-19 | [§5.4](#54-parents) | U2 graph resolves explicit parents before derived enclosure |
| TR-D8 | `host-perf.log` and `yolo-perf.log` keep their formats; the trace is a second sink | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | Partial: independent trace/root library exists; production wiring pending |
| TR-D9 | Scheduler acts are traces of their own, shown to a launch as overlap by time | 2026-11-19 | [§5.5](#55-work-that-is-not-a-child-of-a-launch) | —; production scheduler traces pending |
| TR-D10 | The startup window ends at the `session.exec` mark | 2026-11-19 | [§4](#4-vocabulary) | Partial: reader/summary foundations; complete collection pending |
| TR-D11 | Limits: 10,000 trace events per process, 8 attributes per span, 240 bytes per value, 2,000 admitted jail occurrences per trace, 200,000 content bytes per trace, 50 traces and 10,000,000 retained content bytes at physical peak; never prune a live trace | 2026-11-20 | [§5.7](#57-limits-and-failure) | Partial: bounded-store library exists; production lifecycle pending |
| TR-D12 | The reading verb is `yolo trace`, with list/show and explicit export | 2026-11-20 | [§6](#6-reading-a-trace) | Offline list/show/export source exists; refresh pending |
| TR-D13 | The time budgets are checked by a benchmark and one recorded comparison, never by a test's pass condition | 2026-11-19 | [§8](#8-gates-and-the-overhead-budget) | —; benchmark and recorded on/off evidence pending |
| TR-D14 | Export preserves recorded identities and diagnostics; unparented OTLP events remain resource metadata, malformed graphs error, and no observed span is invented | 2026-11-19 | [§7](#7-the-viewer-decision) | U3 encoders and source tests/goldens exist; tests not run in this amendment |
| TR-D15 | Join deliberately through launch options or the validated fork-build hidden argument; each separate process owns one lane, while in-process children reuse collector/sink and an explicit parent | 2026-11-20 | [§5.2](#52-propagation) | —; complete carrier wiring pending |
| TR-D16 | Host-private multisource registration retains each source's workspace/setup; at most 16 instances and 4,096 registry content bytes per trace at peak. Dynamic exposure uses the existing writable `.local` anchor, without mount widening; unsafe or unavailable exposure degrades collection | 2026-11-20 | [§5.3](#53-writers-files-and-trust) | —; safe locator, producer path change, and backend proof pending |
| TR-D17 | Occurrence identity is source instance plus absolute byte offset, never content equality. Pair only continuous, unambiguous source-order evidence using bounded exact original producer spelling; normalization is display-only. Matching evidence stays private, transactionally persisted, and charged at physical peak; unavailable equality means unfinished/orphan evidence, never guessed completion. Preserve random span associations, calibration, frozen explicit parents, admitted observations, and completed intervals | 2026-11-20 | [§5.3](#53-writers-files-and-trust) | Partial: U7a bounded lexical parser, private matching projection, occurrence budget and coarse-second marker exist; U7b persisted pairing/association, calibration/parent state, registration/confinement and refresh pending; tests not run in this amendment |
| TR-D18 | Reserve all physical peak content before effects, including private/control and old/temp copies. Mint/join/prune validate one admitted directory/lock with continuous live claims; no join mkdir, delayed batching, intentional contention waits, or destructive fallback. Failed lanes stop and partial bytes remain charged | 2026-11-20 | [§5.7](#57-limits-and-failure) | Partial: bounded admission/live claims and U6b2 pending reservation/once-only publication exist; production lifecycle remains pending |
| TR-D19 | Export uses metadata-start selection, Chrome default, literal `-o` or stdout, and explicit exact-URL OTLP POST with no proxy, ambient credentials, redirects, or retries; enforce bounded/redacted responses and typed rejection counts | 2026-11-20 | [§7](#7-the-viewer-decision) | CLI/export source exists at `eedb4b30194ea23020b4630e35cde2ddc80c1507`; no endpoint/runtime pass claimed |
| TR-D20 | One early neutral root; actual branch commits late; designated start permits canonical reader naming without changing recorded identity, names, time, or parent | 2026-11-21 | [Early recording and late launch truth](#early-recording-and-late-launch-truth) | Partial: U6b2 neutral-root designation and public-only resolver exist; production branch classification remains unbuilt |
| TR-D21 | Recognized private pending admission protects physical metadata capacity; original owner publishes exactly seven public keys once; pending/incomplete/unavailable selection and crash retention are explicit | 2026-11-21 | [Pending admission and once-only publication](#pending-admission-and-once-only-publication) | Partial: U6b2 pending store/publication and public metadata authority exist; production selection/finalizer remains unbuilt |
| TR-D22 | Explicit invocation lifetime ownership covers frontend/keeper final events and once-release; actual waiting-span carrier and independent child claims; no status-frame acknowledgement or kill-flush promise | 2026-11-21 | [Caller ownership, propagation, and finalization](#caller-ownership-propagation-and-finalization) | —; accepted integration contract, not built |

## Appendix A: two measured launches

Read on 2026-11-19 from `<workspace>/.yolo/logs/host-perf.log` and the jail's `yolo-perf.log` for
the yolo-jail workspace on the maintainer's host (podman, rootless, recording on by a persistent
opt-in). Times are seconds. "Unnamed" is time between two adjacent spans with no span over it.

| Stretch | Warm launch, 12:58 | Launch that rebuilt one fork, 01:43 |
| :--- | ---: | ---: |
| First span to `probes.done` | 0.38 | 1.48 |
| of which `runtime.ready` | 0.20 | 0.91 |
| Unnamed, `probes.done` to the workspace lock | 0.36 | 1.27 |
| `launch.fork_builds` | 4.05 | 49.54 |
| of which `preparation.build` (one build jail) | none | 46.84 |
| `launch.auto_load_image` | 0.22 | 0.31 |
| `launch.start_loopholes` (keeper) | 1.28 | 3.31 |
| Container start to the first session's spawn | 2.57 | 3.77 |
| of which pid 1's boot | 2.42 | not read |
| of which the boot step `report_durable_dir` | 2.00 | not read |
| The session's own boot pass | 0.41 | not read |
| Provisioning stage | 2.28 | 7.95 |
| **First span to the first session's spawn** | **9.26** | **60.69** |
| **Adding the session's pass and provisioning** | **about 11.9** | **about 69** |

Things the record shows and does not explain:

- **The seven fork and extension checks end one after another**, 0.55 to 0.60 s apart (0.57, 1.13,
  1.73, 2.27, 2.79, 3.35, 3.90 s after a common start), on a launch where none had anything to
  build.
- **One boot step is 2.00 s of a 2.42 s boot.**
- **Host services take 1.3 to 3.3 s to start**, as one span over eleven services.
- **Provisioning is 2.3 to 8.0 s**, as one number, with each program's row at whole-second
  resolution (one program shows 2 s, the rest 0).
- **Time before the first span is unknown**: the header is stamped to the second.

Record size: the file held 3,775 lines and 267,767 bytes across its retained runs.
