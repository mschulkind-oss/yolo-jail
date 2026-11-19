---
title: "One trace per launch: where startup time goes, and how to see it"
status: accepted
stage: DECIDED
next: "Complete the plan against the tree, then build step 1: identity, the trace directory and the text summary"
tags: [observability, timing, tracing, startup, perf]
summary: "Turns the existing timing spans into one trace per launch that follows the launch across every process it starts, fills the stretches no span covers today, and reads it back as a text summary and an exported timeline for an off-the-shelf viewer."
---

# One trace per launch: where startup time goes, and how to see it

**Status:** 2026-11-19. Every question ruled the same day; nothing built. Code claims verified at `f6f13e87d`; the measurements are two
launches read from the maintainer's own `host-perf.log` the same day ([Appendix A](#appendix-a-two-measured-launches)).

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

**Reads with:** [`startup-tracing-plan.md`](startup-tracing-plan.md) (the implementation plan's
seed, still a sketch: the planner completes it against the tree), [`perf-logging.md`](../reference/perf-logging.md)
(the reference for the timing system this extends; its principles P1 to P5 hold unchanged).

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

The launcher mints the trace id when it constructs the collector and opens a root span named for
the kind of launch (`launch`, `attach`, `host`, `host_apply`, `stop`). The scheduler mints one per
act it runs ([§5.5](#55-work-that-is-not-a-child-of-a-launch)).

### 5.2 Propagation

A process joins a trace only when its parent hands it the context deliberately:

| Boundary | Carrier |
| :--- | :--- |
| Launcher to keeper | a field in the keeper's plan, beside the recording gate it already carries |
| Launcher or keeper to a host service it starts | the child's environment |
| Launcher to a capture jail or build jail's launch, which runs in the launcher's own process | a field on the launch options; the sub-launch's root span is a child of the span that waited on it, and it writes into the parent's trace |
| Keeper to the container: pid 1, and each session's exec | `YOLO_TRACEPARENT` on the runtime's command line |
| macos-user launcher to the bootstrap and the guest's daemons | the same variable, in the session's environment file |
| Development wrapper to launcher | the wrapper's start time, in the environment; the launcher enters it as a completed interval |

> [!WARNING]
> **A launcher never adopts a trace context from its own environment.** Everything a launch writes
> into the container's environment is inherited by a `yolo` run inside that jail, so a nested
> launch would otherwise attach itself to the outer launch's trace. `YOLO_TRACEPARENT` is read by
> the entrypoint, the in-jail daemons and the generated shell only. The one launch-to-launch
> hand-over is in-process.

`YOLO_TRACEPARENT` shares no prefix with `YOLO_TIMING`, which
[P5](../reference/perf-logging.md#principles) requires of anything that crosses into the jail. It
is an identifier and no secret, so it is left in the session's environment.

### 5.3 Writers, files and trust

A trace is a directory, `<trace root>/<trace id>/`. The trace root is machine-wide,
`~/.local/share/yolo-jail/logs/traces/`, beside `launches.log`. No jail can write there, so every
file in a trace was written by host code, and what the jail measured arrives only through the
bounded reader below. A build jail and a host command have no workspace of their own, and need
none. `meta.json` names the workspace by its short code and never by its path, as `launches.log`
does; `host-perf.log` stays in the workspace.

> [!WARNING]
> The workspace's `.yolo/logs/` is where `host-perf.log` lives and looks like the place for the
> trace too. It is writable from inside the jail, which would make every trace file untrusted
> input to the summary and the export.

| File | Writer | Contents |
| :--- | :--- | :--- |
| `meta.json` | the process that minted the trace, once | `trace`, `kind`, `start` (nanoseconds), `jail` (its name), `workspace` (the short code), `version`, `commit` |
| `<lane>-<pid>.jsonl` | exactly one host process each | one JSON line per event, appended as it happens |
| `jail.jsonl` | the host's reader, replacing the whole file | the jail half, after sanitizing, in the host files' own line format: ids minted and every parent explicit |
| `lock` | held by the keeper, or by the launcher when there is no keeper | nothing; it marks the trace as live |

**One writer per file** is what ends [the interleaving](#the-record-itself). A line is
`{t, k, name, span, parent, dur, attrs}`: nanoseconds since the Unix epoch, the kind (`start`,
`end`, `mark`, `note`, `record`), and for a span its id, its parent's id when it was given one, and
on `end` the duration. A process's first line names its lane and pid.

**The jail half is written by the jail and read by the host.** Processes on the jail side (pid 1's
boot, a session's boot pass, the provisioning shell, each program launcher, the daemon supervisor)
append lines to one file in the jail's home, named for the trace. Each line is a single short
append, so lines from several processes never split. These writers **mint no ids**: a line carries
a time, a kind, a name and the writer's pid. The host's reader mints the ids and parents every jail
line under the host span that was waiting on the jail at that time.

The reader is the existing one, widened: it reads beneath a root on the workspace's `.yolo`,
regular files only, at most the last mebibyte and 2,000 lines, reduces every name to `[a-z0-9_.]`
and 64 bytes, and drops a line that does not parse or whose time falls outside the trace. It runs
at the two moments the host already reads the jail's file (the keeper's ready frame and the
session's end) and whenever `yolo trace` reads a trace whose jail file still exists. Each run
re-reads the whole source and replaces `jail.jsonl` by rename, so a late run can only add.

**The jail's clock is aligned at the ready event.** On podman on Linux and on macos-user the jail
shares the host's clock; under Apple Container and podman on a Mac it is a virtual machine's. The
reader compares the jail's own "boot done" line with the host's time for the ready event; a
difference over 50 ms is applied as an offset to every jail line and recorded as a note.

`host-perf.log` and `yolo-perf.log` keep their formats and their readers. The trace is a second
sink on the same collector.

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

| When | What happens |
| :--- | :--- |
| The trace directory or a file cannot be created | one warning, then a silent sink; the launch proceeds and `host-perf.log` is still written |
| A process exits through `os.Exit` or is killed | every line it had recorded is on disk; an open span is a `start` with no `end`, which the summary reports as "did not end" |
| A process records more than 10,000 events | it writes one `truncated` mark and stops writing |
| A span carries more than 8 attributes, or a value over 240 bytes | the excess is dropped |
| The jail half is absent, unreadable, a link, or oversized | the trace has no `jail.jsonl`, or a short one; the summary says the jail half is missing |
| Two launches on one workspace at once | two trace ids, two directories; nothing is shared |
| More than 50 traces exist | the launcher that mints a new trace removes the oldest beyond 50, skipping any whose `lock` is held and declining to remove one it cannot ask about |
| A trace from an older yolo, with fields this one does not know | unknown fields are ignored; a file whose first line is unreadable is skipped and named |

Attribute values are names, counts, exit statuses and keys yolo already prints (a pack, a fork, a
program, a service). Never an environment value, a token, a path under the home, or command output.
**Traces cannot grow without bound.** The caps above multiply out: at most 50 traces of at most
200 KB each, 10 MB for the machine, enforced when a trace is minted. A host that mounts yolo's log
directory read-only into a jail lets that jail read every workspace's traces, which is why they
hold names and durations and nothing else.

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

**Exporting a file.** The export writes
[Chrome trace-event JSON](https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU),
which [Perfetto's UI](https://ui.perfetto.dev) opens with one lane per process and nested bars,
and [OTLP's JSON encoding](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding), which
any OTLP collector accepts over HTTP (Jaeger's all-in-one image listens for it on port 4318). Both
encoders are plain `encoding/json`. Sending the OTLP form to an endpoint is one HTTP POST from the
export command, `yolo trace export --otlp-endpoint <url>`, which prints where it sent how many
spans. No launch opens a network connection for tracing, and there is no config key that makes
one.

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
| Disk per trace | 200 KB |
| Disk for all retained traces | 10 MB |

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

The `OQ-TR` rows are the owner's rulings; the `TR-D` rows are implementation decisions taken while
designing.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-TR1 | The timeline is an exported file for an existing viewer: Chrome trace JSON for Perfetto, and OTLP JSON. yolo ships no viewer and links no SDK | 2026-11-19 | [§7](#7-the-viewer-decision) | — |
| OQ-TR2 | Recording is off by default and turned on by user-scope config (`perf_logging = true`); a workspace or project config cannot turn it on. Traces stay small by TR-D11's caps | 2026-11-19 | [§8](#8-gates-and-the-overhead-budget) | — |
| OQ-TR3 | Traces are kept machine-wide, under `~/.local/share/yolo-jail/logs/traces/`, naming the workspace by short code | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | — |
| OQ-TR4 | A trace is sent to a collector only on demand, by `yolo trace export --otlp-endpoint` | 2026-11-19 | [§7](#7-the-viewer-decision) | — |
| TR-D1 | `internal/perf` stays the recorder; no tracing SDK is linked | 2026-11-19 | [§7](#7-the-viewer-decision) | — |
| TR-D2 | Ids and the parent reference use the W3C trace-context formats | 2026-11-19 | [§5.1](#51-identity) | — |
| TR-D3 | The jail receives the context as `YOLO_TRACEPARENT`; a launcher never adopts one from its environment | 2026-11-19 | [§5.2](#52-propagation) | — |
| TR-D4 | A trace is a directory with one file per writing process, each line written as it happens | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | — |
| TR-D5 | Jail-side writers mint no ids; the host's reader mints them and replaces `jail.jsonl` whole | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | — |
| TR-D6 | The jail's clock is aligned to the host's at the ready event, past a 50 ms difference | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | — |
| TR-D7 | A span given no parent is placed by enclosure in time within its lane; existing call sites stay flat | 2026-11-19 | [§5.4](#54-parents) | — |
| TR-D8 | `host-perf.log` and `yolo-perf.log` keep their formats; the trace is a second sink | 2026-11-19 | [§5.3](#53-writers-files-and-trust) | — |
| TR-D9 | Scheduler acts are traces of their own, shown to a launch as overlap by time | 2026-11-19 | [§5.5](#55-work-that-is-not-a-child-of-a-launch) | — |
| TR-D10 | The startup window ends at the `session.exec` mark | 2026-11-19 | [§4](#4-vocabulary) | — |
| TR-D11 | Limits: 10,000 events per process, 8 attributes per span, 240 bytes per value, 2,000 jail lines, 200 KB per trace, 50 traces and 10 MB kept, a live trace never pruned | 2026-11-19 | [§5.7](#57-limits-and-failure) | — |
| TR-D12 | The reading verb is `yolo trace`, with `show` and `export` | 2026-11-19 | [§6](#6-reading-a-trace) | — |
| TR-D13 | The time budgets are checked by a benchmark and one recorded comparison, never by a test's pass condition | 2026-11-19 | [§8](#8-gates-and-the-overhead-budget) | — |
| TR-D14 | Export preserves recorded identities and diagnostics; unparented OTLP events remain resource metadata, malformed graphs error, and no observed span is invented | 2026-11-19 | [§7](#7-the-viewer-decision) | — |

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
