---
title: "Ordinary jail caches must not be a writable free-for-all"
status: in-review
stage: DESIGN
next: "Resolve protected runtime delivery and native consumer quiescence while OQ-CI1 and OQ-CI2 await rulings"
tags: [design, storage, security]
---

# Ordinary jail caches must not be a writable free-for-all

**Status:** 2026-10-09. Nothing built for this proposal; current-source findings checked against `b2eeffc12`.

> **In short.** An agent's environment should keep ordinary cache writes within its workspace's trust, with reclamation beside isolation rather than postponed.

**Why it matters.** Today's shared writable cache lets an unrelated jail choose bytes another consumes or inspect its cached data.
**The shape.** One private persistent backing per workspace, host-owned discovery and fenced reclamation; sharing is a named exception.
**Cost.** A cold first launch per workspace and duplicated downloads; same-workspace sessions remain mutually trusted.
**Start at [§3](#3-the-recommended-scope-and-identity)** — the recommended default, not an owner-selected mechanism.
**Needs your ruling:** [OQ-CI1](#OQ-CI1), [OQ-CI2](#OQ-CI2).
**Reads with:** [research](../research/cache-trust-and-reclamation.md) (source findings), [companion sketch](cache-isolation-plan.md) (incomplete source map, not a build hand-off).

---

## 1. Direction and boundaries

The maintainer's direction is **not a writable cache free-for-all**.
That rules out the current ordinary default; it does not select workspace scope,
per-launch scope, a filesystem quota or a new configuration key.
I recommend workspace scope because sessions of one workspace already share writable source
and home state. [OQ-CI1](#OQ-CI1) keeps that mechanism choice with the owner.

Proposed constraints, whichever scope is selected:

- **P1. Private by default.** No ordinary launch receives the global cache read-write
  merely because it is a jail. Another workspace is neither a trusted writer nor a cache reader.
- **P2. One resolved backing.** Admission, delivery, attach, inspection and reclaim agree
  on the directory actually in use; they do not independently reconstruct a default.
- **P3. Reclaim only known disposable bytes.** Positive ownership, known regenerability,
  known inactivity and consent are all required. A cache-shaped pathname is not enough.
- **P4. No safety inferred from age or locks.** Age is a retention rule, not liveness;
  a host lock which tools never take cannot authenticate cache bytes or protect live writers.
- **P5. Ship the lifecycle together.** Persistent private storage needs discovery,
  bounded inspection, safe reclamation and truthful migration reporting on delivery day.

### What this does not isolate

This is **ordinary `.cache` isolation**, not all writable state isolation.
It does not split `/mise`, Rust/Cargo stores, selected machine-scope pack state,
credentials, the host environment, or macos-user's shared guest identity.
The existing [state-separation contract](../reference/jail-state-separation-design.md)
and [native-account limitations](../reference/macos-user-home-tiers.md#seatbelt-does-the-read-only-half-of-a-bind-and-the-launcher-does-the-other)
remain. Same-workspace untrusted sessions require a stronger scope choice.

No new arbitrary writable host root, cache quota/size knob, forced active eviction,
account-wide cleanup, credential reset or new administrative privilege is proposed.
[Bulk scratch](storage-tiers.md), [shared-tool relocation](shared-tool-store-relocation.md)
and [VM-local volumes](vm-local-volumes.md) retain their independent rulings.

## 2. What exists, and what is missing

[The research](../research/cache-trust-and-reclamation.md#the-actual-source-selection-not-just-a-fallback)
checks both the production selector and mount emitters: ordinary Podman and Apple Container
bind the machine cache writable at `/home/agent/.cache`. It is yolo-owned jail storage,
not the host user's normal cache. Native macos-user instead uses its account home's `.cache`.
Sealed builds already get private cache and mise directories; their stronger contract stays.

A digest check verifies bytes against a digest, **not who chose the digest**.
The npm URL index and Go action index let the writer select both lookup and matching bytes;
[the source's exclusion evidence](../../internal/hostcas/hostcas.go#L159-L209)
therefore applies between hostile sibling jails as well as between jail and host.
Do not mistake “safe concurrent writers” for “safe adversarial writers”.

The current age purge covers named buckets, skips links and excludes profile names.
Its cache walk's nominal time budget labels elapsed time **after** the walk finishes;
its housekeeping lock is not a live-cache fence. Native account caches have no such reclaimer.
See [reclamation findings](../research/cache-trust-and-reclamation.md#reclamation-exists-but-its-safety-and-coverage-are-narrower).
These are gaps to close, not proof that private backing already works end to end.

## 3. The recommended scope and identity

**Workspace-private persistent backing** means the same workspace reuses its cache
across fresh jail launches; a distinct workspace receives distinct backing.
“Private” here is from other ordinary workspace jails, not from the host owner,
other sessions of that workspace, or explicitly shared exceptions.

The identity is the **exact resolved launching workspace**, not a basename, Git remote,
branch, repository content or the constant guest path `/workspace`.
Use the launcher's resolved absolute path as the recorded identity, preserving its existing
container naming contract ([current resolver](../../internal/runtime/naming.go#L24-L80)).
A shortened hash may name a directory but cannot authorize it: records must match the full
identity and opened directory. Case aliases naming the same physical workspace must not
create different writable scopes; reconciling that with current naming is source work,
not permission to change container names here.

### Where the backing may live

Use a **source-shaped root**: storage yolo derives for this workspace from its established
workspace state, exposing only its cache child, not a user-supplied ambient host directory.
The recommended placement is beneath `<workspace>/.yolo/`, outside durable scratch and
installed-program trees eligible for dedup. Exact child spelling and backend decomposition
are engineering choices; they cannot alter the scope or safety behavior.

- Restart at the same resolved workspace reuses the backing. Two concurrent launches of that
  workspace share it intentionally and join the same admission/liveness protection.
- Separate clones and worktrees have separate scopes even with identical repository contents.
- A moved workspace has a new resolved identity. Do not adopt an old cache solely because
  its sidecar moved along, its basename matches or a record points at the new location.
  Report the mismatch; preserve prior bytes and admit a fresh empty child.
- Workspace deletion leaves a record for inspection, not authority to delete whatever later
  occupies the path. Missing/unreadable/changed backing is unknown, never an empty cache.
- A launch with no cache content creates an empty private backing; an empty selection of packs
  does not restore the global default. A read-only workspace grant does not make cache read-only.

## 4. Admission, delivery and attach

The host launcher is the sole writer of scope ownership and admission records, held outside
all jail-writable mounts. The agent/tool writes cache contents, never ownership authority.
Inventory reads; the host reclaimer deletes only what its independent rules admit.
Records name the full workspace identity, concrete backing identity, backend and lifecycle
state. Their serialization is the implementer's choice, not a public schema.

1. Resolve the workspace and selected scope once; validate allowed roots **before creating**.
2. Enter the host launch/reclaim fence, verify full identity, and record **starting** before
   any consumer can use the backing. Create only absent managed directories, never import data.
3. Refuse links at agent-replaceable managed components; open beneath checked real directories
   and preserve their identity through delivery. A changed root is a refusal, not a new grant.
   The existing checked-root helpers protect operations through their handles, **not a later
   runtime reopen by pathname**. Before implementation, prove a protected source reference for
   each backend or a stable anchor no sibling can replace during that interval. A final Lstat
   followed by argv dispatch does not close it. Do not silently relocate backing outside the
   recommended sidecar to evade this gap; any placement adjustment returns to this design.
4. Deliver only that scope's backing. On successful start, hand protection to backend liveness
   without a gap; on failure retain a starting/unknown hold until absence is proved.
5. After exit, keep the cache warm. Only a later fenced, known-inactive pass may reclaim bytes.

| Failure | Proposed disposition and next step |
| :--- | :--- |
| Unsafe root, forged/mismatched ownership, or link/race at a managed component | Refuse before delivery; name the offending component and ask the owner to move it aside and retry. Follow or delete no target. |
| Private backing cannot be created, addressed or protected | Refuse; name the filesystem/permission problem and the host-side correction. Never fall back to the machine cache or another disk. |
| Runtime/start status cannot be established | Retain the scope as unknown; inspect runtime/session health before trying reclaim again. |
| Disk full or I/O error during a session | Writes fail normally; no spill into global cache and no retry loop. Name inspection/free-space recovery, not forced active eviction. |
| Inventory or housekeeping fails | Preserve data and launch outcome; record incomplete coverage and offer inspection/retry, never a fake zero or successful full pass. |

**Attach uses the running jail's actual backing**, including a legacy global cache.
It allocates and migrates nothing. A newer default is not retroactive to existing mounts.
The disclosure and persistence briefing name legacy sharing and the fresh-launch remedy;
changing configuration is not evidence that a running jail changed source.
Native macos-user has no container attach; an additional native session must receive its
own resolved addressing and protection rather than inherit whichever global link was last written.

### Backend delivery is an evidence obligation

| Setup | Required behavior, not yet proven |
| :--- | :--- |
| Linux Podman, rootless | Private source visible/writable through the actual ID mapping; inventory and reclaim work without recursively changing ownership. A nested jail cannot prove the rootless cell. |
| Linux Podman, rootful | Same isolation contract; root-owned bytes may need existing privileged runtime access. Unreadable/unremovable bytes remain unknown/failed, not silently reclaimed; no new broad `sudo` deletion authority. |
| Podman Machine on macOS | Only the workspace's cache backing is shared into the VM. Restart, mapping and host reclaim must be verified natively, not inferred from Linux argv. |
| Apple Container | Keep the whole writable workspace home and one nested private cache source compatible with its device limit. VM-local placement is still [OQ-VL2](vm-local-volumes.md#OQ-VL2), not chosen here. |
| Native macos-user | Per-session addressing and Seatbelt permissions must not redirect a live sibling. Current `.cache` is a real directory, required by relocations; replacing it with one global workspace link is not an adequate design. |

### Native delivery adjustment under investigation

Native `HOME` stays fixed under the [one-home ruling](../reference/macos-user-home-tiers.md#oq-ht4).
The [source trace](../research/cache-trust-and-reclamation.md#native-addressing-compatible-crossings-incomplete-isolation)
finds useful session env/profile crossings, but **no existing complete private-cache primitive**.
Prepare the following cache-only candidate, conditional on scope and evidence, not a selected mechanism:

- Hand one admitted absolute backing to bootstrap generators, provisioning, guest daemons and
  agent env. Set XDG-aware consumers there, npm to its `npm` child, and native Go via explicit
  `GOCACHE` to `go-build`: XDG alone does not move Darwin Go. Check final env after composition;
  do not allow a dotenv override to become an ambient host grant.
- Change yolo's stamp lookup to the session's selected cache at invocation, not a baked
  `HOME/.cache` or a path another session's shared launcher generation overwrites.
  Keep install prefixes, receipts, mise and credentials in their existing tiers.
- Profile the **physical** selected backing and its protected anchors; prevent reads/writes
  of retained legacy `.cache` through the broad home allow, preserving only explicitly
  granted relocation targets. Test traversal and resolved-link behavior, not profile text.
  Deny Go's old native `Library/Caches/go-build` fallback as well as setting `GOCACHE`.
  Keep the account `.cache` real and relocation writes root-confined; no global root link.
- `Library/Caches` is not covered by current relocation. Explicit Go redirection is a named
  consumer adjustment, not privatization or deletion of that whole directory. Unknown native
  consumers there remain a support gap; old bytes must not silently become a private fallback.

This candidate redirects **known env-obeying consumers only**. Hardcoded `HOME/.cache`
consumers are denied rather than magically redirected; a native experiment must show the
failure and next step, not call it parity. Fresh native support under the selected contract
is not ready until this incompatibility and actual writer quiescence are resolved; otherwise
refuse the unsupported launch with the container-backend/retry remedy, never a global fallback.

Preserve the current different-workspace account-home refusal. Same-workspace overlapping
sessions still need independent env and lifetime holds; a per-launch choice would also need
independent cache addresses despite shared launcher files. A shared-home link cannot name both.
Host-lock release on SIGKILL and unseen other-host-user sessions are unknown, not safe overlap.
No all-home/auth migration, privilege consent or third owner gate is added by this preparation.

## 5. Discovery and bounded inspection

Persistent scopes must remain discoverable after their containers exit.
A durable **host-owned allowlist of admitted backings**, not scanning arbitrary workspace
records, supplies inspection candidates. Its directory and immutable identity records are not
mounted into any jail. Refresh liveness separately; a record's “inactive” field is not proof.
No marker written under `.cache` or `.yolo` can create ownership or deletion authority.

Every inventory row reports:

- Workspace scope and actual backing; selected, legacy, relocated or host-owned exception.
- Known size or a partial lower bound; unreadable/unvisited bytes explicitly **unknown**.
- Covered regenerable classes, opaque/profile/credential holds and age rule.
- Live, starting, inactive or unknown state; the reclaimer/consent required and next action.

Enumeration, sizing and candidate selection share a **real interruptible work budget**.
Check cancellation and elapsed budget during traversal and before each destructive unit,
not after walking every file. Metadata work at launch stays small; full inventory runs
on demand or in housekeeping, not before startup.
Reuse the existing cache-walk budget as an initial engineering bound, not a capacity cap;
choose bounded traversal batches during source refinement, with an exit test at the actual caller.
An uninterruptible filesystem call is a reported limitation, not a hard wall-time promise.

No completion stamp on interruption or failure. Partial results may support an explicitly
partial offer, never a claim of complete coverage; the next eligible pass retries.
Report logical bytes separately from actual reclaimed bytes and filesystem capacity,
without double-counting backings or claiming external-drive deletions free the state disk.
Inspection must not read content through links or reveal held credential contents.

## 6. Reclamation is part of isolation

**Regenerable** here means an admitted class yolo can discard and the tool can rebuild or
re-fetch without losing unique user state; it does not mean authenticated or cheap.
Ownership, regular-file shape, old mtime and a familiar directory name are insufficient alone.
Class definitions come from trusted host-side code/declarations, not an agent-editable manifest.
Keep browser profiles, cookies, tokens, installed programs and unclassified vendor data held.
The existing whole-bucket age list is not that positive classifier: define disposable entry
shapes per supported tool/version before admitting them, leave unknown shapes held, and skip
multiply linked files whose other writer/ownership cannot be established. Walk every managed
component without following links, including links staying inside the cache into opaque data.
No recursive whole-scope removal while opaque bytes remain.

### The launch/reclaim fence

**Conditional engineering protocol:** one mandatory per-backing admission mutex, stable and
never unlinked/renamed, plus separate immutable attempt identities and durable lifecycle updates.
All authority lives in host-only state outside the workspace/account home/cache and every
jail-writable mount. A shortened name, guest manifest or free lifetime lock grants nothing.
No shared-to-exclusive flock upgrade: admission and lifetime evidence are different objects.

| Transition under admission protection | Required evidence / result |
| :--- | :--- |
| Absent → prepared | Host exclusively creates the scope/attempt record, validates full workspace and opened backing identity, and holds a lifetime witness before publishing. No writer dispatched yet; atomic publication failure refuses launch. |
| Prepared → starting | Persist **dispatched** before the first possible cache writer: container runtime submission or native bootstrap/provision/daemon, not agent start. Record-write failure dispatches nothing. |
| Starting → live | Record a backend instance identity and settled submission with no unprotected interval. Keep the starting hold until that acknowledgment; a progress frame or successful client return alone is insufficient. |
| Live → inactive | All holders ended, no pending submission/restart capability remains, and the exact backend instance is known quiescent. Any remaining container referencing the source, even stopped/paused, retains it. Native launcher exit needs proof its bootstrap, guest daemons and descendants cannot continue writing. |
| Any dispatched attempt → unknown | Lost launcher/keeper, failed handoff, unreadable record, unmatched instance or failed enumeration persists a hold. A free flock, elapsed age or currently absent container does not settle a delayed submission. No timeout clears it. |
| Prepared → inactive without dispatch | Under the same mutex, prove no writer was submitted. An incomplete/missing dispatch record is unknown, not evidence of this transition. |

After a crash, reconcile from the durable attempt state plus backend proof; never synthesize
inactive from an empty lock count. Legacy/unregistered consumers also retain their ambiguous
backing. Native quiescence after host death remains an **unproved engineering obligation**;
existing account-home/session locks count host launchers, not every surviving guest writer.

Every manual/slot deletion takes the admission mutex non-blocking, revalidates host ownership,
opens the exact recorded root without links, and queries all holders. Only known-inactive,
positively classified units with current consent proceed. Bind measurement candidates to
opened root/file identities; under protection recheck no-follow parents, class, identity and
age before a descriptor-relative unlink. Busy/error/identity change means retain and retry.
Delete a bounded unit then release: a new admission publishes starting before the next unit
can proceed. Detached pending pathnames never bypass these checks. Interruption/failure leaves
partial accounting unstamped, not a fresh inactive declaration.

This excludes **host admissions**, not npm/Go writers or arbitrary host-owner actions. Guest
writers need not take a host lock; their whole reachable lifetime must be covered by evidence.
[The companion](cache-isolation-plan.md#production-handoff-and-reclaimer-wiring) maps the actual
keeper/native/manual/slot crossings. This is not the current courtesy launch lock's protocol.

### Consent, reach and the outstanding capacity decision

Preserve [OQ-BF1/BF2](disk-levers-and-backfill.md#111-decision-ledger)'s **2026-09-08**
offered policy: known default cache classes age out after 30 days; a standing yes permits
automatic housekeeping, not-now defers, never stops asking, and heavy re-fetch stays opt-in.
Make inspection, offers, manual prune and the post-launch slot cover the newly admitted
workspace scopes, including exited workspaces and the native backend's actual backing.
The native reclaimer is owed with isolation, not “container cleanup also runs on a Mac”.
Leave host-owned exceptions and opaque legacy data out of automatic deletion.

[OQ-DF4](minimal-disk-footprint.md#OQ-DF4) was ruled **2026-10-05**: policy, **no budget key**,
and cleanup of unused shared mise versions through the offered tier. Its scope was measured
residual machine storage, narrowed to mise; it did not choose a strict cap for multiplied
private cache working sets. [OQ-CI2](#OQ-CI2) asks whether that policy still suffices here.
**Age/consent cannot guarantee size:** frequently written, live, unknown or opaque bytes can
grow indefinitely, and consent may be declined. No ceiling or chosen number is smuggled into this design.

## 7. Migration and sharing exceptions

New ordinary scopes **start cold**. Do not copy the global tree blindly, share its writable
indexes, hardlink duplicates across private scopes, wipe it globally or drop credentials.
Old live jails retain their mounts/inodes; preserve their sources and disclose legacy sharing.
An inactive legacy tree is still heterogeneous: inventory first, classify positively,
then use the unchanged consent/liveness rules for eligible bytes only.
Rollback does not erase private or legacy trees; it may restore older sharing behavior,
which must be disclosed rather than advertised as a security-preserving downgrade.

Explicit `cache_relocations` retain their user-scope authority and may intentionally share
a target between workspaces. Inspect/disclose each exception's target and sharing, without
expanding its grant, converting it into ownership or creating a host-side reflection.
Apple Container's warning/skip remains until separately supported; native links retain
their admitted target and real-root safety contract while addressing is reconciled.
The held [OQ-CR1](../plans/cache-relocation.md#OQ-CR1),
[OQ-CR2](../plans/cache-relocation.md#OQ-CR2) and
[OQ-CR3](../plans/cache-relocation.md#OQ-CR3) remain unchanged.

[Pants `lmdb_store`](../research/cache-trust-and-reclamation.md#why-content-addressed-does-not-settle-the-trust-question)
remains the existing narrow writable host exception with all gates and disclosure.
It is not yolo's to reclaim. Its hidden private copy must be attributed to the actual scope,
not the old global default. npm and Go gain no equivalent sharing exception.

## 8. Costs, alternatives and observable completion

| Alternative | Verdict and cost |
| :--- | :--- |
| Global writable default with locks | **Rejected:** locks coordinate writers, not their authority; unrelated readers/writers remain. |
| Workspace-private persistent default | **Recommended, awaiting [OQ-CI1](#OQ-CI1):** warm restart, cold new workspace, duplicated working sets; trusts same-workspace sessions. |
| Strict per-launch private default | **Viable owner alternative:** stronger session separation, repeat cold downloads and more teardown/residue work. Warm-restart requirement would change. |
| Trusted admitted read-only blobs with private indexes/locks | **Future only:** expected digests must come from trusted resolution and consumers must support separated storage. Mounting a complete npm/Go cache read-only is not that design. |
| Fetch broker before isolation | **Rejected as prerequisite:** adds a service/protocol while leaving the ordinary lifecycle unresolved. |

| Risk | Mitigation / remaining cost |
| :--- | :--- |
| Duplication fills disk faster | Inventory and safe reclaim ship together; [OQ-CI2](#OQ-CI2) owns the guarantee, not a guessed cap. |
| Forged records or symlink races widen host deletion | Host-only allowlist, exact identity, opened-root checks and beneath-root operations; unknown retains. |
| A hidden native/global path defeats isolation | Source trace plus native experiments; no parity claim from static success. |
| Reclaim interrupts active use | Fence admission and all reclaimer callers; retain active/starting/unknown scopes. |
| Explicit sharing survives | Name exceptions and limits; do not promise all-state or credential isolation. |

Completion must demonstrate, using harmless offline fixtures rather than live agents:

- **Two active workspaces:** distinct backings; A's planted lookup/content, read sentinel and
  deletion attempts do not alter or reveal B's private cache. Both continue running.
- **Warm lifecycle:** same-workspace restart retains a sentinel; attach reads the actual backing;
  a legacy attach remains honestly legacy. Sealed builds retain their stronger exclusions.
- **Adversarial paths:** planted and raced links at roots, parents, descendants or ownership
  records neither grant host access nor cause host-file deletion. Cross-scope hardlinks are absent.
- **Real callers:** deleting production selection, fence registration, inventory or prune/slot
  wiring makes its control fail; testing only a resolver or a comment is insufficient.
- **Reclaim:** inactive eligible bytes can go with consent; live/starting/unknown, opaque and
  credential fixtures stay. Race a new launch against deletion, abort a walk at its budget,
  and retry interruption without overstating coverage or completed work.
- **Native/backend gates:** rootless and rootful Linux, Podman Machine, Apple Container and
  macos-user provide separate applicable evidence. Native path overrides, permissions,
  concurrent sessions and the actual native reclaimer must run natively before claims of support.

## 9. Open questions

1. 💬 **OQ-CI1: Is the ordinary default workspace-private persistent, or private per launch?**

   [The scope comparison](#8-costs-alternatives-and-observable-completion) trades warm
   restart and fewer duplicate downloads against separation of same-workspace sessions.

   - **A — Workspace-private persistent.** Trusts sessions already sharing source/home;
     restart stays warm, and each new workspace starts cold.
   - **B — Private per launch.** Separates sessions further, but repeats downloads and
     changes the warm-restart contract; revise the body before a build hand-off.

   <!-- vantage: question id=OQ-CI1 leaning="A — workspace-private persistent matches the existing same-workspace trust boundary and keeps restarts warm; unrelated workspaces must not share writable cache by default." -->

   _Leaning:_ A — workspace-private persistent matches the existing same-workspace trust boundary and keeps restarts warm; unrelated workspaces must not share writable cache by default.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-CI2: Does offered age cleanup suffice for private caches, or must storage have a strict capacity guarantee?**

   [The prior policy and its limits](#consent-reach-and-the-outstanding-capacity-decision)
   remain binding unless deliberately revised for duplicated private working sets.

   - **A — Keep policy, no budget key.** Inspect and reclaim inactive known caches;
     hot, opaque, unknown or non-consented bytes can still grow.
   - **B — Require a strict capacity guarantee.** Revisit the 2026-10-05
     [OQ-DF4](minimal-disk-footprint.md#OQ-DF4) ruling explicitly; guarantee/failure behavior
     needs a follow-up design, not a chosen cap here.

   <!-- vantage: question id=OQ-CI2 leaning="A — preserve the ruled no-budget-key policy while delivering honest coverage and inactive-cache reclamation; a strict guarantee needs an explicit revision, not a claim that age limits bytes." -->

   _Leaning:_ A — preserve the ruled no-budget-key policy while delivering honest coverage and inactive-cache reclamation; a strict guarantee needs an explicit revision, not a claim that age limits bytes.

   **Answer:**

   > _(empty — fill in when decided)_

## 10. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| CI-DIR1 | Maintainer direction: ordinary cache writes must not be a free-for-all. This is a principle, not selection of workspace/per-launch scope or a capacity mechanism. | 2026-10-09 | [Direction and boundaries](#1-direction-and-boundaries) | — |
