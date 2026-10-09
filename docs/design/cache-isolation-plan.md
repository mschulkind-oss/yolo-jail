---
title: "Sketch: trace private cache delivery and safe reclaim before building"
status: draft
stage: SKETCH
next: "Specify protected source delivery and a native quiescence witness; prepare offline controls while owner questions remain open"
depends-on:
  - cache-isolation.md#OQ-CI1
  - cache-isolation.md#OQ-CI2
tags: [plan, storage, security]
---

# Sketch: trace private cache delivery and safe reclaim before building

**Status:** 2026-10-09. Incomplete and unstable while questions are open; source map
checked against `b2eeffc12`. Follow-up traced env consumers, overlap holds and dispatch/slot
ordering; bounded standalone models exercised root replacement and crash schedules, not yolo.
No implementation, Go tests or native experiments performed.
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

Host-owned durable cache-discovery records and their admission protocol are **new work**.
Session records demonstrate pending-name publication and opened-inode comparisons, not durable
cache ownership or proof that guest writers ended. Authority belongs under host-only state,
never workspace `.yolo`, account `.cache`, a relocation manifest or a mounted lock directory.

## Production handoff and reclaimer wiring

The [conditional protocol](cache-isolation.md#the-launchreclaim-fence) separates a stable
admission mutex from attempt lifetime witnesses and durable dispatched state. Here is where
actual callers must cross it; changing a pure resolver alone is not the feature.

| Existing crossing | Required connection before later implementation |
| :--- | :--- |
| [`runContainer` selection](../../internal/cli/run/run.go#L1986-L2042) → `assembleInput` → [`both mount emitters`](../../internal/cli/run/assemble_parts.go#L54-L173) | Resolve/admit once; carry concrete backing identity and exceptions. Close the checked-handle → runtime-pathname reopen gap, then persist dispatched before submission. Sealed selection stays separate. |
| [`startKeeper` / relay](../../internal/cli/run/run.go#L2314-L2378) → [`awaitRunning`](../../internal/cli/run/keeper.go#L700-L720) | Hand off the cache attempt, not just the courtesy launch fd. The running frame fires even when its poll found no instance: keep starting/unknown until exact instance acknowledgment and settled submission. |
| [`keeper unwind`](../../internal/cli/run/keeper.go#L940-L952) and [`finish`](../../internal/cli/run/keeper.go#L908-L934) | Record inactive only after quiescence proof, not defer/exit status. Retain surviving or restartable containers and incomplete submissions. |
| [`attachExisting`](../../internal/cli/run/run.go#L2691-L2708) / session count | Inspect actual mount identity and join that hold before exec; no fresh allocation. Counting may warn/continue, so a separate mandatory cache admission is needed. Legacy/ambiguous sources retain their hold. |
| Native [`Run` dispatch](../../internal/cli/run/run.go#L1100-L1125) → [`RunMacosUser`](../../internal/macosuser/orchestrator.go#L872-L890) / [`bootstrap`](../../internal/macosuser/orchestrator.go#L1261-L1275) | Register before bootstrap/cache use, not `OnAgentStart`. Native slot already runs before backend setup. Account-home hold prevents ordinary different-workspace overlap but not a surviving guest after launcher death. |
| Native env/profile/generator chain | Per-session env file, bootstrap hydration and final profile must agree on one backing. Stamp paths need invocation-time addressing; shared launcher generation cannot bake a different session's cache path. Keep HOME and existing home-tier refusal. |
| [`inventory.cacheStores`](../../internal/cli/stores/inventory.go#L519-L584) and [`native rows`](../../internal/cli/stores/inventory.go#L1256-L1288) | Enumerate the host-owned admitted scopes after exit, plus separately labeled legacy/relocated/host exceptions. Native default must not remain an unreclaimed account row disguised as container coverage. |
| Manual [`RunPrune` cache call](../../internal/prune/prunecmd.go#L1100-L1131) and [`measureAndPurgeCache`](../../internal/cli/run/housekeeping.go#L326-L375) | Both consume the same admitted-scope engine, positive class definitions, no-follow roots and per-unit admission check. Existing relocation map conveys a grant, not automatic ownership; host alias remains held. |
| [`container slot`](../../internal/cli/run/run.go#L2367-L2377) / [`native slot`](../../internal/cli/run/housekeeping.go#L272-L281) | Discover exited scopes, retain the just-starting/live scope; use interrupted coverage and apply results, stamp only completed passes. Slot timing is not liveness evidence. |

## Remaining source preparation, not owner gates

1. **Protected delivery.** Existing `os.Root` handles survive root replacement but the emitted
   runtime source is a string. Determine a backend-supported protected reference or an anchor
   a sibling cannot replace; a post-open Lstat/dispatch pair is still racy. Keep sidecar
   placement recommended, not silently substituted with an arbitrary host root.
2. **Native addressing.** Prepare the env/profile candidate in
   [the design](cache-isolation.md#native-delivery-adjustment-under-investigation).
   Known npm/Go overrides and dynamic stamps can use existing crossings; arbitrary hardcoded
   paths cannot. Do not claim general native parity from this partial candidate.
3. **Quiescence.** Write the backend acknowledgment/settled-submission predicate; include a
   killed launcher, delayed submission and guest descendants still writing. No cache liveness
   is inferred from a free session/keeper lock, PID reuse or a successful proxy return.
4. **Positive classes and budgets.** Define disposable entry shapes, hold opaque/token/profile
   and multiply linked files, and check bounded directory batches through inventory and apply.
   Do not promote the current whole-bucket age list into a trusted classifier.

Standalone offline models can falsify a protocol or illustrate opened-root confinement, but
cannot establish production wiring, filesystem backend pinning or native process quiescence.
No cache/user inventory or account modification belongs to this stage.

## Tests lead the later implementation, not this document stage

Use source-shaped harmless fixtures. Existing tests below were **read, not run**.
Proposed cases are not tests added to the repository and have no claimed red/green result.

| Required control | Existing fixture / proposed extension |
| :--- | :--- |
| Production source selection | [`goldenOptions`](../../internal/cli/run/assemble_test.go#L59-L108) fixes platform/env seams; [`sealedLaunch`](../../internal/cli/run/seal_test.go#L343-L410) exercises real run wiring. Add ordinary two-workspace selection through the actual caller, not assembler fallback alone. |
| Warm restart and legacy attach | Extend run-path fixtures and then real container integration: sentinel survives fresh restart; attach names the inspected original source after a default/config change and creates no new backing. |
| Two active workspace poisoning/read isolation | Add an offline two-jail fixture: each uses the same guest lookup name with different backing; A changes lookup plus matching bytes, reads/deletes sentinels, and B still sees only its own data. Use no interactive agent or API. |
| Path and root races | [`jailwritable_test.go`](../../internal/prune/jailwritable_test.go#L108-L179) and [`record replacement`](../../internal/macosuser/sessionfiles_test.go#L369-L395) are starting fixtures. Add root/parent/record replacement before open, between open/lock and after open/before runtime submission; include in-root symlinks into held data and hardlink aliases. Outside sentinels survive launch/inventory/apply. |
| Fence and unknown status | [`sessionlock_test.go`](../../internal/cli/run/sessionlock_test.go), [`keeperpins2_test.go`](../../internal/cli/run/keeperpins2_test.go), [`accounthomehold_test.go`](../../internal/cli/run/accounthomehold_test.go) expose lifecycle seams. Add crash between dispatched/ack, empty running poll, delayed runtime creation, surviving native writer and multiple holders. Delete durable registration or manual/slot admission checks and show each control fails. |
| Real budget exit | Inject clock/cancellation into a large fixture; prove visitation stops before the tail, unknown bytes remain, apply stops too, and no completion stamp is written. Elapsed-time labeling alone must fail. |
| Opaque/credential holds | Old token/profile files inside a cache-shaped tree remain under dry-run/apply/automatic consent; unknown classes remain even in a known-inactive owned scope. |
| Sharing exceptions | [`cacherelocation_test.go`](../../integration/cacherelocation_test.go) proves actual relocated writes. Extend disclosure/selected backing without changing grants; [`hostcasalias_test.go`](../../internal/cli/run/hostcasalias_test.go) pins alias emitter and caller; keep npm/Go exclusion. |
| Dedup and sealed behavior | Same bytes in two scopes never become one writable inode. [`seal_test.go`](../../internal/cli/run/seal_test.go#L343-L410) retains withheld host aliases/relocations and private stores. |
| Native compatibility | [`envfile_test.go`](../../internal/macosuser/envfile_test.go), [`sessionfiles_test.go`](../../internal/macosuser/sessionfiles_test.go), [`real-root relocation refusal`](../../internal/entrypoint/darwinhomelayout_test.go#L1018-L1079) and [`relocations_test.go`](../../internal/macosuser/relocations_test.go) pin crossings. Add final cache env/profile/generator agreement and invocation-time stamps; deleting either env delivery or profile enforcement must fail a separate control. |

**Caller-deletion controls are essential:** remove the real scope selector, mount delivery,
starting registration, inventory discovery, manual prune or slot call in a disposable later
verification tree and show the relevant control fails. Source-text/comment assertions alone
cannot establish the feature. Preserve consent, heavy opt-in, legacy and outside-host sentinels.

## Independent native experiment owed

Later authorized work only, on a throwaway fixture and existing provisioned backend; no agent,
API, real credential/cache inventory, install or teardown. Linux models do not settle these:

1. Place distinct harmless legacy, selected A and selected B sentinels. Through the **actual**
   launch env and generated profile, observe npm's configured path, Go's default and explicit
   cache paths, an XDG-obeying fixture and direct `HOME/.cache` / native-path operations.
   Do not download packages or compile; path reporting and disposable read/write probes suffice.
2. Require private overrides/stamps to use selected backing and legacy content not to be read.
   Show hardcoded-path failure explicitly. If its only successful execution consumes legacy
   bytes, the candidate fails the native contract; do not label the backend honored.
3. Keep A live while attempting B. Preserve existing cross-workspace refusal and prove A's
   addresses/links stay unchanged. Two sessions of A need distinct env files and lifetime holds;
   per-launch scope would additionally require different roots and non-baked stamp addressing.
4. Exercise final env override attempts, link/root replacement and relocation target traversal
   under the actual profile. Old opaque bytes and explicit sharing targets remain untouched.
5. In a later **separately authorized** lifecycle fixture, interrupt the host while a harmless
   guest writer survives. Prove durable unknown retains the scope and denies re-point/reclaim;
   only validated end-of-all-writers/submission proof permits inactive. Merely freeing a lock
   is a negative control. No existing session-file cleanup is reused as that proof.
6. Verify native inventory/manual/slot reach the same backing after safe exit, skip live and
   unknown scopes, stop at the traversal budget and preserve opaque/credential-shaped fixtures.

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
