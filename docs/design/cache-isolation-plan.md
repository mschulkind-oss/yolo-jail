---
title: "Sketch: trace private cache delivery and safe reclaim before building"
status: draft
stage: SKETCH
next: "Owner rulings on OQ-CI1 and OQ-CI2, then build slice S1 against the then-current tree; the rootless-host and Mac checks stay owed"
depends-on:
  - cache-isolation.md#OQ-CI1
  - cache-isolation.md#OQ-CI2
tags: [plan, storage, security]
---

# Sketch: trace private cache delivery and safe reclaim before building

**Status:** 2026-10-09. Rulings [OQ-CI1](cache-isolation.md#OQ-CI1) and [OQ-CI2](cache-isolation.md#OQ-CI2) are still open. The non-owner-gated
verification pass is done: [results](#verification-results-2026-10-09). The source map was
re-checked against `8f52d3561`; only `run.go` and `paths.go` had moved, and their anchors are updated.
Linux evidence was run in this jail. The rootless-host, Podman Machine, Apple Container and
macos-user checks are written as exact commands, not run. No production code changed.
**Design:** [ordinary cache isolation](cache-isolation.md).
**Research:** [trust and reclamation findings](../research/cache-trust-and-reclamation.md).

**Precedence:** the design wins on behavior; the current tree wins on facts; this sketch
is advice and the first thing to be wrong. The [build hand-off](#build-hand-off-under-either-ruling)
is ready to start once both rulings land. It is not authorized before then.
Paths below are investigation seams, not an authorized edit fence or a ticket wall.

The scope-dependent entries wait on [OQ-CI1](cache-isolation.md#OQ-CI1);
capacity/retention claims wait on [OQ-CI2](cache-isolation.md#OQ-CI2).
Independent source preparation below can proceed while those rulings are pending.
No further owner question is needed for helper names, record encoding or traversal batches.

## Current source map

| Seam | What a future source-complete plan must connect |
| :--- | :--- |
| [`paths.go`](../../internal/paths/paths.go), [`naming.go`](../../internal/runtime/naming.go) | Derive the proposed host-only `ordinary-caches` child of `GlobalStorage()`, never of `GlobalCache()` or the workspace; verify full resolved identity, not only container-name hash. Preserve the frozen container-name contract. |
| [`statefile.go`](../../internal/paths/statefile.go), [`wsstatebeneath.go`](../../internal/cli/run/wsstatebeneath.go) | Reuse checked real-root and beneath-root operations. Trace the remaining interval from opened directory to runtime delivery; a prior Lstat alone is not proof against a swap. |
| [`run.go`](../../internal/cli/run/run.go#L1989-L2045), [`assemble.go`](../../internal/cli/run/assemble.go#L109-L120) | Actual ordinary selection supplies `cacheDir` explicitly; mount fallback changes alone cannot deliver isolation. Resolve once and pass that same backing into all readers. |
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
| [`runContainer` selection](../../internal/cli/run/run.go#L1989-L2045) → `assembleInput` → [`both mount emitters`](../../internal/cli/run/assemble_parts.go#L54-L173) | Resolve/admit once; carry concrete backing identity and exceptions. Close the checked-handle → runtime-pathname reopen gap, then persist dispatched before submission. Sealed selection stays separate. |
| [`startKeeper` / relay](../../internal/cli/run/run.go#L2317-L2381) → [`awaitRunning`](../../internal/cli/run/keeper.go#L700-L720) | Hand off the cache attempt, not just the courtesy launch fd. The running frame fires even when its poll found no instance: keep starting/unknown until exact instance acknowledgment and settled submission. |
| [`keeper unwind`](../../internal/cli/run/keeper.go#L940-L952) and [`finish`](../../internal/cli/run/keeper.go#L908-L934) | Record inactive only after quiescence proof, not defer/exit status. Retain surviving or restartable containers and incomplete submissions. |
| [`attachExisting`](../../internal/cli/run/run.go#L2694-L2720) / session count | Inspect actual mount identity and join that hold before exec; no fresh allocation. Counting may warn/continue, so a separate mandatory cache admission is needed. Legacy/ambiguous sources retain their hold. |
| Native [`Run` dispatch](../../internal/cli/run/run.go#L1103-L1128) → [`RunMacosUser`](../../internal/macosuser/orchestrator.go#L872-L890) / [`bootstrap`](../../internal/macosuser/orchestrator.go#L1261-L1275) | Register before bootstrap/cache use, not `OnAgentStart`. Native slot already runs before backend setup. Account-home hold prevents ordinary different-workspace overlap but not a surviving guest after launcher death. |
| Native env/profile/generator chain | Per-session env file, bootstrap hydration and final profile must agree on one backing. Stamp paths need invocation-time addressing; shared launcher generation cannot bake a different session's cache path. Keep HOME and existing home-tier refusal. |
| [`inventory.cacheStores`](../../internal/cli/stores/inventory.go#L519-L584) and [`native rows`](../../internal/cli/stores/inventory.go#L1256-L1288) | Enumerate the host-owned admitted scopes after exit, plus separately labeled legacy/relocated/host exceptions. Native default must not remain an unreclaimed account row disguised as container coverage. |
| Manual [`RunPrune` cache call](../../internal/prune/prunecmd.go#L1100-L1131) and [`measureAndPurgeCache`](../../internal/cli/run/housekeeping.go#L326-L375) | Both consume the same admitted-scope engine, positive class definitions, no-follow roots and per-unit admission check. Existing relocation map conveys a grant, not automatic ownership; host alias remains held. |
| [`container slot`](../../internal/cli/run/run.go#L2370-L2380) / [`native slot`](../../internal/cli/run/housekeeping.go#L272-L281) | Discover exited scopes, retain the just-starting/live scope; use interrupted coverage and apply results, stamp only completed passes. Slot timing is not liveness evidence. |

## Remaining source preparation, not owner gates

All four items are now verified or specified. The [results](#verification-results-2026-10-09)
section has the evidence for each, and the [ledger](cache-isolation.md#10-decision-ledger)
records the mechanism choices they settle.

| Item | Outcome |
| :--- | :--- |
| 1. Protected delivery | **Verified on Linux Podman for the leaf-only anchor** ([V1](#v1-the-host-only-anchor-holds-on-linux-podman)). Two grant paths can expose it today, so a launch refusal is owed ([V2](#v2-two-read-only-grant-paths-can-expose-the-anchor)). The native anchor cannot sit under the host home ([V3](#v3-the-native-anchor-cannot-sit-under-the-host-home)). Rootless, Podman Machine and Apple Container checks are specified. |
| 2. Native addressing | Unchanged candidate. Because every native workspace runs as one uid, Seatbelt is the only scope separator ([V3](#v3-the-native-anchor-cannot-sit-under-the-host-home)). |
| 3. Quiescence | Predicates written for containers ([V4](#v4-container-quiescence-predicate)) and macos-user ([V5](#v5-native-quiescence-predicate)). The native predicate depends on five Mac checks. |
| 4. Classes and budgets | Class table and traversal rule written ([V6](#v6-positive-classes-and-bounded-traversal)). A run fixture shows today's purge deletes a credential and a lock file. |

Standalone experiments can falsify a protocol, but they cannot establish production wiring.
No user cache was inventoried and no account was modified.

## Verification results, 2026-10-09

Run in this jail against `8f52d3561`. Nested podman here is **rootful with `--userns=host`**
(`podman info` reports `rootless=false`, crun 1.30, podman 5.8.7), so the
[rootless carve-out](../../AGENTS.md#testing) applies to every Linux result below.
`go test -short` of `internal/prune`, `internal/macosuser`, `internal/entrypoint`,
`internal/cli/stores`, `internal/hostcas` and `internal/cli/run` passed. That baseline covers
every existing fixture named [below](#tests-lead-the-later-implementation-not-this-document-stage).

### V1. The host-only anchor holds on Linux Podman

**No jail-writable path reaches the proposed parent on a real launch.** This jail was launched
by host yolo. Its mount table (`awk '{print $4,$5,$6}' /proc/self/mountinfo | rg yolo-jail`)
binds these children of `GlobalStorage()`:

- read-write: `cache`, `home/.claude-shared-credentials` and `mise`;
- read-only: `agents/<cname>/…`, `flake-bundles/<stamp>`, `state/<loophole>/…`, and two
  user-configured context mounts, `logs` and `captures`.

`GlobalStorage()` itself is not bound, and neither is any ancestor of
`<GlobalStorage>/ordinary-caches`.

**A bounded nested-podman model** reproduced yolo's podman shape: a read-only home skeleton
plus the leaf bound at `/home/agent/.cache`. It used a stand-in `GlobalStorage` with two scopes,
host-only `0700` parents, harmless sentinels, and the container running as root.

| Test | Result |
| :--- | :--- |
| T1. Guest reads `..` from the leaf | It reaches the container's `/home/agent`, never the host parent; the scope record is unreachable. `mountinfo` shows the source's host path, so a scope path must carry no authority or secret. |
| T2. Guest renames or removes the mounted leaf | `Read-only file system`, the parent being the `:ro` skeleton. |
| T3. Guest plants `up -> ../record` and `sib -> <scopeB>/cache` | Both dangle inside the guest. On the **host** the same links resolve to the authority record and the sibling scope, so every host reader must walk without following links. Scope B was unchanged. |
| T4. Host-side swap of the leaf for a link to scope B, then `podman run` | The guest read **scope B's sentinel**. The runtime reopens the source by pathname and follows links. |
| T5. Same swap between two `podman start` runs of one container | The first start read A and the second read B. A restart re-resolves the pathname. |
| T6. Legacy `cache` mount tries to reach `../ordinary-caches` | `No such file or directory`. |

T4 and T5 are why the anchor's protection must come from **who can write its parents**, not from a
check before submission. Only host yolo can write them, so the hold must cover every restart.

**Not covered by these runs:**

- **Rootless ID mapping:** needs a real rootless host or CI.
- **Podman Machine:** whether the VM sees the leaf at the same host path, and its `..` behavior.
- **Apple Container:** whether a VZ shared directory's root confines `..`. The anchor sits
  outside the writable `wsState` home bind by construction, so a rename inside the guest cannot
  touch it.

The checks are R1–R3 in the [native experiment list](#independent-native-experiment-owed).

### V2. Two read-only grant paths can expose the anchor

**Read isolation also needs no grant that exposes the parent.**

- `mounts` runs its refusal set only on read-write elements
  ([`mounts.go`](../../internal/config/mounts.go#L449-L462)). A read-only element is
  explicitly allowed in the agent-editable workspace config
  ([`mounts.go`](../../internal/config/mounts.go#L431-L437)).
- The pack `mount` kind refuses nothing and is bounded only by disclosure
  ([`kinds.go`](../../internal/packdecl/kinds.go#L127-L138)).

So a read-only grant of `~/.local/share/yolo-jail` would hand one jail every scope's cache.
The same grant already exposes today's shared `cache` and `agents/`. Only the new root is in
scope here.

The launch must refuse any grant whose resolved source is the root, an ancestor of it, or
anything inside it other than the leaf yolo delivers. That covers read-only and read-write
`mounts`, pack `mount`, `host_files` and `cache_relocations` targets. The refusal names the
grant and asks the owner to narrow it and retry
([CI-D3](cache-isolation.md#10-decision-ledger)).

Relocation mountpoints are made with `os.MkdirAll` under `GlobalCache()`
([`ensure.go`](../../internal/storage/ensure.go#L99-L102)). That is safe today only because
a subdir key is a single segment. Under a private leaf it should be a beneath-root `Mkdir`
([CI-D8](cache-isolation.md#10-decision-ledger)).

### V3. The native anchor cannot sit under the host home

**macos-user manages ACLs only on neutral ground.** It refuses to manage them inside a user home
([`commands.go`](../../internal/macosuser/commands.go#L264-L271)), and its workspaces live under
`/Users/Shared/yolo` ([`macosuser.go`](../../internal/macosuser/macosuser.go#L98)). Host material
reaches the guest through the root-owned `/var/yolo-jail`, staged by `sudo`. Guest writes there
were measured on hardware as `Operation not permitted`
([`StagedCtxRoot`](../../internal/macosuser/macosuser.go#L366-L389)). So the guest cannot be
assumed to traverse `<GlobalStorage>` under `/Users/<host>`.

The native candidate ([CI-D4](cache-isolation.md#10-decision-ledger)) has four parts:

- **Location:** `/var/yolo-jail/ordinary-caches/<ws-key>/scopes/<scope-id>/cache`, with
  root-owned parents and a leaf writable by `_yolojail`.
- **Write:** the Seatbelt profile write-allows that leaf.
- **Read:** the profile denies reads of the `ordinary-caches` root except the leaf. Every
  workspace runs as the one `_yolojail` uid ([`macosuser.go`](../../internal/macosuser/macosuser.go#L29)),
  and the profile reads by `(allow default)` ([`seatbelt.go`](../../internal/macosuser/seatbelt.go#L170-L181)),
  so the MAC layer is the only scope separator.
- **Host reclaim:** host deletion relies on the inheriting shared-group ACL that workspaces already
  use ([`SharedRootProvisionCommands`](../../internal/macosuser/macosuser.go#L172-L185)).
  There is no new sudo deletion path.

N1–N4 in the [native list](#independent-native-experiment-owed) check it.

### V4. Container quiescence predicate

**A filter on the mount source cannot be used.** A container with
`-v /tmp/yolo-anchor-exp/q/leaf:/home/agent/.cache` matched nothing under
`podman ps -a --filter volume=<source>`, but it did match `--filter volume=/home/agent/.cache`.
Every jail has that destination, so the filter cannot name a scope. `podman inspect` reports
`Mounts[].Source` exactly.

**Scope S is container-quiescent** if and only if all of these hold under S's admission mutex:

1. No attempt of S is recorded as dispatched, starting, live or unknown without a matching,
   proven-gone instance.
2. `podman ps -a -q` succeeded for every state, including created, paused and stopped.
3. `podman inspect` succeeded for every listed container.
4. No listed container has a mount source equal to S's leaf or inside it.

Any failed query makes S unknown. A user's own container that binds the leaf also holds it.
Apple Container uses the same shape through `container ls -a` and `container inspect`,
checked by R3. Podman Machine is checked by R2 ([CI-D5](cache-isolation.md#10-decision-ledger)).

### V5. Native quiescence predicate

Source facts:

- **Launch:** every guest step is `sudo --user=_yolojail`: bootstrap, then provisioning, then
  `yolo-jaild supervise`, then the agent
  ([order](../../internal/macosuser/orchestrator.go#L1273-L1313)). The account hold and session
  record come first ([hold](../../internal/macosuser/orchestrator.go#L878-L882),
  [record](../../internal/macosuser/orchestrator.go#L1150-L1153)).
- **Supervisor stop:** only the supervisor gets its own process group, signalled TERM then KILL
  ([`real.go`](../../internal/macosuser/real.go#L359-L384)). The source says a SIGKILL is not relayed
  and that what it leaves behind "only a Mac can measure"
  ([`real.go`](../../internal/macosuser/real.go#L343-L347)).
- **Agent:** it runs as a plain child with no process group of its own; only SIGTERM and SIGHUP
  are forwarded to its `sudo` ([`macosuserarm.go`](../../internal/cli/run/macosuserarm.go#L26-L29),
  [`RunSession`](../../internal/cli/run/macosuserarm.go#L212-L245)). Nothing in that path stops
  descendants the agent leaves behind.
- **Host side:** the host user cannot signal the guest, and a double-forked daemon escapes the
  in-sandbox guard ([`sessionguard.go`](../../internal/macosuser/sessionguard.go#L39-L46)).
  Host locks see only the invoking host user's launches
  ([`accounthomehold.go`](../../internal/cli/run/accounthomehold.go#L36-L39)).
- **Process tooling:** no code lists or kills processes by the guest uid. The profile leaves the
  account home, including `~/Library/LaunchAgents`, writable.

**Scope S is native-quiescent** if and only if all of these hold under a **machine-wide** admission
fence that every guest launch takes, other host users' launches and captures included:

- (a) no session record of S's workspace is live or unknown;
- (b) no account-home hold is live or unknown;
- (c) a process listing reports zero processes with the `_yolojail` uid;
- (d) no launchd job or cron entry exists for that uid.

Condition (c) is uid-wide on purpose. One uid serves every scope and host user, so a
per-scope process proof does not exist. The account hold already makes this cost nothing
extra, because only one workspace runs at a time.

**If A1 shows an unprivileged host user cannot list the guest's processes, native reclaim
stays a support stop.** It does not escalate to sudo ([CI-D6](cache-isolation.md#10-decision-ledger)).
A1–A5 in the [native list](#independent-native-experiment-owed) check the assumptions.

### V6. Positive classes and bounded traversal

**Confirmed in source:**

- **Buckets:** default and heavy buckets, plus the forbidden names
  ([`cachepurge.go`](../../internal/prune/cachepurge.go#L25-L39)).
- **Walk:** a `fs.WalkDir` beneath `os.Root`, regular files only, links skipped, mtime only,
  no `Nlink` test ([`cachepurge.go`](../../internal/prune/cachepurge.go#L120-L169)).
- **Budget:** measured only **after** the full dry-run walk. The debounce stamp is written
  even when the walk was partial, and the apply pass is an unbounded second walk
  ([`housekeeping.go`](../../internal/cli/run/housekeeping.go#L326-L350)).
- **Manual purge:** has no budget ([`prunecmd.go`](../../internal/prune/prunecmd.go#L1120)).
- **Global dedup:** includes `cache` ([`prune.go`](../../internal/prune/prune.go#L35)), so
  today's cache already holds files with `Nlink` > 1 that yolo itself linked.
- **Existing interruptible walkers:** [`walk.go`](../../internal/cli/stores/walk.go#L84-L91)
  and [`miseversions.go`](../../internal/prune/miseversions.go#L451-L470) both stop at a
  deadline with `SkipAll`.

**A run fixture shows the age list is not a classifier.** A throwaway test called
`PurgeCacheByAge` with `apply=true` on files dated 40 days back. The default buckets deleted
`uv/.lock`, and the opt-in heavy buckets deleted `huggingface/token`. That path is where
`huggingface_hub` keeps its login token by default (upstream knowledge, not source). This is
**shipped behavior** for anyone who opted into heavy purge. It is independent of isolation,
and the owner may want a separate fix. A separate fix on 2026-10-09 makes today's purge hold the
Credentials row and lock files (`*.lock`, `.lock`, `LOCK`, `*.lck`, plus uv's control files), and
bounds its walk and its deletions by the 60-second budget with CI-D7's batched read, writing the
launch's debounce stamp only when the measurement completes. The positive class table below is
still unimplemented: everything it does not hold is still purged by age.

**Proposed classes,** relative to a bucket root. Every row marked disposable also needs a
regular file, no link following, `Nlink == 1` and a recheck under the guard. Tool layouts are
upstream knowledge, and each must be confirmed on disk before its row is admitted.

| Class | Shape | Rule |
| :--- | :--- | :--- |
| Go build cache | `go-build/<2 hex>/<hash>-{a,d}`; same under `gopls/` | Per file, age. Go refreshes mtime on use. |
| Go bookkeeping | `go-build/{README,trim.txt,testexpire.txt}` | Hold. |
| npm content and index | `npm/_cacache/{content-v2,index-v5,tmp}/**`; `npm/_logs/*` | Per file, age. A missing entry is a cache miss. |
| npm `_npx` | `npm/_npx/<hash>/` | Whole entry by newest mtime, or hold. Per-file deletion breaks an installed tree. |
| pip | `pip/{http-v2,http,wheels}/**`, `selfcheck/*.json` | Per file, age. |
| uv indexes | `uv/{simple-v*,wheels-v*,sdists-v*,interpreter-v*}/**` | Per file, age. |
| uv archive and control | `uv/archive-v0/**`, `uv/{.lock,CACHEDIR.TAG,.gitignore}` | Hold. Environments hardlink into the archive, and the lock is live. |
| Unpacked trees | `nce/<sha>/`, `node-gyp/<ver>/`, `pex/{installed_wheels,venvs,unzipped_pexes}/<h>/`, `ms-playwright/<browser>-<rev>/` | Whole entry or hold. Archive mtimes and completion markers make per-file age unsafe. |
| Databases | `pants/**`, any `*.mdb`, `*.db`, `*.sqlite` | Hold. A daemon may have them open. |
| mise downloads | `mise/<tool>/*` archives and version lists | Per file, age. |
| Hugging Face blobs | `huggingface/hub/models--*/` | Whole model entry or hold. Snapshot links dangle otherwise. |
| Credentials | `huggingface/{token,stored_tokens}`, and any name containing `token` or `auth` | **Always hold.** |
| Anything unlisted | — | Hold, and report as unknown. |

**Bounded traversal** ([CI-D7](cache-isolation.md#10-decision-ledger)):

1. Read directories in batches of 256 with `ReadDir(n)` beneath the `os.Root`, replacing the
   `fs.WalkDir` at [`cachepurge.go`](../../internal/prune/cachepurge.go#L125).
2. Check the context and deadline before each batch and again inside the guarded unlink.
3. Return a `partial` flag, and use it instead of the elapsed-time label after the walk.
4. Give the apply pass and the manual purge the same deadline, and have both print "partial".
5. Write no completion stamp on interruption.

## Protected-source selector, callers and smaller controls

Proposed names below are **not implemented or accepted**. [The pinned upstream trace](../research/cache-trust-and-reclamation.md#runtime-delivery-retains-a-pathname-boundary)
justifies a stable ordinary pathname; it does not certify the deployed OCI/VZ callee.

| Proposed seam | Exact caller/control to prepare after policy and support gates |
| :--- | :--- |
| New `paths.OrdinaryCacheRoot()` → `<GlobalStorage>/ordinary-caches`; new run-side `admitOrdinaryCache` returns the checked leaf plus full scope/backing identity | Keep root/scope parents host-only; verify all effective grants do not expose authority or siblings. Records/locks stay outside the leaf. Unit fixtures reject forged identity, source-parent links, relocated/legacy ancestors and an exposed parent; directory naming never supplies authority. |
| Move ordinary resolution before [`relocation provisioning`](../../internal/cli/run/run.go#L1893-L1908) and [`alias preparation`](../../internal/cli/run/run.go#L1924-L1927) | Pass that leaf into `EnsureCacheRelocations` and `planHostCASAlias` instead of their global-cache assumptions. `goldenOptions`/`hostcasalias_test.go` controls must fail if actual call-site delivery is removed, including the empty-host/warm-private distinction. Do not touch `sealedStores` or widen exceptions. |
| [`runContainer` explicit selection](../../internal/cli/run/run.go#L1989-L2045) → `assembleInput.cacheDir` → [`cacheSource`](../../internal/cli/run/assemble_parts.go#L111-L115) → both emitters | Future `ordinaryCacheDelivery` run-path fixture uses two scopes, same guest path, restart and differing leaf sources. Deleting the production selector or substituting its current global fallback must fail. Assert neither parent nor legacy cache contains new authority; sealed/legacy attach controls retain their actual sources. |
| Host-only source protection through real backend reopening | Later authorized smallest backend fixture: harmless sentinel, delayed create/open, guest attempts replacement via workspace and legacy cache; verify the runtime received the admitted leaf and guest `..` cannot reach its host parent. Linux rootless requires a real rootless host; Apple requires macOS. No interactive agent or API. |
| Native source admission → env/profile/generator crossings | First prove selected-leaf writability and only necessary ancestor traversal without parent enumeration/replacement or sibling access. If existing authority cannot supply that, retain the support stop. Fixed HOME, real legacy `.cache`, hardcoded-path and surviving-writer obligations remain separate. |

The standalone fixture illustrates opened-FD versus early plain-path reopening and the
proposed exposure topology; exposed-parent and legacy-ancestor negative controls fail.
It does not implement mount confinement. The irreducible FD alternative gap is the
**selected OCI runtime/version and descriptor namespace/lifetime**, plus Apple's opaque VZ
opening after string-only XPC. Do not substitute `/proc/self/fd` or `/dev/fd` argv from this
model; later deployment must refuse unprotected delivery, not fall back to shared bytes.

## Tests lead the later implementation, not this document stage

Use source-shaped harmless fixtures. The packages holding the existing fixtures below passed
`go test -short` at `8f52d3561`. Proposed cases are not yet in the repository and have no red
or green result.

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

These checks are later authorized work only. Run them on a throwaway fixture and an existing
provisioned backend. Run no agent or API, and inventory no real credential or cache. Do no
install or teardown. Linux runs cannot settle them. Report `podman info --format
'{{.Host.Security.Rootless}}'`, or the macOS version and architecture, with each result.

**Backend delivery (V1, V4):**

- **R1. Rootless Linux host or CI.** Rerun the V1 model
  (`podman run --rm -v <leaf>:/home/agent/.cache …`) without `--userns=host`:
  1. Report whether the guest can write the leaf through the actual ID mapping.
  2. Report the owner the host sees on a guest-created file.
  3. Confirm that `..` and T3–T5 behave as in V1.
  4. Confirm that a host-user `unlink` of guest bytes works without `podman unshare`.
- **R2. Podman Machine.**
  1. From the Mac, run `podman machine ssh -- ls -ld <leaf> <leaf>/..`, then the V1 model.
  2. Confirm the VM sees the leaf at the same path.
  3. Confirm that `podman inspect` reports that path as `Mounts[].Source`, as V4 assumes.
- **R3. Apple Container.**
  1. Bind the leaf nested under a writable `wsState` home.
  2. Confirm that the guest's `..` from the leaf stays in the guest, and that `container inspect`
     names the source path.
  3. Confirm that a guest `mv` of the mountpoint cannot alter the host leaf.

**Native anchor (V3):**

- **N1.** Run `sudo -u _yolojail ls -ld /Users/<host> ~<host>/.local/share/yolo-jail`. A
  `Permission denied` confirms the host-home root is unusable natively.
- **N2.** Stage `/var/yolo-jail/ordinary-caches/<k>/scopes/<s>/cache` with root-owned
  parents. Under a real session profile with the write-allow and read-deny added, check four
  things:
  1. The guest can write the leaf.
  2. `touch`, `mv` and `ls` on the parent and a sibling scope give `Operation not permitted`.
  3. A sibling's sentinel is unreadable.
  4. A link inside the leaf to the sibling does not read through.
- **N3.** Check that a guest-created file in the leaf, under the inheriting shared-group ACE,
  can be deleted by the host user without sudo.
- **N4.** Run `ls -ld ~/.cache` in the account home. It must stay a real directory with its
  relocation links intact, and legacy bytes there must be unreadable under the new profile.

**Native quiescence (V5):**

- **A1.** As the host user, without sudo, run `ps -U _yolojail -o pid,ppid,pgid,command`
  during a session. Compare it with the same command under `sudo`.
- **A2.** Start a session, `kill -9` the host `yolo`, then run A1 again. Repeat with
  `kill -9 -<supervisor pgid>`. Report every survivor.
- **A3.** Inside the sandbox, try `launchctl submit -l x -- /bin/sleep 999` and a plist in
  `~/Library/LaunchAgents`. Then on the host run `sudo launchctl print user/$(id -u _yolojail)`
  and `sudo crontab -u _yolojail -l`.
- **A4.** Run `ps -o pid,pgid,tpgid,command -U _yolojail` during a session to learn the
  agent's process group.
- **A5.** Show that a machine-wide fence works: a root-staged lock file under `/var/yolo-jail`,
  opened read-only by the host user, must exclude a second host user's launch and `yolo capture`.

**Native addressing (the original six obligations):**

1. Place distinct harmless legacy, selected-A and selected-B sentinels.
2. Through the **actual** launch env and generated profile, report the npm cache path,
   `go env GOCACHE` with `GOCACHE` unset and set, and an XDG-obeying probe. Then run direct
   `$HOME/.cache` and `~/Library/Caches/go-build` read and write probes. Do not download or
   compile.
3. Private overrides and stamps must use the selected backing, and legacy content must not be
   read. A hardcoded-path probe must fail with its next step. If its only successful run reads
   legacy bytes, the candidate fails.
4. Keep A live while launching B. The cross-workspace refusal must remain, and A's addresses
   and links must not change. Two sessions of A need distinct env files and holds.
5. Try a final-env override of `XDG_CACHE_HOME`, `NPM_CONFIG_CACHE` and `GOCACHE` through
   dotenv, plus link and root replacement and relocation-target traversal under the actual
   profile. Old opaque bytes and explicit sharing targets must remain untouched.
6. After a safe exit, native inventory, manual purge and the housekeeping slot must reach the
   same backing. They must skip live and unknown scopes, stop at the budget and keep opaque and
   credential-shaped fixtures.

## Build hand-off under either ruling

**Ready to start once both rulings land.** It is not authorized before then. Each slice
lands with its failing test first and a control that fails when its call site is deleted.
Re-map anchors against the then-current tree before starting.

| Slice | Production change | Proves | Backends |
| :--- | :--- | :--- | :--- |
| **S1. Root and records** | `paths.OrdinaryCacheRoot()`; host-only `<ws-key>/{record,admission.lock,attempts/,scopes/<scope-id>/cache}` ([CI-D1](cache-isolation.md#10-decision-ledger), [CI-D2](cache-isolation.md#10-decision-ledger)); beneath-root create; refuse a link or a foreign owner on every component | Forged and linked components refuse; a case alias joins only on identical device, inode and folded path | All |
| **S2. Grant refusal** | Launch-time refusal of the V2 grants ([CI-D3](cache-isolation.md#10-decision-ledger)) | A read-only `mounts` or pack `mount` of the root, an ancestor or a sibling scope refuses and names the grant | All |
| **S3. Fence and attempts** | Admission mutex, dispatched-before-submit and the container predicate ([CI-D5](cache-isolation.md#10-decision-ledger)) wired through `runContainer`, keeper and attach | Empty running poll, crash between dispatch and acknowledgment, and a stopped container all keep the hold | Podman, then Apple Container |
| **S4. Delivery** | Select once in `runContainer`; pass the leaf to `assembleInput.cacheDir`, relocations ([CI-D8](cache-isolation.md#10-decision-ledger)) and the host-CAS plan; attach reports the inspected source | Two-workspace sentinel isolation, warm restart and legacy attach honesty. Deleting the selector fails the test. | Podman, then Apple Container |
| **S5. Inventory and reclaim** | Host-owned discovery; class table and bounded walk ([CI-D7](cache-isolation.md#10-decision-ledger)); manual and slot callers; dedup exclusion; no stamp on partial | Budget exit, live and unknown held, credentials held, no cross-scope inode | Podman, then Apple Container |
| **S6. Native** | `/var/yolo-jail` anchor, profile rules, env and stamp addressing, native predicate ([CI-D4](cache-isolation.md#10-decision-ledger), [CI-D6](cache-isolation.md#10-decision-ledger)) | N1–N4 and A1–A5 pass. Otherwise refuse the native launch with the container-backend remedy. | macos-user |

S1 through S5 ship together as one vertical slice, under P5. S6 may follow, but until then
a native launch is **refused**; it never falls back to the shared account `.cache`.

**What each ruling changes.** Every slice above is the same under either answer except these
rows.

| Ruling | Scope id | Reclaim and inventory | Docs to revise first |
| :--- | :--- | :--- | :--- |
| [OQ-CI1](cache-isolation.md#OQ-CI1) **A**, workspace | A fixed `w` under each `<ws-key>` | Age classes in an inactive scope | None |
| [OQ-CI1](cache-isolation.md#OQ-CI1) **B**, per launch | The attempt id. A container restart and every attach of that container reuse it (T5; attach joins the running container) | An inactive scope's classified bytes may go whole. Opaque bytes still hold. | [Design §3](cache-isolation.md#3-the-recommended-scope-and-identity) warm restart, [§8](cache-isolation.md#8-costs-alternatives-and-observable-completion) completion list |
| [OQ-CI2](cache-isolation.md#OQ-CI2) **A**, policy | — | Existing offer and consent | None |
| [OQ-CI2](cache-isolation.md#OQ-CI2) **B**, guarantee | — | Needs a follow-up design. The tree has no storage-quota mechanism (only `cpu.max` in `cgd`), and rootless podman and macos-user have no unprivileged per-directory quota (upstream knowledge). | Revisit [OQ-DF4](minimal-disk-footprint.md#OQ-DF4) |

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

Do not build before both rulings. A non-cosmetic disagreement goes back to the design;
missing symbols or someone else's dirty seam requires a fresh source map, not a patch-around.
No runtime/CLI/config change, new size knob, broad host grant or administrative cleanup is
licensed here. The two policy anchors remain the only owner calls; source preparation and
later native evidence obligations stay explicit rather than making all investigation wait.
