---
title: "In-jail Nix roots need a synchronized handoff and bounded workspace retention"
date: 2026-09-28
status: in-review
tags: [nix, gc-roots, podman, mount-namespaces, storage, design]
summary: "Host-path translation fixes the namespace mismatch, not asynchronous discovery or retention. Automatic user roots need producer synchronization, workspace accounting, finite retention and explicit release."
stage: DESIGN
next: "Agent investigation: resolve discovery loss and concurrent-GC safety, then specify bounded lifecycle, cap and cleanup defaults before a build-ready design"
vantage:
  status-chip: true
---

# In-jail Nix roots need a synchronized handoff and bounded workspace retention

**Status:** 2026-10-07. The map and yolo-owned consumers are built; automatic user-root protection
is not. Owner replies settle automatic protection with bounded lifecycle/cap/inspection/release
([OQ-NR1](#decision-ledger)) and read-only host-auto visibility without a new routine launch line
([OQ-NR2](#decision-ledger)). Reliable mechanism and lifecycle/default refinement remain agent work,
not reopened owner questions. Source audit withdraws the observer's completeness claim; historical
measurements remain in [§3](#3-measured-in-this-jail) and [§8](#8-what-is-built), not concurrent-GC proof.

> **In short.** Translation fixes which path the host roots. Safe automatic retention also needs
> an acknowledged producer handoff and workspace-owned roots that yolo can inspect and release.

**Why it matters.** Durable result/profile links can lose dependencies between commands; retaining
every old link indefinitely would instead let forgotten workspaces pin host disk.

**The shape.** Supported producers hand root intent to a bounded workspace ledger; managed links
hold admitted targets, while active-use pins guard registration and release.

**Cost.** Client integration and lifecycle accounting; an async observer alone cannot deliver the promise.

**Start at [§1](#1-verdict)** — the settled direction and remaining engineering investigation.

**Needs your ruling:** None. Mechanism and lifecycle/default refinement remain agent work.

**Reads with:** [implementation sketch](in-jail-nix-roots-plan.md) (not a build authorization),
[`workspace-path-mirroring.md`](workspace-path-mirroring.md) (no mirroring needed),
[`nix-across-backends.md`](../reference/nix-across-backends.md) (backend boundaries).

---

## 1. Verdict

Build automatic translated-root protection with **bounded lifecycle, a cap, workspace inspection
and explicit release/cleanup** ([OQ-NR1](#decision-ledger)). Retention must not depend on an
agent remembering to delete links. Exact numeric cap/lease defaults and a reliable registration
mechanism are not settled; agent investigation must resolve them before this is build-ready.

A read-only view of the host's `gcroots/auto` is permitted ([OQ-NR2](#decision-ledger)), with
**no new routine launch line**. It exposes root-link paths, including stale entries and changes;
it does not grant host-file contents or write access. Unrelated pack read/exec trust banners and
other trust disclosures remain unchanged.

**Recommend synchronized producer integration, with explicit keep/release as its diagnostic surface.**
This is an engineering recommendation under that settled direction, not another owner question.

1. **Producer participation closes the discovery gap.** Admit a managed root while the producing
   client still holds demonstrable temporary protection; acknowledge before reporting protected success.
2. **Managed roots separate retention from user links.** A workspace owns yolo-created links and
   intent records. Expiration/release removes only those links, never result/profile files or store bytes.
3. **Caps constrain retention, not build size or hostile code.** Charge closure unions, share overlaps,
   and refuse new retention when safe eviction cannot make room; never evict an active root for a quota.
4. **Option C is only a recovery adjunct.** A read-only host-auto view is permitted with **no new routine
   launch line** ([OQ-NR2](#decision-ledger)); unrelated read/exec trust banners remain unchanged.
   Permission is not authorization to implement a new mount or service in this documentation pass.

The map and indirect-root capability already exist. Metadata exposure and durable disk retention
still change behavior. This proposal makes neither a new service nor a full Nix protocol proxy a prerequisite.

## 2. The mechanism: why the root is lost

### 2.1 Who resolves the path, and where

An indirect root is two symlinks: `/nix/var/nix/gcroots/auto/<hash>`, pointing at the out-link,
which points at the store path. At GC time `LocalStore::findRoots` walks `gcroots/`, and for each
link under `auto/` it `readLink`s the out-link path and then **lstats it** (`pathExists` is `lstat`).
If the path is absent it logs `removing stale link from … to …` and unlinks the auto entry. If it
is present, the out-link must be a symlink whose single target is inside the store.

- **Source:** [`src/libstore/gc.cc` `findRoots`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/gc.cc#L231-L305),
  stale deletion at L263–268. It is the same in 2.18.9, 2.24, 2.28, Lix and Determinate Nix.
- **The namespace is always the host's.** The process doing this is either root's
  `nix-collect-garbage` opening the local store directly, or a daemon worker, for a daemon-routed
  GC or the `min-free` auto-GC. The upstream `nix-daemon.service` sets no mount-namespace options.
- **A root query deletes too.** The daemon's `FindRoots` op runs the same walk, so even
  `nix-store --query --roots` from a jail prunes stale auto links ([§3](#3-measured-in-this-jail), M1).
- **Two hops, exactly.** A third symlink, or a relative second target, roots nothing.

So a jail's `/workspace/result` becomes `gcroots/auto/<sha1("/workspace/result")> → /workspace/result`,
and on the host `/workspace` does not exist. The root is deleted at the next walk. The auto entry is
named by the path string alone, so every jail's `/workspace/result` shares one auto entry. Those
collisions can also lose observation/origin information; they are not an accounting key.

### 2.2 What the client sends

`nix build --out-link X` and `nix-store --add-root X` call `IndirectRootStore::addPermRoot`, which
for a daemon client runs **on the client**:

1. send `AddTempRoot(storePath)`;
2. create the symlink `X` locally;
3. send `AddIndirectRoot(absPath(X))`.

The daemon's handler takes that string verbatim and creates the auto entry. It has **no trust
check**.

- **Sources:** [`indirect-root-store.cc`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/indirect-root-store.cc#L20-L45)
  and [`daemon.cc` `AddIndirectRoot`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/daemon.cc#L745-L756).
- **`absPath` does not resolve symlinks.** A path reached through a symlink is sent as spelled
  ([§3](#3-measured-in-this-jail), M7).
- **Standard root-making clients use this call.** `nix profile` and `nix develop --profile`
  create each generation link with `addPermRoot` (`profiles.cc` `createGeneration`), and nix-direnv
  roots its `.direnv/flake-profile-*` with `nix build --out-link`.
- **Non-root profiles have no other root.** `~/.local/state/nix/profiles` is rooted only through
  auto links. Root's profiles under `/nix/var/nix/profiles` are scanned directly, and a jail has
  none.

`AddPermRoot` (op 47, protocol 1.36, Nix 2.20) would create the link on the daemon's side, but it
**requires a trusted client**, and no CLI sends it over the unix socket. Upstream has no supported
way for an untrusted remote client to make a permanent root: [NixOS/nix#11812](https://github.com/NixOS/nix/issues/11812)
and [#7138](https://github.com/NixOS/nix/issues/7138) are open. A 2023 comment on #7138 describes
this exact case, docker containers sharing the host socket. The per-user gcroots directory that
once served it was removed in Nix 2.14 ([#5226](https://github.com/NixOS/nix/pull/5226)).

### 2.3 What already protects a jail

Two things protect a jail today, and both are tied to a process being alive:

- **Runtime roots.** The GC scans every `/proc/<pid>`: its `exe`, `cwd`, `fd/*`, `maps` and
  `environ`. The host `/proc` includes the jail's processes. So a running `nix shell` or
  `nix develop`, whose `PATH` names its store paths, is protected while it runs (M2, M3). On Linux
  there is no setting that turns this off. It does stop working under the unprivileged-daemon mode
  (`use-roots-daemon`, Nix 2.34), which cannot read other processes' `/proc`.
- **Temp roots.** These last exactly as long as the client's daemon connection
  (`temproots/<worker-pid>`, liveness by file lock). `nix build` holds its temp roots only for its
  own run (M4).

So the gap is precisely **between commands**: a `result` link, a profile or a devshell cache that no
live process is using right now.

> [!WARNING]
> **A Nix 2.36 client will stop sending temp roots to a 2.35 daemon.** [PR #16113](https://github.com/NixOS/nix/pull/16113)
> (merged 2026-09-17, unreleased) sends client temp roots only when the daemon advertises
> `addTempRoots`, with no fallback. The jail's nix comes from the image and the daemon from the
> host, so an image bump can open a window *during* a build that nothing here would notice. That
> matters to whoever bumps the image's nix, and it is independent of this design.

### 2.4 A new permanent root does not update an active GC snapshot

The pinned upstream [GC implementation](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/gc.cc#L531-L548)
scans permanent roots before temporary roots. `AddIndirectRoot` creates an auto entry; it does not
notify a collection already using an earlier permanent-root snapshot. Temporary roots instead
participate in the [GC lock/socket synchronization](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/gc.cc#L84-L173).

A legal failure schedule is: GC scans/prunes the jail-spelled entry; producer exits; observer
registers a translated entry; GC retains its earlier snapshot and collects the target. A temporary
pin acquired **after discovery** protects only from that point, not the preceding interval.
A successful daemon reply acknowledges registration, not path correspondence or future retention.

**Holding an earlier temp pin is not itself a complete handoff fence.** GC can start after that
pin was added, miss the later permanent link in its scan, then find the connection already closed
when scanning temporary roots. Reassert the temporary pin **after permanent registration is
acknowledged**, before closing its connection: an already-running GC receives that pin through
its synchronization socket; a later GC can see the already-created permanent root. Alternatively,
a proven transaction holding the GC lock across the handoff can supply the same ordering.
This fence and validity/path checks require disposable-store verification before implementation.

These are source-grounded limits, checked 2026-10-07, not a new host-GC experiment. The historical
runtime-root observations apply to the measured Linux daemon mode, not every backend/version.

## 3. Measured in this jail

All of these were run 2026-09-28 from a podman jail on Linux: host daemon Nix 2.35.2, protocol
1.38, jail client Nix 2.34.8, `nix store info` reporting `Trusted: 0`. The probe was a fresh
`builtins.toFile` store path that nothing else references. "Dead" means it appeared in
`nix-store --gc --print-dead`, which is a dry run that deletes nothing.

| # | What the jail did | Host's verdict |
|---|---|---|
| M1 | `nix-store --add-root /tmp/…/link -r P` (a jail-only path) | **dead**; the next root query logged `removing stale link … to "/tmp/…/link"` |
| M2 | held `P` open on an fd in a jail process | **alive**, listed as a `{censored}` runtime root |
| M3 | a jail process with `P` only in an environment variable | **alive** (runtime root through `environ`) |
| M4 | `AddTempRoot(P)` over a connection held open by a 34-line Python client | **alive** while connected, **dead** once closed |
| M5 | `nix build --out-link` at a path bound at the **host's spelling** (a nested user and mount namespace binding `/workspace` at `/home/matt/code/yolo-jail`) | **alive**, listed uncensored under the host path |
| M6 | a symlink at `/workspace/.yolo/x`, then `AddIndirectRoot("$YOLO_HOST_DIR/.yolo/x")` from the same Python client | **alive**; after `rm` of the link, **dead** again |
| M7 | `nix-store --add-root /tmp/lnk/p` where `/tmp/lnk → /workspace/.yolo` | the daemon recorded `/tmp/lnk/p` verbatim: the client does not resolve symlinks |
| M8 | nested `podman run --read-only -v …:/home/matt/.local/share/…/x` | the mountpoint was created on the read-only root fs |

**M6 proves host-path translation.** It needs no additional mount or trust; the measured registration
alone changes no nix invocation. It does not prove automatic discovery, concurrent-GC handoff or bounded
retention. `YOLO_HOST_DIR` already names the workspace's host path; M5 proves the namespace property by binding.

The first root query also deleted more than a hundred stale auto links. They pointed into jail homes, test
temp dirs, `/workspace/.claude/worktrees/…`, and `/home/agent/.cache/nix/flake-registry.json`, which
nix roots on every flake evaluation. They protected nothing, so deleting them cost nothing. They show
the scale of the problem: every one was a root the jail asked for and never had.

## 4. The translated root

A **translated root** *(coined here)* is an `AddIndirectRoot` sent with the host's absolute
spelling of a link the jail created. It is **not** a new kind of nix root. It is the ordinary
indirect root, carrying the string the host can resolve.

**The map.** A jail path translates only if it lies under a bind mount whose host source the
launcher knows. The launcher states that set; the jail does not derive it (the `root` field of
`mountinfo` is relative to the filesystem, a btrfs subvolume here, and is not a host path). Behavior:

- The **workspace** maps `/workspace/…` to `YOLO_HOST_DIR/…`, a variable the jail already has.
- The **home overlay binds** map `/home/agent/.local`, `.config`, `.cache` and the rest to their
  sources under `<workspace>/.yolo/home/` and the machine state dir.
- **A path under no mapped bind is not translated.** That covers the container root fs, the
  anonymous `/tmp` and `/var/tmp` volumes ([OQ-NR3](#decision-ledger), decided as [NR-D1](#NR-D1)), and
  `/nix/store` itself. The root
  stays exactly as dead as it is today, and nothing is reported.
- **Longest destination prefix wins**, because the home binds nest (`/home/agent/.claude/skills`
  inside `/home/agent/.claude`).
- **A nested launch composes.** It translates its own sources through its parent's map before
  writing its own. A source the parent cannot translate is dropped, with the same effect as above.

**What is registered.** A translated root is sent only when the jail-side path is a symlink whose
target is a literal `/nix/store/…` path. Anything else would be two hops or no root at all
([§2.1](#21-who-resolves-the-path-and-where)). The registration is idempotent: the auto entry is
named by the path's hash, so sending twice creates one entry.

**Failure and the existing client.** Current yolo-owned callers degrade to today's behavior
([§8](#8-what-is-built)). `internal/nixroots` negotiates protocol 1.37 and sends only
`AddIndirectRoot`; it has no temporary-pin or lifetime API. `Registrar.Root` **replaces** its
owned link. It must not be reused to observe an arbitrary result or profile generation: a delayed
P observation could overwrite the user's newer Q link.

**Proposed handoff.** Keep creation of yolo-owned links separate from preservation-only observation.
For a supported producer, the sequence is:

1. Keep the producer's temporary protection alive; use an explicit compatible temporary pin where
   client/daemon feature negotiation does not establish it.
2. Validate the still-live direct store target and actual parent under the mapped writable bind.
   Classify aliases before lexical translation; descriptor-relative no-follow traversal and
   identity/target revalidation reject retargets, masks and escapes. Never resolve the final link
   into a multi-hop profile alias or rewrite the source link/mtime.
3. Serialize admission/accounting; create a **managed root** *(coined here)*, a yolo-owned indirect
   root link under the workspace's durable state, not the user's result/profile link. Register
   that link's host spelling while temporary protection remains held.
4. After permanent registration acknowledgment, **reassert the compatible temporary pin on the
   still-held connection** to fence any GC that began since the first pin. Only then acknowledge
   protected admission and release the handoff pin. Ledger/root acknowledgment without this fence
   is insufficient. Crash recovery retains pending entries rather than treating them as released.

The initial temporary pin guards validity/registration; the post-registration fence protects a GC
whose permanent snapshot preceded the new managed link. The link covers later scans.
This relies on pinned-source Nix synchronization and on the host spelling naming the same link.
A fake daemon can verify ordering, not prove these premises. Host-side ancestor replacement is
still a coverage limit to investigate. Producer failure disposition is engineering work: prefer a
clear refusal of protected success over silently promising retention that admission could not establish.

### 4.1 Workspace ownership, caps and release

**Recommended model, not built:** durable intent records under each workspace's state name the
original direct leaf, current target, admission/last-use time, lease expiry and active operation
identity. One serialized workspace coordinator writes the records and managed links; producer
hooks submit intent, not competing root-file writes. A **proposed host-owned workspace index** records
canonical workspace/state identity at fresh host launch, without exposing a shared writable index to
jails. Current runtime-derived workspace inventory loses stopped `--rm` jails; it is insufficient for
this lifecycle. The host inventory validates every record/path before cleanup. Jail-written metadata
is not host deletion authority; missing/moved workspaces remain explicit unknowns, not broad searches.

- **One target, one workspace root.** Multiple leaves/generations referencing the same target share
  one managed link and distinct intent records. Retarget P→Q admits Q before dropping P's association;
  P persists only if another intent or active operation owns it, not as unlimited hidden history.
- **Closure accounting uses set unions.** Sum each referenced store object's recorded NAR size once
  per workspace; a user-wide inventory sums the union across workspaces once. Charge each workspace
  its full union even when another also needs it. Show shared bytes separately from potentially
  exclusive bytes; neither is a promise of disk space reclaimed by GC. Compression/hardlinks and
  roots outside yolo make physical/reclaimable bytes different.
- **Finite passive retention.** Renew on acknowledged producer/use intent, not observation, attach,
  scanning, or every launch. Deletion of an original leaf releases its intent; profile generations
  are individual direct leaves, not an alias resolved recursively. Passive expiry/cap eviction removes
  only managed links and records; original links can later dangle and require rebuild/re-admission.
- **Cap before admission.** Expire eligible passive entries, then evict least-recently-used passive
  intents until the proposed closure union fits the workspace budget. Reject an oversized target or an
  unknown/incomplete size calculation; never pretend an entry-count cap is a byte cap. Identical
  duplicate intent does not spend budget twice. No active root is removed to satisfy the cap.
- **Active-use safety.** A participating operation holds a synchronized temporary pin plus an active
  ledger claim; renewal/release serialize with eviction. Unknown process/backend identity, lost keeper
  connection or contradictory teardown evidence fences cleanup; a timestamp alone is not proof of
  non-use. This can leave existing roots over budget: report the fenced bytes and decline growth,
  not revoke protection. Arbitrary nonparticipating users remain outside this guarantee.
- **Inspection and explicit release.** Proposed workspace inspection lists intents, targets, union/
  shared bytes, expiry, active/fenced state and the exact next cleanup action. Explicit single/workspace
  release uses the same lock/liveness checks, returns pending when active/unknown, and names the next
  verification step. It never deletes user links, profiles, other workspaces' claims or store objects.

### 4.2 Defaults and stopped workspaces

**Reversible engineering recommendations, not owner-selected values:** start evaluation with a
**7-day passive idle lease**, **10 GiB workspace NAR-union budget**, **30 GiB user-wide union warning
threshold** and **1,024 intent records per workspace**. A 7-day lease covers a weekly revisit; the workspace
budget accommodates multi-GiB development closures while limiting distinct environments; the record
limit bounds metadata/observer load separately. The aggregate threshold is **inspection advice**, not
an atomic machine quota: current mounts provide no shared user-wide admission ledger. Per-workspace
locks cannot enforce an aggregate cap across simultaneous jails. A hard aggregate cap would require a
separately reviewed common admission authority; do not smuggle in its mount/service. Existing image measurements in
[`minimal-disk-footprint.md`](minimal-disk-footprint.md#2-measured-2026-08-25) are scale evidence only,
not measured agent-closure distributions or authority to copy the image reaper's week.
Validate these candidates using fixture closure graphs and later authorized workload measurements.
Numeric/default tuning and passive eviction are agent investigation. Recommend automatic bounded
retention for supported mapped producers, with active-use vetoes and inspection/release; prove the
lease/cap behavior and user-visible dangling-link consequences before selecting shipped defaults.

Recommended housekeeping: reconcile on explicit inspection/release, before admission, on fresh
start/restart, and in a host lifecycle slot at most once per 24 hours of **host yolo activity**.
No always-on host service is proposed. While all yolo processes are stopped, deadlines do not unlink
roots: the next host invocation enforces them. **Finite policy is not a wall-clock deletion guarantee
on an idle host.** Stopping releases proven completed active claims, not passive leases; failed stop
or unknown runtime fences them. Restart recovers pending/expired state before admitting new targets.

No-profile/native root paths, preexisting unmanaged roots and yolo's owned image/prefix/package
roots are not silently migrated. Classify/report legacy entries; never unlink arbitrary auto entries
or original links. Initially empty ledgers retain nothing. Invalid/oversized records decline admission
and cleanup with a repair step. A reduced budget does not evict active claims. Clock rollback cannot
extend passive retention silently; preserve last observed time and surface uncertainty for reconciliation.
A shared writable bind needs workspace intent ownership; an unattributable auto event cannot assign it.
A stopped workspace removed/moved outside yolo is reported missing, not a reason to follow a new path.

Caps are a cooperative retention policy, **not a hostile-agent quota**: an untrusted daemon client
already can register other permanent roots. A strict adversarial disk boundary would need a different
host enforcement/trust design. Bounded accepted state and admission refusal do not bound all Nix bytes.

## 5. What registers the translated root

| Option | Achievable coverage | Limits / recommendation |
| :--- | :--- | :--- |
| **A — explicit keep** | A currently valid direct leaf admitted under temporary protection | Diagnostic/fallback; cannot repair build→keep GC loss and is not the automatic product |
| **B — CLI wrapper** | A finite parsed command set and named output leaves | Post-exit registration still races; absolute binaries/library clients bypass it. Reject as universal fix |
| **C — host-auto observer** | Eventually observes some standard root requests, then validates this jail's local candidate | Best effort only; new read-only mount/observer not built or selected |
| **D — mirrored root dir** | Steered tools writing host-identical paths | Default `result` remains uncovered; do not add path mirroring for this fix |
| **E — producer integration** | Supported Nix permanent-root creation while its temporary pin remains alive | Recommended investigation: acknowledge managed-root admission before protected success; version/client range must be enumerated |
| **F — local intent reconciliation** | Declared durable root locations or cooperative intent records | Recovery adjunct; bounded watches/scans cannot discover every arbitrary path or close producer races |

### 5.1 What option C can actually observe

[Inotify](https://man7.org/linux/man-pages/man7/inotify.7.html) queues a filename/mask, **not a
symlink target**. If GC or `FindRoots` unlinks `auto/<hash>` before processing, `readlink` fails;
the path hash cannot recover the original request. A startup scan cannot recover a vanished entry.
The fixture-only experiment on 2026-10-07 observed exactly that schedule, with the local result
still present. No Nix store/daemon was involved; it proves information loss, not GC behavior.

If later selected, the observer must watch **before** its initial scan, drain events, reconcile on
queue overflow/watch invalidation, and report an incomplete/degraded state when recovery is not
possible. Scans, queues and watch counts need finite limits; local intent reconciliation must stay
within declared writable mapped roots. Translated managed-link events must be excluded to prevent
feedback loops. Missing permissions/view/watch is no broader-mount or privilege fallback.

Raw entries identify neither producer nor target. Two jails with `/workspace/result` share an auto
key; each may inspect its own P or Q and register a distinct managed host link. This is opportunistic
local recovery, not attribution. Deduplicate by workspace intent/canonical leaf and target, never
by raw auto filename across jails. A shared host leaf needs distinct claims before either is released.
Observer discovery never rewrites source symlinks and never renews leases simply by seeing them.

### 5.2 Supported producers and explicit limits

Investigate the permanent-root creation boundary rather than reconstructing argv. It must cover
standard `nix build`/`nix-build`, profile **generation leaves**, `nix develop --profile`, direnv's
chosen client and the registry cache call sites **only if they use the supported implementation**.
Rootless deployment and mixed Nix versions need separate proof of temporary-root behavior.
Alternative binaries, direct library clients, rootless/unprivileged daemon modes and unmapped scratch
remain explicitly outside any unproved coverage. A build requesting no permanent root creates no intent.

`macos-user` shares host paths and needs no namespace translation; this proposal does not intercept
its valid native roots or apply managed expiry there by accident. Apple/VM stores remain deferred.
An attached container acquires no new mount/client hook; restart is required for a changed contract.

## 6. Alternatives rejected

| Alternative | Verdict |
|---|---|
| **Protocol proxy**: a yolo socket in front of the daemon that rewrites `AddIndirectRoot` in flight | Rejected. It is the only fully transparent option, but the worker protocol is not self-delimiting, so the proxy must frame every op, NAR streams included, for every protocol version. One bug breaks every nix command in every jail |
| **Mirror the workspace at its host path** | Out of scope here, and not needed. [`workspace-path-mirroring.md`](workspace-path-mirroring.md#OQ-WP1) asks whether a fourth absolute-path problem exists. This is one, but translated roots fix it without mirroring, so it does not move that verdict |
| **Trust the jail** (`trusted-users`) for `AddPermRoot` | Rejected. The root would still be created in the daemon's namespace at a path the jail must spell for the host, so translation is still needed. And a trusted client is root-equivalent on the host |
| **Temp-root keeper**: an in-jail process holding one connection and `AddTempRoot`ing what the jail references | Rejected as the primary mechanism. It protects only while the jail runs, needs a list of what to keep, and has no way to drop one root short of reconnecting |
| **Host-side reconciler** reading only `gcroots/auto` | Rejected as a complete trigger. Raw entries contain no producing-jail identity and can be pruned before read. An explicit per-workspace producer protocol is different and remains investigable; it is not authorized here |
| **`keep-outputs` / `keep-derivations`** | Irrelevant. They widen what an existing root keeps; they create no roots |

## 7. Non-goals

- **Rooting jail-only paths.** `/tmp` in a jail is scratch. A link there stays unrooted
  ([OQ-NR3](#decision-ledger), decided as [NR-D1](#NR-D1)).
- **Deleting user state or running host GC.** Expiry/release removes only validated yolo-managed links.
  No profile-generation deletion, credential/config cleanup, shared-store GC or broad root scan is licensed.
- **Apple Container and the container Macs.** In-jail nix there is ruled "possible, but not planned"
  ([`setup-support-gaps.md`](../plans/setup-support-gaps.md#2-ranked-gap-backlog) G21). Everything here applies unchanged if that ever changes.
- **The unprivileged-daemon mode.** Runtime roots stop protecting jail processes under it
  ([§2.3](#23-what-already-protects-a-jail)). Translated roots are unaffected, so this design is the
  better position if a host ever adopts that mode.

## 8. What is built

The briefing states the gap, wherever the launch mounts the host nix daemon. It says that nix here
uses the host daemon and store, that ordinary jail-spelled result/profile links are not roots the
host honors, and to rebuild a dangling link. The historical Linux runtime-root observation has the
mode/version limits in [§2.3](#23-what-already-protects-a-jail). Keep the live warning until a supported
trigger actually ships; translated yolo-owned roots do not make arbitrary user roots safe.

**Current safety limit:** [`Registrar.Root`](../../internal/nixroots/register.go) replaces an owned
link and sends only indirect registration. The controlled consumers have build→root windows;
none establishes the synchronized handoff proposed in [§4](#4-the-translated-root). Fake-daemon
caller tests establish strings/ordering at those callers, not concurrent-GC or observer completeness.

**yolo's own roots are translated roots** ([NR-D2](#NR-D2), built 2026-10-01). Three pieces, and
none of them is a trigger for a user's links:

- **The map.** A podman launch that mounts the host nix daemon, and is not sealed, sets
  `YOLO_HOST_PATH_MAP` in the jail: a JSON object from each mount's destination to the host
  directory it is, read off the argv the launch is about to run (`hostPathMapEnvArgs` in
  [`translatedroots.go`](../../internal/cli/run/translatedroots.go)). A read-write bind of an
  absolute source translates. A read-only bind, a volume and a tmpfs stay in the map with an
  empty value, a *mask* *(coined here)*: mounts nest, and longest-prefix matching would otherwise
  translate a path under one through the mount above it. A nested launcher passes each source
  through its own jail's map first, and a source that map cannot spell becomes a mask. A map in
  which nothing translates is not set at all.
- **The client.** [`internal/nixroots`](../../internal/nixroots) speaks the handshake at
  protocol 1.37 and `AddIndirectRoot`, nothing else. `Registrar.Root` resolves the link's
  directory, translates it, replaces the link and sends the host's spelling. A link it cannot
  translate is left uncreated, and the daemon is not dialled.
- **The consumers.** `gcRooter` gives a host launch `nix-store --add-root` and an in-jail one the
  translated root, for four roots: the image's, the prefix's, the image extras' and the
  store-delivered packages' profile. A fifth is in-jail only: the image copier's `--out-link`,
  which is its root on the host, is registered again as a translated root after an in-jail
  build (`AutoLoadOptions.RootCopier`). A jail whose launcher stated no map gets none of them,
  the old skip. In a jail two failures print, as a warning on the launch stream: the daemon
  refusing the root, and a roots directory that cannot be made. Every other failure is silent,
  and none fails the launch.

The tests drive each of the five call sites against a fake daemon
([`translatedroots_test.go`](../../internal/cli/run/translatedroots_test.go),
[`jailprefix_test.go`](../../internal/cli/run/jailprefix_test.go)), and the image package's
default copier build against a stand-in `nix`
([`layercopy_test.go`](../../internal/image/layercopy_test.go)).

MEASURED 2026-10-01, from this jail against the host daemon (Nix 2.35.2, `Trusted: 0`). This
jail's own launcher predates the map, so every run that needed one was handed
`{"/workspace": "$YOLO_HOST_DIR"}` by hand, with `HOME` under the workspace:

- a nested launch from a workspace under `/tmp` registered its image root and its prefix root.
  The host's `nix-store --query --roots` listed both under the host's spelling of the link, and
  once the links were deleted it listed neither;
- the same production path rooted a fresh `builtins.toFile` probe. `nix-store --gc --print-dead`
  listed the probe before the root, did not list it with the root in place, and listed it again
  once the link was gone;
- the nested jail's own map translated only its `~/.cache`, whose source lay under the workspace.
  Its `/workspace` and `~/.local`, whose sources lay under `/tmp`, were masks, as
  [NR-D1](#NR-D1) intends;
- with no map handed over, the nested launch made no root links and set no map in its jail;
- the image copier's build, driven directly with the registrar as its root hook rather than
  through a nested launch, rooted its out-link under the host's spelling. `--query --roots`
  listed it beside the host's own copier root for the same store path, and dropped it once the
  link was deleted.

**Unmeasured deployment:** the historical runs above handed the map over manually. No fresh
host-launcher map deployment, observer mount, managed lifecycle/cap or synchronized-GC handoff was
measured in this documentation pass. Neither Linux fixture/nested evidence nor source inspection
establishes native/rootless/AWS behavior.

**Not translated:** the prefix's `--out-link` (`image.JailPrefixOutLink`) still registers under
the jail's spelling, dead as before. The prefix root above backs the same store path, so a
translated copy of it would root nothing more.

## Remaining engineering investigation

The automatic direction and lifecycle/cap/inspection/release requirements are answered, not open
owner choices. Compare synchronized producer admission with observer-only recovery, establish
failure behavior and version/path coverage, and test the passive lease/cap/eviction recommendations.
Do not re-ask [OQ-NR1](#decision-ledger) or fabricate an owner selection of numeric defaults.
No new owner question is raised by this revision; a genuinely new product choice would need its
own new ID and evidence, rather than reusing a settled card.

## Decision Ledger

Owner policy is settled; reliable discovery/GC handoff and bounded-lifecycle defaults remain
agent investigation. The automatic user-root feature is not build-ready or built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-NR1 | Owner aligned with automatic translated roots, with mandatory lifecycle, cap, workspace inspection and explicit release/cleanup; not indefinite retention dependent on agent memory. Numeric cap/lease and a reliable watcher were not chosen. Vantage comment `b6072a1d`, round 0 | 2026-10-07 | [§1](#1-verdict), [§4](#4-the-translated-root), [§5](#5-what-registers-the-translated-root) | — |
| OQ-NR2 | Owner permits a read-only host `gcroots/auto` mount; no extra routine launch line. Unrelated trust banners unchanged. Vantage comment `51b328aa`, round 0 | 2026-10-07 | [§1](#1-verdict) | — |
| OQ-NR3 | Reversible implementation choice: binds only; anonymous `/tmp` and `/var/tmp` volumes do not translate. Provenance: [NR-D1](#NR-D1) | 2026-09-30 | [§4](#4-the-translated-root), [§7](#7-non-goals) | ✅; [§8](#8-what-is-built) |
| OQ-NR4 | Reversible implementation choice: yolo's own in-jail roots use translation as the first consumer; existing reapers remain responsible where links live. Provenance: [NR-D2](#NR-D2) | 2026-09-30 | [§8](#8-what-is-built) | ✅; user-root lifecycle remains unbuilt |
| <a id="NR-D1"></a>NR-D1 | *Implementation decision, [OQ-NR3](#decision-ledger).* **Binds only: the map holds the binds the launcher itself wrote, and the anonymous `/tmp` and `/var/tmp` volumes do not translate.** Those volumes are per-launch scratch that yolo deletes once the jail exits, so a root there could outlive nothing but the launch. A running process that uses the store path is already kept by runtime and temp roots ([§2.3](#23-what-already-protects-a-jail)). A nested launcher cannot learn a volume's host path, while the host launcher could from `podman volume inspect`, so translating volumes would give one link two answers depending on who launched. A nested jail's own roots are [NR-D2](#NR-D2)'s. Reversible: the host launcher can add its volumes to the map later | 2026-09-30 | [§4](#4-the-translated-root), [§7](#7-non-goals) | 2026-10-01: the map holds every mount the argv makes, and a volume, a tmpfs or a read-only bind is a mask, so a link under `/tmp` or `/var/tmp` translates to nothing ([§8](#8-what-is-built)). MEASURED in a nested launch: its `/tmp` workspace was a mask |
| <a id="NR-D2"></a>NR-D2 | *Implementation decision, [OQ-NR4](#decision-ledger).* **Yes: yolo's own in-jail roots become translated roots, the first consumer of the protocol client in [§4](#4-the-translated-root).** Three skips go, not the two the question names: `rootImageFn` (the image root, [`imageload.go`](../../internal/cli/run/imageload.go)), the in-jail skip of `image.RegisterPrefixRoot` ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)), and the in-jail skip of `rootExtrasProfile` for the store-delivered packages' extras profile ([`storepackages.go`](../../internal/cli/run/storepackages.go)). All three root under `paths.BuildDir()`, which in a jail is inside `/home/agent/.local`, a mapped bind, so each one translates. This consumer needs no trigger from [OQ-NR1](#decision-ledger), because yolo registers its own links directly. **Reaping stays with the existing reapers, run where the links live.** A translated root dies with its link. A nested launcher's `yolo prune` sweeps its own `build/roots` and `build/prefix-roots` under the same retention rules the host's sweep follows ([`OQ-LS1`](../reference/image-retention.md#why-its-this-way)'s week, [`OQ-LS4`](../reference/image-retention.md#why-its-this-way)'s liveness), so nothing new reaps. The leaning's concern holds, and it is why this is so: the host's reapers never walk a workspace's home overlay, so the jail that made a link reaps it. What remains is [§4](#4-the-translated-root)'s *who can hold host disk*: a nested jail's root holds its closure until that jail's own sweep removes the link. Reversible: keep the skips | 2026-09-30 | [§8](#8-what-is-built) | 2026-10-01: `internal/nixroots` and `gcRooter` ([§8](#8-what-is-built)). Four skips went, not three: the store-delivered packages' own profile root (`storeProfileRootLink`) was skipped in-jail too. A fifth root is the same principle: the image copier's out-link, dead in a jail, is registered again as a translated root after an in-jail build (`AutoLoadOptions.RootCopier`). MEASURED for the image and prefix roots in a nested launch, and for the copier's root with its build driven directly; the two profile roots are pinned by the unit tests only. Nothing reaps `build/package-roots`, on the host or in a jail, so a nested jail's two profile roots last until their links are deleted, as the host's do |

## Appendix: reproducing M4 and M6

**Historical probes, not this task's acceptance commands.** `FindRoots`/root queries can prune
shared stale links; even the GC dry-run below is not authorized for a shared host store in this
pass. Keep the original measurement/reproduction record; use disposable fixtures for new experiments.

The measurement client, verbatim. It speaks the worker-protocol handshake, sends one
`AddTempRoot` (`temp`) or `AddIndirectRoot` (`indirect`), and holds the connection for the given
seconds. Check the verdict with `nix-store --gc --print-dead | rg <hash>`, a dry run.

```python
# Minimal nix worker-protocol client: AddTempRoot / AddIndirectRoot on ONE connection, then hold it.
import socket, struct, sys, time
M1, M2, LAST = 0x6e697863, 0x6478696f, 0x616c7473
def wu(s, n): s.sendall(struct.pack('<Q', n))
def ru(s):
    b = b''
    while len(b) < 8:
        c = s.recv(8 - len(b))
        if not c: raise EOFError
        b += c
    return struct.unpack('<Q', b)[0]
def rb(s, n):
    b = b''
    while len(b) < n: b += s.recv(n - len(b))
    return b
def ws(s, t):
    t = t.encode(); wu(s, len(t)); s.sendall(t + b'\0' * ((8 - len(t) % 8) % 8))
def rs(s):
    n = ru(s); d = rb(s, n); rb(s, (8 - n % 8) % 8); return d.decode()
def stderr(s):
    while True:
        m = ru(s)
        if m == LAST: return
        if m == 0x63787470: raise RuntimeError('daemon error (STDERR_ERROR)')
        if m == 0x64617416: print('log:', rs(s)); continue  # STDERR_NEXT
        raise RuntimeError('unhandled stderr msg %x' % m)
s = socket.socket(socket.AF_UNIX); s.connect('/nix/var/nix/daemon-socket/socket')
wu(s, M1); assert ru(s) == M2; dv = ru(s); print('daemon proto %d.%d' % (dv >> 8, dv & 0xff))
wu(s, (1 << 8) | 37); wu(s, 0); wu(s, 0)
print('daemon version', rs(s)); print('trusted', ru(s)); stderr(s)
op, path = sys.argv[1], sys.argv[2]
wu(s, {'temp': 11, 'indirect': 12}[op]); ws(s, path); stderr(s); print(op, 'root ->', ru(s))
hold = int(sys.argv[3]) if len(sys.argv) > 3 else 0
sys.stdout.flush(); time.sleep(hold)
```

```console
$ python3 nixproto.py temp /nix/store/<hash>-probe 40 &      # M4: alive while held
$ ln -s /nix/store/<hash>-probe /workspace/.yolo/x
$ python3 nixproto.py indirect "$YOLO_HOST_DIR/.yolo/x"      # M6: alive until the link goes
```
