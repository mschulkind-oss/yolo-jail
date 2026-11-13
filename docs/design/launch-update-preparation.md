---
title: "Prepare between launches; materialize only at a launch"
status: accepted
stage: DECIDED
next: "Final runtime and platform acceptance: a fresh non-agent nested launch, then real rootless and native Darwin evidence"
---

# Prepare between launches; materialize only at a launch

**Status:** 2026-10-14. The combined candidate connects source scheduling, explicit freshness, independent shared demand, HOST delivery, Apple check-only scheduling and supported ordinary npm capture. The independent combined-source review ran on 2026-11-12, and the seven major defects it found are fixed ([the record](launch-update-preparation-plan.md#review-2026-11-12)). Final combined runtime/platform acceptance remains; this is not a shipped claim.

> **In short.** Environment preparation should continue between launches, using the same admitted-build pipeline. A launch chooses a generation; background work never changes the environment already running.

**The shape:** one detached host scheduler registers demand with independent keyed workers running existing check/replay/build/admit acts; launch-time readers copy, install and render their selected generations.

**Cost:** persistent scheduling state and narrowly scoped live host-copy retention, not another builder or an agent registry.

**Start at:** [The policy boundary](#the-policy-boundary).

**Needs your ruling:** None. [OQ-PD23](program-delivery.md#decision-ledger) includes ordinary CLI and yolo-installed MCP programs; remaining capture acceptance is agent engineering.

**Reads with:** [ready independent implementation plan](launch-update-preparation-plan.md), [build timing authority](pi-extension-store-builds.md), [patched-fork safety](../reference/patched-forks.md).

---

## The policy boundary

My recommendation is to extend the existing detached act, not build a second delivery pipeline.
[XB-D28](pi-extension-store-builds.md#XB-D28) already covers everything yolo builds or captures;
[XB-D21](pi-extension-store-builds.md#XB-D21) and [XB-D22](pi-extension-store-builds.md#XB-D22) retain their platform and host scope.
The requested continuation adds ongoing preparation and an explicit update-and-wait launch option.
It includes the settled [machine-level CLI/MCP capture architecture](program-delivery.md#machine-level-program-preparation).

- **P1 — Pack authority:** selected pack declarations define programs, sources, recipes, platforms and ownership. No tool-name switch or agent registry.
- **P2 — Admission before delivery:** checks and candidate builds do not constitute a usable generation. Existing guarded admission, completion receipts and compare-and-swap remain authoritative.
- **P3 — Preparation is not rendering:** timers may update build/capture stores and outcome records, never host agent settings, host floor links, workspace homes or running-jail copies.
- **P4 — Policy survives speed:** user-scope `agent_updates` controls automatic timing. Absent/true/`launch` retains foreground behavior; `next-launch` enables ongoing preparation; false freezes an established revision automatically. Explicit human update overrides that automatic freeze and stamps/backoff, not explicit source constraints. With no good artifact or recorded initial identity, preserve today's initial cold resolution and mandatory readiness; do not invent held bytes. Plain fork standing branch pins move only through authorized manual resolution, never on a timer.
- **P5 — Failure evidence survives:** known applicable patch failures refuse affected launches under [PF-D81](../reference/patched-forks.md#PF-D81), even with a serving build. Background completion, notice consumption and busy skips cannot clear that evidence.

A normal `next-launch` launch reads a valid admitted build without network/check/build waits,
except cold establishment where neither usable build nor authorized fallback exists.
A changed recipe/series/platform is not a valid serving build. The existing explicit
[PF-D82](../reference/patched-forks.md#PF-D82) historical cached-good route stays separate: validate the prior receipt and runtime,
disclose its old contents, retain current failure/edits, and never manufacture current success.
A launch need not check the network to discover a failure not yet observed; once failure evidence is recorded,
the next applicable launch must enforce it.

## Complete outcome inventory

| Outcome | Current source / remaining contract |
| :--- | :--- |
| Built extension trees, patched or empty-series | Keyed pool, copy and Podman launch-triggered detached advance exist. Add ongoing scheduling and shared visibility. |
| Patched fork programs | Advance/admission exists; extend timing to `next-launch`, retaining fatal patch semantics and explicit historical recovery. |
| Plain source forks | Timer may build a missing artifact at its recorded standing pin, never move that pin. Manual update reuses authorized manual pin resolution; explicit tag/commit constraints still apply. Unchanged admitted inputs reuse their artifact. |
| Captured ordinary programs | Reuse host capture/floor machinery for immutable machine-level artifacts under [OQ-PD23](program-delivery.md#decision-ledger); supported warm-update producer and consumers are connected in the candidate, with final real-runtime acceptance outstanding. |
| Declared programs not capturable on a platform | Keep their supported installer/readiness route; say unsupported preparation explicitly. No timer executes an unsealed host installer. |
| MCP server programs | Included in machine preparation for supported yolo-installed declarations; preserve bounded invocation refresh until its tested replacement lands. Raw npx or hand-installed servers are not silently captured. |
| Raw or hand-installed extensions | Existing in-jail prelaunch refresh remains foreground; not a pack-built artifact. Explain this residual wait. |
| Configured pack sources | Existing refresh remains separate; scheduling may reuse its authorized resolution, but must not render refreshed declarations into a running environment. |
| Host tree/program delivery | Linux preparation can run detached; selected launch/apply materializes the admitted generation. Timer never swaps host links or settings. |
| Apple Container | Implement the ruled check-only advance; build pending candidates at a fresh launch only when runtime liveness proves no conflicting jail. |
| macos-user and Darwin host | Preserve current unsupported tree-build scope; native program captures only through already supported sealed routes. No Linux tree relabeled Darwin. |
| Update-now-and-wait | Add shared jail/host launch policy and long/short option, scoped below. |
| Pending/busy/completed/error reporting | Extend per-key outcomes, status inspection and foreground progress; retain required launch disclosures. |
| Timing and acceptance | Matching installed-host samples exist, but cold/warm and complete wall-clock remain unproven. Obtain controlled host and native/rootless evidence. |
| Native loading/migration | Real extension loading and maintainer migration remain external/manual acceptance, not proven by compilation or fake build fixtures. |
| Host environment consistency | Separate worker; shared provider declarations are present. This design neither claims a defect nor owns that change. |

## Selection and persistent ownership

The scheduler is one detached host `yolo` process per user storage root, started idempotently by
an eligible launch or explicit start action. It uses the existing self-exec and detach posture:
closed stdin, log-backed streams and no interactive terminal. Detachment does **not** scrub environment.
Construct a bounded environment explicitly: storage/home, executable/tool lookup, noninteractive transport
and required runtime/source controls only; use existing provider environment declarations to exclude
agent credentials and prevent arbitrary launch variables or hatch inheritance. Credentialed host source
transport retains only its existing authorized helpers/transport inputs; sealed build children receive
no credentials or host-file grants. These are separate boundaries, not a tool-name credential denylist.
No OS login service, root daemon or hidden package installation is introduced.

1. A launch registers only selected eligible artifact keys, owning/contributing pack specs, platform,
   backend and resolved input identities. Empty selection registers nothing and starts no process.
2. The worker resolves declarations through the same **user-authorized** pack resolver as today's detached act.
   Never trust a disposable staged tree, workspace-modified manifest or paths received as executable authority.
   Workspace-only keys the user resolver cannot authorize remain foreground-only with an inspectable unresolved reason.
   Explicit freshness still uses their existing scoped foreground check/build/admission route; it is not
   refused merely because detachment is unauthorized. These keys have no cross-process shared ownership
   or producer-exit survival. Cancellation refuses freshness even when an old good build exists; a
   detached worker never executes their staged or workspace manifests.
3. User-selected `next-launch` packs seed the set; a launch can add an authorized key/platform actually needed by that launch.
   Duplicate keys coalesce. No recursive scan of workspaces, all shipped packs or unselected programs.
4. Reread user policy before each dispatch and before admission. Removed/off selections cancel automatic
   work without moving its pointer after revocation. A manual request may override automatic off, but
   cannot survive removal of selection or changed explicit source constraints. A host selection never
   implies jail selection or vice versa.
5. One scheduler lease protects scheduling records. Per-key locks and existing admission writers own build/check records;
   the scheduler does not duplicate these records or declare its own successful build.
6. Keep previously admitted entries and subscriptions across process restart. Records lacking new scheduler metadata
   remain usable under existing receipts; derive due state from their existing attempts, not a synthetic successful check.

Policy/config read failure prevents **new autonomous dispatch** and leaves existing builds intact, with a status reason
and next step to repair config. This is not a change to the launch policy reader's existing default-open behavior.
Cached input identities are invalidation hints, not authority: reread declaration and recipe before an act or a handoff.

Subscriptions are keyed by declaration key/runtime/platform, with a monotonic input generation covering
owner/contributor, source, series and recipe. Duplicate registration preserves that generation; an edit
increments it and cancels old work. Keep inactive removed/off entries inspectable, never dispatch them.
A short atomic metadata transaction owns subscription replacement; the worker lease owns dispatch, and
existing guarded stores alone own admission. While a batch runs, a control watcher checks revocation
on the same 60-second cadence and cancels changed keys; foreground requests wake it immediately.
Before settle revalidate generation and policy, so a racing edit cannot admit stale work. Admission
uses a nonwaiting subscription guard inside the keyed record transaction to exclude concurrent
registration changes at its commit point. Guard contention defers admission with candidate retained;
registration never acquires a record/build lock. No record holder waits for a subscription lock. Corrupt
scheduling state refuses new dispatch and names repair, not a reset of proven build records.

## Cadence, due rules and resources

These are reversible engineering bounds, not new owner defaults:

- Wake every **60 seconds**, and immediately for newly registered work, config-change notification or a foreground request.
  Missed sleeps cause one reevaluation, never a burst of catch-up jobs. No networking when all keys are not due.
- Source checks preserve the existing **one-hour attempt interval** and keyed candidate/build backoff. Captures preserve
  their established release/refresh intervals while the capture adapter is built; the scheduler tick is not a new update interval.
- Recipe/series/source/platform edits invalidate a queued request immediately. A pending candidate becomes runnable when its
  existing backoff permits. A busy skip does not stamp success or extend the upstream attempt throttle.
- Offline/fetch/build failures retain their exact typed outcome and retry time; no retry loop inside one tick.
  Scheduler-start/resolution failures retry no more than hourly; explicit update requests bypass time throttles, not safety gates.
- Existing per-act pools bound checks to **8** and builds to `min(4, max(1, CPUs/2))`; Apple Container
  permits one foreground build and zero background builds. One scheduler dispatch batch runs at a time;
  reuse pools and per-key exclusion with simultaneous foreground acts. Cross-process global permits and
  fairness are optional followup only if measured resource pressure warrants them, not prerequisites.
- Process due subscriptions in stable key order, skip busy keys and retry next tick. Do not hold the
  scheduler lease's metadata transaction while acquiring an artifact/check/build lock; never kill a
  winner to favor a foreground request.
- Existing per-build/wait **20-minute** bound and **60-second** source-check/network bounds remain.
  Build children retain their existing **60-second** cancellation kill grace. No new act-wide deadline that
  would mark an otherwise healthy later key failed. Document total wait can be multiple build bounds.
- Sealed builds retain the existing build-jail CPU/memory limits and filesystem boundary; the scheduler cannot
  borrow credentialed launch limits or grant host files to accelerate a build. Disk-full stops that key with
  its cleanup/retry action, never reaps live generations to make space.
- Metadata is one latest scheduling record per key plus existing build provenance; use existing detach-log rotation
  (one MiB plus previous file). Do not introduce unlimited event history or timer-created artifact duplicates.

The worker sleeps while all keys are held/backed off. It exits when no eligible subscriptions remain;
it otherwise runs between launches until logout/shutdown, explicit stop or binary replacement.
An unsupported/unavailable runtime leaves due keys deferred with a reason, not a rapidly respawned process.

## Joining, cancellation and recovery

| Event | Contract |
| :--- | :--- |
| Two ordinary launches | Snapshot their own generation; coalesce registration. Neither waits for scheduler ownership or mutates the other's staged tree. |
| Two updates for one key | Join the same keyed act, reread guarded admission after the winner; no duplicate download/build for the same release/recipe/platform. |
| Explicit wait meets an older check | Registration snapshots the short per-key ledger epoch and monotonic started-check sequence. Only a greater sequence in that epoch with identical inputs qualifies; concurrent requests before that check share it. A check already running cannot qualify: reread its winner then run one follow-up fresh check. Wall time alone never qualifies work across clock rollback. |
| Inputs move during work | Guarded settle rejects stale admission. Automatic work requeues the newest authorized identity; an explicit request fails with a retry command rather than silently changing its selected vector. Retain detailed outcome. |
| Off/removal during work | Cancel before new check/build/admission; do not undo a generation already admitted before the policy change. |
| Waiter Ctrl-C | Detach that waiter from shared work. For ordinary existing foreground advances retain [PF-D25](../reference/patched-forks.md#PF-D25) and shared act-interrupt behavior. Explicit freshness wait cancels the launch, never silently claims freshness or starts stale. |
| No serving build and interruption | Launch stops; readiness cannot invent a program. Shared work with other demand may finish. |
| Worker SIGINT/SIGTERM/SIGHUP | Stop dispatch; cancel children, existing process-group signal then kill grace, remove only known owned build jails. Preserve pending/failure evidence. |
| Explicit worker stop | Same shutdown, no restart in that invocation; next eligible launch or explicit start can resume. Status names this. |
| Crash/SIGKILL | Lease expires; next launch/status-start checks process identity and runtime liveness. Unknown container liveness preserves staging and pending state. No PID-only kill or age-only deletion. |
| Binary/source skew | Worker records executable identity; incompatible replacement starts no new acts, drains/stops through owned lifecycle, next launch starts the selected binary. Existing source-skew gate still applies to build launchers. |

No scheduler global lock is held during a keyed act. Preserve build → record → mirror ordering;
never wait on an earlier lock while holding a later one. Status reads snapshots without build locks.
Joining uses request/outcome identities, not a consumed one-time notice or a PID alone. For
user-resolved declarations, each requester holds its own kernel-released demand lease; the detached
host worker owns production independently of any requester. A scheduler act attaches demand through
the same route. Requester cancellation or process exit detaches only that lease; positively witnessed
zero demand cancels the child and confirms the original backend's teardown before publication.
Unreadable lease state is not absence and cannot authorize admission. These shared guarantees do not
apply to the foreground-only declaration class above.

## Explicit update and launch materialization

The proposed surface is **`--update`, `-u`**, a boolean before the command separator, for
fresh jail launches and `yolo host -- <command>`. The source audit finds these spellings free in
both parser scopes; the top-level `yolo update` remains self-update, not this option.
Tokens after `--` remain the command's arguments. Help/dry-run performs no preparation.
Reject the option on an attach before any advance, naming a fresh launch as the next step;
never implicitly restart a running jail.

- Resolve the launch's selected eligible keys once; host exec restricts programs/trees to the command's declared owner,
  while a fresh jail prepares every selected declared program/tree needed by its readiness/render, not guessed from argv.
- A `yolo --update` invoked inside a jail cannot use the host's read-only fork lock to move a selected mutable plain-source
  fork's branch pin. Refuse only when that fork is required on this platform, before partial freshness work, and name
  `yolo --update -- <command>` from the host workspace as the next step. Fixed tag/commit inputs, unselected forks and
  unaffected supported routes remain eligible; ordinary non-update launch still uses the recorded pin.
- Request a **fresh check**, meaning a check started after the request's keyed epoch/sequence boundary under
  the same selected input identity; wait for its candidate admission. Bypass automatic `agent_updates: false`,
  stamps and retry/backoff timing because a human asked. Honor explicit tag/commit/source constraints,
  capabilities, selection, strict patch errors and cancellation. A standing branch pin is not an explicit
  immutable constraint: the manual resolver may move it. Timer resolution never does.
- Fresh check is not force-rebuild. Keep capture's existing rebuild mode separate. Wait on the keyed
  winner, reread guarded admission, and reuse a matching admitted artifact when release/recipe/platform
  is unchanged. If an older check predates this request, run one follow-up fresh check, not a rebuild.
- No candidate means a successful fresh check, not a rebuild. A failed check, build or unsupported required freshness route
  fails the explicit request with the repair/ordinary-launch route. Never quietly fall back while advertising freshness.
- Supported ordinary npm programs/MCP use the connected machine-prepared adapter; unsupported,
  customized or unobserved runtime inputs retain the bounded declared installer route.
- Apple Container can check now, but when another jail prevents a required build the flag refuses and names stopping that
  jail or preparing through supported Podman. Below the read-only floor no unsafe build/copy is authorized.
- Completion is a per-request vector of exact selected identities and admitted generations, not a machine-global “up to date” bit.
  Refuse combining explicit freshness with historical cached-good recovery, naming an ordinary recovery launch instead.
  A patch-failure bypass can authorize an ordinary launch of the admitted build, but cannot turn a failed freshness
  request into success. Preserve the concrete error and offer the separate working bypass command.

At handoff, the launch chooses its admitted artifact before copying/installing/rendering. Existing independent
per-launch jail tree copies keep completion-marker checks and one guarded reread if reaped; they need no
universal generation registry. Shared host tree/program delivery must not overwrite live module bytes:
retain host versioned copies that a tracked session uses until liveness is known gone, retaining on unknown
liveness. Untracked preexisting host versions are conservatively retained until explicit cleanup with a
warning. Only launch/apply swaps host links/settings. Program-generation changes belong to their actual
materialization adapter, not the initial tree scheduler. Admission alone never mutates a running environment.

Cold starts still complete mandatory program readiness and existing refusal/hatch behavior.
The flag changes preparation timing, not credential composition, boot PATH, host-apply consent or selected pack disclosures.
Attach uses the running staged tree and generation, never a newly prepared tree.

## Visibility and acceptance

Extend existing outcomes into **pending, checking, busy, building, admitted, unchanged, failed, interrupted,
unresolved and unsupported** states, each bound to key/input/request generation, timestamp, serving generation,
retry time and concrete reason/log path where applicable. “Admitted” says available for a later launch,
not installed or running. Busy is a deferred state, not success or failure.

- Extend `yolo pack status` with preparation snapshots and `yolo pack prepare start|stop|status` for
  worker control. Status never starts work or consumes notices; start reauthorizes recorded selection;
  an empty set exits without spawning. No new lane dashboard.
- Foreground waits print compact progress for shared checks, permit/key waits, builds and final result; piped output
  has bounded lines and no terminal control codes. No detached process writes into an old launch's terminal.
- Every error offers the easiest repair/retry action; patch application gets the existing prominent self-contained block.
  Per-key status persists after a one-time launch notice is consumed. Do not repeat unrelated valid-config warnings.
- Preserve pack read/exec trust banners and launch logs; progress compression is not a quiet mode.
- Timing separates registration, admitted-read, due check, shared wait, sealed build, copy/install, render, readiness,
  program refresh and handover. Measure full wall clock separately; overlapping spans are not additive.

Acceptance requires fixtures proving timer progress **without a second launch**, deduplication under concurrent
launches/waits, no credentials/host-file grants in builds, stale-input rejection, cold readiness, off/held behavior,
strict patch failure and historical recovery, cancellation/crash/liveness recovery, generation retention,
Apple Container check-only and host no-timer-render behavior. No interactive agent or API tests.
Runtime QA requires fresh non-agent nested launches for generic source changes, real rootless hosts for namespace/
loopback classes, and native Darwin for Apple Container/macOS limits. Static compile is not native acceptance.

## Alternatives, costs and limits

| Alternative / risk | Verdict / mitigation |
| :--- | :--- |
| Second generic updater/build cache | Rejected: duplicates provenance, safety gates and locking. Dispatch existing acts. |
| Timer renders host settings | Rejected: no authority for autonomous mutation of the real agent environment. |
| Global newest pointer for running sessions | Rejected: module reads can mix generations. Pin installs/copies and retain by liveness. |
| Launch-only background spawn | Insufficient: no progress between launches after that act exits. Add a scheduler around it. |
| Native/macOS support by relabeling Linux entries | Rejected: platform/recipe receipt must match; retain unsupported routes explicitly. |
| Persistent worker/resource pressure | Existing per-act bounds, keyed exclusion, due attempts and explicit lifecycle/status; consider global permits only after measured pressure. |
| Ordinary CLI/MCP producer support | Architecture is settled by [OQ-PD23](program-delivery.md#decision-ledger); validate each producer's sealing, compatibility and launcher/readiness handoff before replacing its existing route. |

This does not optimize agent startup internals, replace raw extension management, widen credentials,
change confinement defaults, auto-install a host agent on a timer or prove shutdown performance.
Research informs this design; the ready plan hands off independent slices now. Capture replacement alone
stays in its source-dependent sketch until the committed install-only/refresh interface is verified. Implementers build fenced
seams; reviewers check contracts and runtime QA establishes platform claims.
