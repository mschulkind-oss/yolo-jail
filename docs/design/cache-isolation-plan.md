---
title: "Sketch: trace private cache delivery and safe reclaim before building"
status: draft
stage: SKETCH
next: "Trace native cache paths and a gap-free admission/reclaim protocol; revise this sketch against those findings"
depends-on:
  - cache-isolation.md#OQ-CI1
  - cache-isolation.md#OQ-CI2
tags: [plan, storage, security]
---

# Sketch: trace private cache delivery and safe reclaim before building

**Status:** 2026-10-09. Incomplete and unstable while questions are open; source map
checked against `b2eeffc12`. No implementation, tests or native experiments performed.
**Design:** [ordinary cache isolation](cache-isolation.md).
**Research:** [trust and reclamation findings](../research/cache-trust-and-reclamation.md).

**Precedence:** the design wins on behavior; the current tree wins on facts; this sketch
is advice and the first thing to be wrong. It is **not an implementation hand-off**.
Paths below are investigation seams, not an authorized edit fence or a ticket wall.
Do not infer readiness from the existence of this map.

The scope-dependent entries wait on [OQ-CI1](cache-isolation.md#OQ-CI1);
capacity/retention claims wait on [OQ-CI2](cache-isolation.md#OQ-CI2).
Independent source preparation below can proceed while those rulings are pending.
No further owner question is needed for helper names, record encoding or traversal batches.

## Current source map

| Seam | What a future source-complete plan must connect |
| :--- | :--- |
| [`paths.go`](../../internal/paths/paths.go), [`naming.go`](../../internal/runtime/naming.go) | Derive a workspace-shaped private child in the one path package; verify full resolved identity, not only container-name hash. Preserve the frozen container-name contract. |
| [`statefile.go`](../../internal/paths/statefile.go), [`wsstatebeneath.go`](../../internal/cli/run/wsstatebeneath.go) | Reuse checked real-root and beneath-root operations. Trace the remaining interval from opened directory to runtime delivery; a prior Lstat alone is not proof against a swap. |
| [`run.go`](../../internal/cli/run/run.go#L1986-L2042), [`assemble.go`](../../internal/cli/run/assemble.go#L109-L120) | Actual ordinary selection supplies `cacheDir` explicitly; mount fallback changes alone cannot deliver isolation. Resolve once and pass that same backing into all readers. |
| [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go#L54-L172), [`homeskeleton.go`](../../internal/cli/run/homeskeleton.go) | Podman private source plus mountpoint under its read-only skeleton; Apple Container's nested source under whole writable home. Do not add one mount per vendor. |
| [`seal.go`](../../internal/cli/run/seal.go#L169-L181) | Preserve `sealedStores` and withheld crossings; ordinary scope selection must not widen a sealed build. |
| [`relocations.go`](../../internal/config/relocations.go), [`ensure.go`](../../internal/storage/ensure.go#L72-L141) | Fixed user-scope relocation grants stay; provisioning mountpoints must use actual selected backing rather than create stubs only in global cache. Do not automate relocation. |
| [`hostcasalias.go`](../../internal/cli/run/hostcasalias.go#L28-L98), [`hostcas.go`](../../internal/hostcas/hostcas.go) | Feed the selected private backing to stranded-copy/provisioning decisions; keep Pants membership, decline gates and disclosure unchanged. |
| [`commonEnvBlock`](../../internal/cli/run/assemble.go#L1198-L1230), [`shell.go`](../../internal/entrypoint/shell.go), [`shims.go`](../../internal/entrypoint/shims.go), [`capture.go`](../../internal/macosuser/capture.go#L1168) | Trace npm defaults and generated launcher stamp/receipt paths. Avoid treating `/tmp/mise-cache` or independent compile-cache state as the global `.cache`. |
| [`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go#L811-L886), [`runplan.go`](../../internal/macosuser/runplan.go), [`ctxlinks.go`](../../internal/macosuser/ctxlinks.go#L416-L440), [`seatbelt.go`](../../internal/macosuser/seatbelt.go) | Native per-session addressing must coexist with real `.cache` relocation checks and narrowly granted targets, without retargeting another live session's cache. |
| [`flock.go`](../../internal/cli/run/flock.go#L241-L250), [`probe.go`](../../internal/runtime/probe.go#L13-L24), [`macosusersessions.go`](../../internal/runtime/macosusersessions.go) | Trace launch/start/attach/teardown holders and unknown polarity. Existing launch-lock errors proceed with warnings; that courtesy is not reclaim admission protection. |
| [`inventory.go`](../../internal/cli/stores/inventory.go#L519-L584), [`stores.go`](../../internal/cli/stores/stores.go) | Discover admitted scopes after exit; report actual backing, liveness, partial size and coverage. Native account rows are currently separate/unreclaimed. |
| [`cachepurge.go`](../../internal/prune/cachepurge.go), [`prunecmd.go`](../../internal/prune/prunecmd.go#L1100-L1131) | Positive regenerable classification, interrupted traversal, fresh root/file checks and liveness fence for manual apply as well as housekeeping. |
| [`housekeeping.go`](../../internal/cli/run/housekeeping.go#L326-L382), [`offer.go`](../../internal/cli/run/offer.go) | Replace label-after-walk behavior with real interruption, keep offered consent, reach exited scopes, and leave failed/partial work unstamped. Native slot must reach native backing. |
| [`prune.go`](../../internal/prune/prune.go#L27-L46) | Exclude new private caches from both workspace/global cross-scope hardlink dedup, including applied candidate handling. |
| [`persistencemap.go`](../../internal/cli/run/persistencemap.go#L93-L162), [`briefing.go`](../../internal/jailcontent/briefing.go) | Describe actual scope and exceptions; do not make an attach's legacy source look private because the default changed. |

Host-owned durable cache-discovery records and their admission protocol are **new work**:
current runtime workspace discovery and native session records do not supply that whole lifecycle.
Their names/layout are not specified here. Refine them in the design before promotion,
including protection against an agent forging records under its writable `.yolo`.

## Source preparation available now

1. **Native addressing trace.** Follow generated bootstrap, session environment, npm defaults,
   launcher stamps, relocation delivery and Seatbelt permissions. Separate tools obeying a
   cache override from hardcoded `.cache` and `Library/Caches` consumers. Record gaps in
   [the design's backend contract](cache-isolation.md#backend-delivery-is-an-evidence-obligation),
   not as a third owner gate. Do not replace account `.cache` with a global workspace link.
2. **Fence trace.** Draw the interval from cache admission through starting, runtime/session
   handoff, attach and final exit, alongside manual and slot deletion. Identify holders for
   older launchers and failed starts. Existing image-housekeeping protection is a useful
   per-deletion pattern, not a cache-liveness guarantee. Close every gap before a build hand-off.
3. **Ownership/coverage trace.** Identify only host-admitted regenerable classes; existing
   forbidden names are a minimum exclusion, not a complete positive classification.
   Separate opaque/token/profile holds from sizing errors and unavailable backends.
4. **Budget trace.** Specify interruptible batches and cancellation across enumeration,
   size walking, candidate selection and apply. Cover directory enumeration/memory costs,
   not just file deletion. Preserve partial accounting through offer and retry.
5. **Identity trace.** Resolve path aliases and case-sensitive/case-insensitive fixtures;
   compare full records and opened directories without changing existing container names.
   A missing/moved workspace record remains an inspection clue, not a deletion grant.

These are smart-role investigations. A builder must not guess their answers from this sketch.
No cache inventory, account modification or native execution belongs to this docs stage.

## Tests lead the later implementation, not this document stage

Use source-shaped harmless fixtures. Existing tests below were **read, not run**.
Proposed cases are not tests added to the repository and have no claimed red/green result.

| Required control | Existing fixture / proposed extension |
| :--- | :--- |
| Production source selection | [`goldenOptions`](../../internal/cli/run/assemble_test.go#L59-L108) fixes platform/env seams; [`sealedLaunch`](../../internal/cli/run/seal_test.go#L343-L410) exercises real run wiring. Add ordinary two-workspace selection through the actual caller, not assembler fallback alone. |
| Warm restart and legacy attach | Extend run-path fixtures and then real container integration: sentinel survives fresh restart; attach names the inspected original source after a default/config change and creates no new backing. |
| Two active workspace poisoning/read isolation | Add an offline two-jail fixture: each uses the same guest lookup name with different backing; A changes lookup plus matching bytes, reads/deletes sentinels, and B still sees only its own data. Use no interactive agent or API. |
| Path and root races | [`jailwritable_test.go`](../../internal/prune/jailwritable_test.go#L108-L179) has planted/raced-link fixtures. Extend to new root/parent/record components and directory replacement; outside sentinels survive launch, inventory, purge and apply. |
| Fence and unknown status | Exercise live, paused, starting, failed enumeration, missing/unreadable records, multiple holders and launch-versus-removal races. Deleting launch registration or reclaimer recheck must break a control. |
| Real budget exit | Inject clock/cancellation into a large fixture; prove visitation stops before the tail, unknown bytes remain, apply stops too, and no completion stamp is written. Elapsed-time labeling alone must fail. |
| Opaque/credential holds | Old token/profile files inside a cache-shaped tree remain under dry-run/apply/automatic consent; unknown classes remain even in a known-inactive owned scope. |
| Sharing exceptions | [`cacherelocation_test.go`](../../integration/cacherelocation_test.go) proves actual relocated writes. Extend disclosure/selected backing without changing grants; [`hostcasalias_test.go`](../../internal/cli/run/hostcasalias_test.go) pins alias emitter and caller; keep npm/Go exclusion. |
| Dedup and sealed behavior | Same bytes in two scopes never become one writable inode. [`seal_test.go`](../../internal/cli/run/seal_test.go#L343-L410) retains withheld host aliases/relocations and private stores. |
| Native compatibility | [`darwinhomelayout_test.go`](../../internal/entrypoint/darwinhomelayout_test.go#L1018-L1079) pins real-root relocation refusal; [`relocations_test.go`](../../internal/macosuser/relocations_test.go) pins plan/profile/disclosure. Later native experiment must prove addressing, overlap and actual native reclaim. |

**Caller-deletion controls are essential:** remove the real scope selector, mount delivery,
starting registration, inventory discovery, manual prune or slot call in a disposable later
verification tree and show the relevant control fails. Source-text/comment assertions alone
cannot establish the feature. Preserve consent, heavy opt-in, legacy and outside-host sentinels.

## Safe rollout and promotion

Advice: prepare a complete vertical slice rather than landing private persistent allocation
without discovery/reclaim. After rulings and source gaps close, promote against the then-current
tree with exact failing fixtures, commands, fences and stop conditions.

1. New scopes start cold; no legacy copy, cross-scope hardlink dedup or global wipe.
2. Wire selection/admission/delivery and its controls together with host-owned discovery.
3. Wire inspection, manual/slot reclaim, native reclaim, consent and partial accounting together.
4. Preserve running legacy backing; attach reports its actual source. Classify legacy bytes
   before any eligible cleanup; do not let moved/deleted workspace records authorize removal.
5. Require distinct applicable verification for Linux rootless/rootful, Podman Machine,
   Apple Container and native macos-user. Nested Linux success is not native/ID-map proof.
6. Reconcile storage/persistence guidance only after behavior exists and evidence is recorded.

Existing guidance needing eventual reconciliation:
[storage ownership](../reference/storage-and-config.md#machine-wide-storage),
[jail sharing](../reference/jail-home.md#sharing-semantics),
[native home tiers](../reference/macos-user-home-tiers.md#the-layout-what-is-a-symlink-what-is-a-mirror),
[setup-specific guidance](../../userguide/reference/settings-per-setup.md),
[the purge-list comment](../../internal/prune/cachepurge.go#L10-L27).
They are **not changed** in this planning stage; do not rewrite current behavior as shipped.

## Promotion stops

Do not build from this sketch. A non-cosmetic disagreement goes back to the design;
missing symbols or someone else's dirty seam requires a fresh source map, not a patch-around.
No runtime/CLI/config change, new size knob, broad host grant or administrative cleanup is
licensed here. The two policy anchors remain the only owner calls; source preparation and
later native evidence obligations stay explicit rather than making all investigation wait.
