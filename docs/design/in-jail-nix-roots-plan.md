---
title: "Implementation sketch: synchronized workspace Nix retention"
date: 2026-10-07
status: draft
stage: SKETCH
next: "Prove the producer hook and managed-root lifecycle in isolated fixtures before promoting this sketch"
depends-on: [in-jail-nix-roots.md]
tags: [nix, implementation-plan, storage]
---

# Implementation sketch: synchronized workspace Nix retention

**Status:** 2026-10-07. Incomplete; not a build authorization. Automatic direction and lifecycle/cap/
inspection/release are owner-settled; mechanism reliability and default refinement are agent work,
not a request to answer NR1 again. Source map checked at
`931489400b4ce7334a0731a47a47c116f6e44ab2`; upstream Nix source checked at the design's pinned revision.
**Design:** [in-jail Nix roots](in-jail-nix-roots.md).
Precedence: design wins on behavior, tree wins on fact, this sketch is advice and the first to rot.

## Map and reuse

| Existing seam | Constraint / next investigation |
| :--- | :--- |
| [`internal/nixroots/hostmap.go`](../../internal/nixroots/hostmap.go) | Reuse `HostMap.Translate`/`Compose` after actual parent classification; lexical cleaning is not race confinement |
| [`register.go`](../../internal/nixroots/register.go), [`daemon.go`](../../internal/nixroots/daemon.go) | `Registrar.Root` rewrites owned links; `AddIndirectRoot` closes its connection and sends no temporary pin. Add separate preservation-only intent admission; do not observe through `Root` |
| [`nixrootstest/daemon.go`](../../internal/nixroots/nixrootstest/daemon.go) | Extend fake protocol fixture for operation ordering/errors, not as GC semantics proof |
| [`translatedroots.go`](../../internal/cli/run/translatedroots.go) | Map/owned-consumer seam only; preserve seal, masks and no-map behavior |
| [`flake.nix`](../../flake.nix) | Investigate maintained client patch/build delivery; no Nix bump/patch is authorized by the sketch |
| [`prepare.go`](../../internal/cli/run/prepare.go), [`briefing.go`](../../internal/jailcontent/briefing.go) | Warning remains until supported automatic coverage actually ships; rewrite caller tests only then |
| [`internal/cli/stores/inventory.go`](../../internal/cli/stores/inventory.go), [`internal/prune`](../../internal/prune) | Inspection/host housekeeping candidates; enumerate only known workspaces, never recursively discover host homes |
| New workspace-root coordinator/ledger, host-owned workspace index and hidden admission/release surface | Proposed ownership split only; no path/name is an existing API. Validate a mapped writable durable destination before admitting; the host index must preserve stopped-workspace identity |

Current `durableStores` uses `prune.FindYoloWorkspaces`, which sees only containers the runtime still
lists; stopped `--rm` workspaces disappear. The proposed host-owned index must be written by fresh
host launch, validated without following jail-controlled `.yolo`/ancestor links, and reconciled during
host inspection/cleanup. This is new state, not an existing durable workspace registry.

## Producer hook: prove before selecting

