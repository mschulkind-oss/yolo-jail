---
title: "A writable cache is a cross-workspace trust channel"
status: accepted
stage: CURRENT
next: "Prove native hardcoded-path disposition and consumer quiescence; resolve pathname-to-runtime delivery before promoting the sketch"
tags: [research, storage, security]
---

# A writable cache is a cross-workspace trust channel

Re-analyzed **from source, 2026-10-09**, against `b2eeffc12`.
This is a repository audit, not an inventory of a user's caches or runtime evidence.
No tests, builds, installs, native probes or cleanup were run.

**Verdict:** recommend persistent workspace-private backing for ordinary jail caches,
with inspection and reclamation delivered together. This is not a selected mechanism;
[the design](../design/cache-isolation.md) owns the policy questions and
[its companion sketch](../design/cache-isolation-plan.md) owns the source map.
Yolo manages an agent's environment; this proposal narrows one shared surface of its jail.

## The actual source selection, not just a fallback

| Surface | Current behavior and evidence |
| :--- | :--- |
| Machine cache | `GlobalCache()` returns `<GlobalStorage>/cache`; initialization creates it before config loads. [`paths.go`](../../internal/paths/paths.go#L860-L883), [`ensure.go`](../../internal/storage/ensure.go#L24-L70) |
| Ordinary containers | `runContainer` explicitly selects `GlobalCache()` into `assembleInput.cacheDir`. Both Podman and Apple Container bind it writable at `/home/agent/.cache`; the assembler's empty-value fallback selects it too. Changing only that fallback misses production. [`run.go`](../../internal/cli/run/run.go#L1986-L2042), [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go#L54-L172) |
| Sealed builds | `sealedStores` creates `build-cache` and `build-mise` in the build workspace. The seal withholds relocations, host aliases and machine-scope pack dirs. This is a built precedent, not ordinary-jail isolation. [`seal.go`](../../internal/cli/run/seal.go#L18-L43), [`sealedStores`](../../internal/cli/run/seal.go#L169-L181) |
| Native macos-user | The home layout links workspace state but leaves `.cache` in the sandbox account home. Relocation explicitly requires that root to be a real directory and checks its opened identity. [`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go#L21-L28), [`InstallDarwinCacheRelocations`](../../internal/entrypoint/darwinhomelayout.go#L811-L886) |
| Cache environment | Container npm uses `.cache/npm`; mise downloads use `/tmp/mise-cache`, not the persistent `.cache/mise` bucket named by purge. Shell and native bootstrap also default npm to `.cache/npm`. [`commonEnvBlock`](../../internal/cli/run/assemble.go#L1198-L1230), [`shell.go`](../../internal/entrypoint/shell.go#L249-L254), [`bootstrapTemplate`](../../internal/entrypoint/shell.go#L387-L391) |
| Separate warm state | Node and declared temporary compile caches already live under workspace-private `.local/state/yolo/compile-cache`, with launcher age cleanup. They are not the ordinary `.cache` source. [`compilecache.go`](../../internal/entrypoint/compilecache.go#L8-L89) |
| Persistence description | Ordinary `.cache` is reported machine-durable; sealed `.cache` is workspace-durable. Native has no persistence map from this helper. [`persistencemap.go`](../../internal/cli/run/persistencemap.go#L93-L162) |

The default shared cache is **yolo-owned jail storage**, not the host user's ordinary
`~/.cache`. Nevertheless, siblings can read, overwrite and delete each other's bytes.
An identical guest path does not identify an identical workspace: the
[documented Claude MCP-log key](../reference/jail-home.md#sharing-semantics)
collapses every container's `/workspace` into `-workspace`.

## Native addressing: compatible crossings, incomplete isolation

| Consumer / crossing | Source result, not native execution |
| :--- | :--- |
| `HOME` | Fixed `/Users/_yolojail`, protected from composed env overrides. A private cache must not relocate the whole home or credentials. [`sandboxEnvPairs`](../../internal/macosuser/macosuser.go#L1185-L1230), [`protected names`](../../internal/macosuser/envfile.go#L293-L315), [one-home ruling](../reference/macos-user-home-tiers.md#oq-ht4) |
| Session cache env | `XDG_CACHE_HOME`, `NPM_CONFIG_CACHE` and `GOCACHE` are not protected names today; the per-session root-owned env file can carry them to agent/provisioning. Bootstrap hydration sets generator variables, not process env; its own argv keys win. A future managed cache must reach both crossings and reject conflicting final values. [`envfile.go`](../../internal/macosuser/envfile.go#L100-L133), [`boot.go`](../../internal/entrypoint/boot.go#L318-L375) |
| npm | Shell/bootstrap/launchers use `NPM_CONFIG_CACHE` when set, otherwise `$HOME/.cache/npm`. Prefix/install-lock paths are separate; moving them is not cache work. [`shell defaults`](../../internal/entrypoint/shell.go#L249-L254), [`bootstrap`](../../internal/entrypoint/shell.go#L387-L391), [`launcher`](../../internal/entrypoint/shims.go#L1370-L1406) |
| Go build cache | Go 1.26.7 source inspection confirms Darwin ignores `XDG_CACHE_HOME` for its default: `HOME/Library/Caches/go-build`; absolute `GOCACHE` overrides it. The repository already names that relocation gap in [`ctxlinks.go`](../../internal/macosuser/ctxlinks.go#L416-L440). No Go command ran. |
| Yolo launcher stamps | Agent/package-manager stamp directories are generated from `e.Home/.cache`, not XDG. Changing npm's env alone leaves these shared. Per-session roots must not be baked into launchers later regenerated by a sibling session. [`agent stamps`](../../internal/entrypoint/shims.go#L475-L481), [`manager stamps`](../../internal/entrypoint/shims.go#L902-L907) |
| Arbitrary hardcoded/native paths | `~/.cache` and `~/Library/Caches` are currently readable/writable through the broad account-home grants. No env setting supplies a mount namespace or redirects those consumers. [`seatbelt.go`](../../internal/macosuser/seatbelt.go#L158-L228) |

`InstallDarwinCacheRelocations` still requires **real** account `.cache` and writes its
children beneath a checked root; it is not compatible with blindly linking that root
into a workspace. Its link manifest is guest-writable, not cache ownership authority.
[The relocation boundary](../../internal/entrypoint/darwinhomelayout.go#L811-L886) stays.

Native overlap is constrained already: [the account-home hold](../../internal/cli/run/accounthomehold.go#L3-L36)
refuses another workspace while a host session holds the home; two same-workspace
sessions share it. It explicitly loses the hold if host yolo dies while the guest
survives, and it sees only the invoking host user's holds. Neither a free flock nor
one shared symlink proves that two scopes can use that name safely. The existing
[concurrent-workspace question](../reference/macos-user-home-tiers.md#oq-ht6) is not reopened here.

**Preparation conclusion:** per-session env plus physical-path profile rules are useful
seams, not a complete native delivery. Overriding known consumers and denying retained
legacy cache is a candidate to test, not proof that arbitrary hardcoded tools work.
[The design](../design/cache-isolation.md#native-delivery-adjustment-under-investigation)
keeps native support conditional without inventing an all-home migration.

## Why “content-addressed” does not settle the trust question

A content-addressed store uses a digest of bytes as their key; checking the digest
rejects different bytes under that key. That does not authenticate a mutable index
which selects the key for a URL, project or build action.

[`hostcas.Stores`](../../internal/hostcas/hostcas.go#L159-L209) records the exclusions:
npm's request-URL index and Go's writer-chosen action-ID index let a writer choose both
the lookup and matching content. uv/pip have index-keyed buckets too.
Concurrent-write compatibility is not protection against an adversarial writer.

**The existing exception is Pants `lmdb_store` alone**, under
[the 2026-09-08 ruling](../design/disk-levers-and-backfill.md#OQ-BF10).
It is writable host-tool storage, gated on Linux Podman, matching OS/architecture,
presence, writability, no relocation of its segment, and an empty host store only when
it does not hide a warm private copy. An empty host store with nothing warm to hide still aliases.
Declines preserve the private copy; they never refuse launch.
[`the cold-start guard`](../../internal/hostcas/hostcas.go#L425-L442),
[`the empty-store control`](../../internal/hostcas/hostcas_test.go#L249-L262),
[`hostcasalias.go`](../../internal/cli/run/hostcasalias.go#L28-L98).
Content integrity does not prevent deletion, reading or disk exhaustion through that exception.

Two blanket claims are unsafe bases for a new design: the
[storage reference's concurrent-writer paragraph](../reference/storage-and-config.md#what-is-shared-and-writable-and-what-serializes-it)
calls cache and mise content-addressed, and the
[purge-list comment](../../internal/prune/cachepurge.go#L10-L27) calls its buckets pure CAS.
Those files are unchanged by this research; eventual implementation must reconcile their wording.

## Reclamation exists, but its safety and coverage are narrower

| Concern | What the current source establishes |
| :--- | :--- |
| Coverage | Named default buckets, separate opt-in heavy buckets, and forbidden profile/installed-program names. Only old regular files are counted/deleted; unknown content is not classified. [`cachepurge.go`](../../internal/prune/cachepurge.go#L10-L66) |
| Path safety | Descendant removals run beneath `os.Root`, with links skipped. The current global/relocated root itself is opened following a link; workspace-private roots need a stronger admission check because the workspace is writable. [`cachepurge.go`](../../internal/prune/cachepurge.go#L78-L169), [`wsstatebeneath.go`](../../internal/cli/run/wsstatebeneath.go#L16-L127) |
| Liveness | Cache purge receives no live-cache set. Housekeeping rechecks file age under its deletion guard; jail tools do not take that host lock. Manual purge uses the unguarded wrapper. [`housekeeping.go`](../../internal/cli/run/housekeeping.go#L326-L375), [`guard.go`](../../internal/prune/guard.go), [`prunecmd.go`](../../internal/prune/prunecmd.go#L1100-L1131) |
| Walk budget | `cacheWalkBudget` is 60 seconds, but the complete dry-run returns before elapsed time labels its result partial. It does **not** interrupt the walk or bound the following apply pass. [`housekeeping.go`](../../internal/cli/run/housekeeping.go#L326-L382) |
| Consent | Offer at 1 GiB; yes becomes standing consent, not-now waits seven days, never stops asking. Age defaults to 30 days. These are existing values, not a size ceiling. [`offer.go`](../../internal/cli/run/offer.go#L24-L126) |
| Inventory | Container cache coverage is derived from the purge lists. Native account `.cache` is listed as unreclaimed; its housekeeping instead purges the separate container cache. [`inventory.go`](../../internal/cli/stores/inventory.go#L519-L584), [`native rows`](../../internal/cli/stores/inventory.go#L1256-L1288), [`native slot`](../../internal/cli/run/housekeeping.go#L248-L281) |
| Discovery | `FindYoloWorkspaces` discovers runtime-visible containers, not every exited workspace. Native session records supply current liveness, not a durable cache registry. [`probes.go`](../../internal/prune/probes.go#L240-L273), [`macosusersessions.go`](../../internal/runtime/macosusersessions.go#L80-L109) |
| Dedup | Global hardlink dedup includes `cache`; workspace dedup already excludes embedded pack trees because a shared inode restores cross-workspace writes. Private writable caches need the same exclusion in walk and apply. [`prune.go`](../../internal/prune/prune.go#L27-L46), [`exclusion rationale`](../../internal/prune/prune.go#L154-L213) |

Age cleanup cannot guarantee a byte bound: hot files, opaque bytes, declined consent
and unknown/live scopes remain. Ownership plus inactivity also cannot prove that
cookies, tokens, browser profiles or unfamiliar vendor state are regenerable.

### Why existing liveness cannot authorize cache removal

The [session count](../../internal/cli/run/sessionlock.go#L148-L165) can warn and proceed
uncounted. Native [record creation](../../internal/macosuser/sessionfiles.go#L331-L350)
also warns; a released launcher lock proves nothing about surviving guest descendants.
Even the keeper's [running frame](../../internal/cli/run/keeper.go#L700-L720) is emitted
after a bounded poll whether or not that poll found the container. Do not use the slot
callback as cache admission acknowledgment. A new durable dispatched-attempt hold must
survive launcher/keeper loss until backend quiescence is proved; free locks and timestamps
alone cannot clear it. [The conditional protocol](../design/cache-isolation.md#the-launchreclaim-fence)
therefore differs from the existing courtesy locks and session-file sweeps.

## Prior rulings constrain the proposal

- [OQ-DF4](../design/minimal-disk-footprint.md#OQ-DF4), **2026-10-05**:
  policy, no budget key; the residual shared mise versions join offered cleanup.
  Duplicated private working sets are a new cost, not evidence that this ruling chose a cap.
- [OQ-BF1/BF2](../design/disk-levers-and-backfill.md#111-decision-ledger), **2026-09-08**:
  offered age purge, automatic only after consent; heavy re-fetch remains opt-in.
- [Cache relocation's three held questions](../plans/cache-relocation.md#open-questions)
  concern automation, host reflection and broader sharing, not a default isolation mechanism.
  [Shared-tool placement](../design/shared-tool-store-relocation.md),
  [bulk scratch](../design/storage-tiers.md) and
  [VM-local cache placement](../design/vm-local-volumes.md#OQ-VL2) stay independent.
- [XB-D12](../design/pi-extension-store-builds.md#XB-D12), **2026-10-05**, forbids
  shared npm caches for sealed extension builds; it does not choose ordinary-jail scope.

**Next source work:** specify protected delivery of the admitted directory across the
runtime's pathname reopen; test native cache overrides, hardcoded-path denial and writer
quiescence independently. Env-only native parity is not established. The companion maps
these controls to actual callers; native experiments require separate authorization.