Pinned upstream [`IndirectRootStore::addPermRoot`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/indirect-root-store.cc#L20-L44)
is the narrow candidate: `addTempRoot` → local symlink → `addIndirectRoot` → return. A maintained
client hook can synchronously invoke an admission helper here while the producer's protection
remains held. The helper must explicitly pin/validate, register the managed root, **reassert the pin
after registration acknowledgment**, then acknowledge and close. A pre-registration pin alone can
predate a GC whose permanent snapshot missed this link; post-registration repinning fences that GC.
Keep standard user links unchanged; the helper creates only managed links.
[`createGeneration`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/profiles.cc#L61-L95)
uses that boundary for direct generation leaves, not the mutable profile alias.

**Required investigation:** inspect every advertised client entry point at the supported version,
including direnv/registry cache; enumerate those bypassing this hook. Establish legacy/batch temp-pin
compatibility explicitly. A helper launched after child exit is not equivalent. Reject a
collected/invalid target; `AddIndirectRoot` success alone proves neither validity nor host correspondence.
Any new worker operations need bounded framing/cancellation and a compatible version matrix.

## Lifecycle mechanics to prove

Advice: use a workspace-scoped lock and transactional pending/admitted/releasing records so crash
recovery can conservatively account for roots created before acknowledgment. Stable storage encoding
is the implementer's choice; behavior is [the design's](in-jail-nix-roots.md#41-workspace-ownership-caps-and-release).

- Reserve cap bytes/record slots before creating roots; duplicate target/intent does not double-charge.
- Hold explicit operation pins across root admission/retarget; one pin connection's lifetime cannot
  be treated as another operation's liveness. Reconnection requires overlap, not a GC gap.
- Revalidate source parent/leaf identities and target; never rewrite an observed P over a newer Q.
- Persist a pending reservation before registering its managed root. Unknown partial transactions
  retain/account conservatively; recovery never calls them successfully released merely from an empty answer.
- Release only exact validated managed symlinks/records. Garbage collection is Nix's later action,
  not a cleanup implementation step. Runtime unknown fences existing roots and blocks new growth.
- The user-wide threshold is advisory inventory, not an enforceable aggregate cap. Adding a shared
  admission directory/service to turn it into a quota needs a separate reviewed contract.

## Bounded build contracts after the gates

1. **Luna NR-A — pin/registration API:** tests first against fake daemon; observed red before implementation.
   Cover pre-pin/validity/register/post-pin-fence/ack/release ordering, version failures, timeout/cancel, no source writes,
   masks/aliases/ancestor changes. Focused gate: `go test ./internal/nixroots/...`.
2. **Luna NR-B — workspace lifecycle/accounting:** synthetic closure graphs and injected clock/liveness;
   duplicates/overlaps, shared workspaces, oversized/unknown sizes, passive expiry/LRU, active veto,
   cap reduction, pending crashes, restart/stopped/missing workspace and explicit release. New package gate
   is named only after its location is selected; do not assign this slice concurrently with NR-A shared files.
3. **Producer proof — isolated Nix only:** independently authorize a wholly disposable local store/state/
   daemon with connection paths verified before GC. Hold collection after permanent-root scan; compare
   late indirect registration, pre-pin-only handoff and the post-registration repin fence. No shared-store query/GC substitute.
4. **Launch/inspection integration:** after mechanism/default engineering validation and isolated proof, wire actual
   supported-client calls plus workspace inspection/release and host lifecycle reconciliation. Mutate
   production caller, post-registration pin fence, acknowledgment ordering, cap check and active veto separately; retain reds/patches/
   restored greens. Parent owns build/nested/full landing gates and real rootless host acceptance.

## Ships with, and stops

- Source/register API tests and real producer-path tests, not only helper greens. Fixture tests must
  fail when production admission is deleted; a text-order test is not dynamic lifecycle proof.
- Inspection reports union/shared bytes and fenced state without promising physical reclamation.
  Each failed admission/release supplies the exact next verification or profile/root action.
- If option C is later selected: separately bound watch/queue/scan load, report unrecoverable event loss,
  exclude feedback and keep the owner-ruled absence of a new routine launch line.
- Reconcile [setup gaps](../plans/setup-support-gaps.md), [briefing reference](../reference/agent-briefings.md)
  and [image retention](../reference/image-retention.md) only for the actual delivered scope. No migration
  of owned package/image roots or unmanaged original links through an opportunistic scan.
- **Stop:** incomplete mechanism/default engineering validation, failed version/path synchronization
  proof, or any need for a new host service/mount. Settled [NR1/NR2](in-jail-nix-roots.md#decision-ledger)
  are not blockers awaiting another owner reply. Allowed read-only host-auto exposure alone supplies
  neither synchronization proof nor implementation permission for this documentation task.
