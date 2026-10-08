---
title: "In-jail Nix roots: a watcher keeps a jail's links as bounded, workspace-owned roots"
date: 2026-09-28
status: in-review
tags: [nix, gc-roots, podman, mount-namespaces, storage, design]
summary: "A podman jail's nix roots are recorded under the jail's spelling, which the host deletes as stale. An in-jail root watcher sees each root request in a read-only view of the host's gcroots/auto and keeps the link as a yolo-owned managed root under the host's spelling, with a 7-day lease, a 64-root workspace cap, inspection and release (`yolo nix-roots`)."
stage: BUILT
next: "Measure the watcher end to end in a fresh jail on a host with this yolo installed: a `nix build` in /workspace, then `yolo nix-roots list` and the host's `nix-store --query --roots`"
vantage:
  status-chip: true
---

# In-jail Nix roots: a watcher keeps a jail's links as bounded, workspace-owned roots

**Status:** 2026-10-08. Built: the root watcher, the managed-root registry with its lease, cap
and release, and `yolo nix-roots` ([§8](#8-what-is-built)). Owner rulings: automatic protection
with a mandatory lifecycle, cap, inspection and release ([OQ-NR1](#decision-ledger)), and a
read-only view of the host's `gcroots/auto` with no launch line of its own
([OQ-NR2](#decision-ledger)). The mechanism and the numbers are implementation decisions
[NR-D3](#NR-D3) to [NR-D7](#NR-D7). MEASURED against the host daemon: a kept link is a root
the host lists under its own spelling, and releasing it removes that root. UNMEASURED: the watcher
receiving a real daemon's entries through the bind, because this jail's launcher predates the bind.

> **In short.** The watcher sees each root request the host daemon records, and for a link that
> exists in this jail it makes a yolo-owned link to the same store path, registered under the
> host's spelling. That link is what the host keeps, for a bounded time, and yolo can list and
> release it.

**Why it matters.** Without it, a `result` link, a profile generation or a nix-direnv cache made in
a jail is not a root the host honors, so a host garbage collection can delete its target between
two commands. Keeping every such link forever would instead let a forgotten workspace pin host disk.

**The shape.** A *managed root* (coined in [§4](#4-the-translated-root)): a link under
`<workspace>/.yolo/nix-roots/links/` that yolo owns and can delete. One exists per jail link nix
was asked to root. It lives 7 days after its last renewal, a workspace holds at most 64, and it
goes when the jail's own link goes.

**Cost.** One read-only bind, one in-jail process, and one directory of links per workspace. A
GC that lands in the milliseconds before the watcher registers a root can still collect it; a
rebuild restores it ([Known limit](#known-limit)).

**Start at [§1](#1-verdict).**

**Needs your ruling:** None.

**Reads with:** [implementation sketch](in-jail-nix-roots-plan.md) (superseded by the build),
[`workspace-path-mirroring.md`](workspace-path-mirroring.md) (no mirroring needed),
[`nix-across-backends.md`](../reference/nix-across-backends.md) (backend boundaries).

---

## 1. Verdict

**Option C with A as its hand-run form, over managed roots** ([NR-D3](#NR-D3)). The owner's
ruling asked for automatic translated-root protection with a lifecycle, a cap, a way to see a
workspace's roots and a command to release them ([OQ-NR1](#decision-ledger)). This is how it
is built:

1. **The root watcher triggers it.** An in-jail process watches a read-only bind of the host's
   `/nix/var/nix/gcroots/auto` ([OQ-NR2](#decision-ledger)). The daemon writes one entry there
   for every indirect root any client asks for, so the watcher covers every root-making tool at
   once, and it sits in no nix command's path ([§5](#5-what-registers-the-translated-root)).
2. **A managed root holds the store path, not the user's link.** The daemon has no operation that
   drops an indirect root, so a root registered at the user's own link would last exactly as long
   as that link, with no lease and no release. A link yolo owns can be deleted, so that is what is
   registered ([§4.1](#41-workspace-ownership-caps-and-release)).
3. **The handoff is fenced.** The watcher pins the store path with a temp root the moment it reads
   the entry, registers the managed link on the same connection, and pins again before closing
   ([NR-D7](#NR-D7), [§2.4](#24-a-new-permanent-root-does-not-update-an-active-gc-snapshot)).
4. **The lifecycle is bounded by count and time:** a 7-day lease from the last renewal, 64 roots
   per workspace, and release when the source link is deleted ([NR-D4](#NR-D4)).
5. **`yolo nix-roots`** lists, keeps, releases and prunes, on the host and in the jail.

The read-only bind prints no launch line ([OQ-NR2](#decision-ledger)). Unrelated trust banners
are unchanged.

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
The watcher and `keep` make this fence ([NR-D7](#NR-D7)). Its behavior against a concurrent GC
is argued from this source reading, not measured.

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


**The handoff, as built** ([NR-D7](#NR-D7)). For an entry the watcher accepts, and for
`yolo nix-roots keep`, one daemon connection carries the whole handoff:

1. `AddTempRoot(P)`, as soon as the jail link's target P is read. From here until step 5 the
   store path is pinned whatever the producing client does.
2. Check that P is still in the store. A path that is already gone is refused and logged; a
   rebuild roots it again.
3. Under the workspace registry's lock, make or repoint the managed link `links/<id>` → P.
4. `AddIndirectRoot(<host spelling of links/<id>>)`, then `AddTempRoot(P)` again once that is
   acknowledged. The second pin fences a GC that began before the managed link existed:
   [§2.4](#24-a-new-permanent-root-does-not-update-an-active-gc-snapshot) explains why the first
   pin alone does not.
5. Record the root and close the connection.

A registration the daemon refuses removes the managed link it just made and records nothing.
`Registrar.Root`, which yolo's own image and prefix roots use, is unchanged: it still replaces an
owned link, and it is never used to observe a user's link.

### 4.1 Workspace ownership, caps and release

A *managed root* *(coined here)* is a link yolo owns under the workspace's state directory, pointing
at the same store path as a link the jail asked nix to root, and registered as a translated root.
The user's own link is never touched. Releasing a managed root deletes only yolo's link; the host's
next GC may then collect the store path, exactly as it would after a host user deleted `result`.

- **Where it lives.** `<workspace>/.yolo/nix-roots/`: `roots.json` (the records), `links/<id>`
  (the managed links) and `.lock`. The id is the first 16 hex digits of the SHA-256 of the jail
  link's path. The host and the jail open the same directory, the host as `<workspace>/.yolo`, the
  jail as `/workspace/.yolo`.
- **One record per jail link.** A record holds the jail link's path in both spellings, the store
  path, the managed link's host spelling, and when it was admitted and last renewed. A link that is
  repointed (P to Q) moves its managed link to Q. P stays only if another root holds it.
- **Renewal.** A fresh entry from the daemon for the same link (a rebuild), or a `keep`. Seeing an
  entry again in a scan does not renew it.
- **Release.** Explicit (`yolo nix-roots release <id|link>…` or `--all`), or by the lifecycle:
  the lease lapsed, the cap was exceeded, or the jail's link was deleted or no longer points into
  the store. A managed link that no record names (left by a crash between link and record) is
  removed by `prune`.
- **Inspection.** `yolo nix-roots list [--format json]`: id, jail link, store path, how it was
  admitted, when it was renewed and when it expires.
- **The host deletes in a jail-writable directory**, so every access goes through an `os.Root` on
  a directory checked not to be a link (`paths.OpenStateDirRoot`). The only names it deletes are
  16-hex-digit ids, and only when they are symlinks. A record is data: no path in it is a path
  the host deletes. A `roots.json` yolo cannot parse is reported and never overwritten.

### 4.2 The numbers, and when they apply

The lease is **7 days** from the last renewal and the cap is **64 roots per workspace**,
least-recently-renewed released first ([NR-D4](#NR-D4)). They are constants
(`nixroots.DefaultLease`, `nixroots.DefaultCap`), not configuration.

- The cap is a **count, not bytes**. It bounds how many distinct closures a forgotten workspace
  can pin, not how large one is. `list` does not price closures.
- The lifecycle runs when the watcher starts, once an hour while it runs, at every admission, and
  on `yolo nix-roots prune`, on either side. **No host process enforces it while no jail of the
  workspace runs**, so a lease can outlast 7 days on an idle workspace until the next launch or
  `prune`. `release` and `prune` work on the host with no jail running.
- It is a cooperative retention policy, **not a hostile-agent quota**: an untrusted daemon client
  can already register permanent roots of its own.

## 5. What registers the translated root

| Option | Coverage | Verdict |
| :--- | :--- | :--- |
| **A — explicit keep** | A currently valid link, admitted with the same fenced handoff | **Built** as `yolo nix-roots keep`, the hand-run form of C |
| **B — CLI wrapper** | A parsed command set's named output links | Rejected: sits in every nix command's path, blind to profiles and nix-direnv |
| **C — host-auto watcher** | Every client that makes a root through `AddIndirectRoot` ([§2.2](#22-what-the-client-sends)) | **Built** ([NR-D3](#NR-D3)), with the limit in [§5.1](#51-what-option-c-can-actually-observe) |
| **D — mirrored root dir** | Only tools steered into a host-identical path | Rejected: the default `./result` stays uncovered |
| **E — producer integration** | A patched nix client calling yolo before it reports success | Rejected: needs a maintained nix fork in the image, and covers only that client |
| **F — local intent reconciliation** | Declared root locations | Not needed: the watcher's start-up scan covers what a stopped watcher missed |

### 5.1 What option C can actually observe

The watcher places its inotify watch **before** its start-up scan, so an entry made during the scan
arrives as an event. A queue overflow triggers a rescan; a watch that disappears ends the process,
and the next boot starts a new one. For each entry it reads the path string the daemon recorded, and
continues only if that path exists **in this jail** as a symlink straight into the store, lies under
a mount the launcher's map can spell for the host, and is not under the registry itself (its own
registrations land in the same directory).

Raw entries do not say which jail made them. Two jails with `/workspace/result` share one entry, and
each one checks its own `/workspace/result`. A jail can therefore renew its own unchanged link when
another jail rebuilds at the same path; the lease and the cap still bound that.

The window this design does not close is in [Known limit](#known-limit).

### 5.2 Supported producers and explicit limits

Every client that makes a permanent root through `IndirectRootStore::addPermRoot` reaches the
watcher: `nix build`/`nix-build --out-link`, `nix-store --add-root`, profile generation links
(`nix profile`, `nix develop --profile`), nix-direnv, the flake-registry cache. Profile generations
are kept one by one; the `profile` → `profile-N-link` alias is relative and never itself a root.
A link under `/tmp` or `/var/tmp` is not kept ([NR-D1](#NR-D1)), and neither is any link under a
read-only bind or a volume. A workspace mounted read-only cannot hold managed links, so nothing is
kept there.

`macos-user` shares host paths, so a root nix makes there is already one the host honors: no
watcher runs and `keep` says so. Apple Container mounts no host daemon. A jail started by a
launcher older than this binds no auto directory and runs no watcher; `keep` still works wherever
the map exists.

## 6. Alternatives rejected

| Alternative | Verdict |
|---|---|
| **Protocol proxy**: a yolo socket in front of the daemon that rewrites `AddIndirectRoot` in flight | Rejected. It is the only fully transparent option, but the worker protocol is not self-delimiting, so the proxy must frame every op, NAR streams included, for every protocol version. One bug breaks every nix command in every jail |
| **Mirror the workspace at its host path** | Out of scope here, and not needed. [`workspace-path-mirroring.md`](workspace-path-mirroring.md#OQ-WP1) asks whether a fourth absolute-path problem exists. This is one, but translated roots fix it without mirroring, so it does not move that verdict |
| **Trust the jail** (`trusted-users`) for `AddPermRoot` | Rejected. The root would still be created in the daemon's namespace at a path the jail must spell for the host, so translation is still needed. And a trusted client is root-equivalent on the host |
| **Temp-root keeper**: an in-jail process holding one connection and `AddTempRoot`ing what the jail references | Rejected as the primary mechanism. It protects only while the jail runs, needs a list of what to keep, and has no way to drop one root short of reconnecting |
| **Host-side reconciler** reading only `gcroots/auto` | Rejected. Raw entries contain no producing-jail identity, and the host cannot see a jail's paths to check them. The watcher is the same reader run inside the jail, where those paths exist ([§5.1](#51-what-option-c-can-actually-observe)) |
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

**The user-root protection** ([NR-D3](#NR-D3) to [NR-D7](#NR-D7), built 2026-10-08):

- **The bind.** A podman launch that mounts the host nix daemon, and is not sealed, binds the
  host's `/nix/var/nix/gcroots/auto` read-only at `/run/yolo/nix-gcroots-auto`
  (`hostGCRootsAutoMountArgs` in [`translatedroots.go`](../../internal/cli/run/translatedroots.go)).
  A launcher in a jail binds its own view on, so a nested jail watches the same host directory
  through its composed map. The bind is read-only, so the map records it as a mask. A host with no
  such directory gets no bind. No launch line ([OQ-NR2](#decision-ledger)).
- **The watcher.** `yolo-jaild nix-roots` ([`watch.go`](../../internal/nixroots/watch.go),
  [`watch_linux.go`](../../internal/nixroots/watch_linux.go)), started by the boot step
  `start_nix_root_watcher` ([`nixrootwatcher.go`](../../internal/entrypoint/nixrootwatcher.go))
  only when the bind and a map are both present. Its log is
  `~/.local/state/yolo-jail-daemons/nix-roots.log`. A lock file
  (`/tmp/yolo-nix-roots.lock`) keeps it to one process per jail.
- **The registry.** [`registry.go`](../../internal/nixroots/registry.go): the managed roots of
  [§4.1](#41-workspace-ownership-caps-and-release) and their lifecycle.
- **The pin.** `nixroots.Conn` and `Registrar.Pin`
  ([`daemon.go`](../../internal/nixroots/daemon.go), [`register.go`](../../internal/nixroots/register.go)):
  `AddTempRoot` beside `AddIndirectRoot` on one connection, for the handoff in
  [§4](#4-the-translated-root).
- **The command.** `yolo nix-roots list|keep|release|prune`
  ([`nixroots.go`](../../internal/cli/nixroots.go)). On the host, `keep` points at
  `nix-store --add-root`, and the other three work without a jail.
- **The briefing** tells a jail with the host daemon that its links under the workspace and home
  are kept, with the lease, the cap and the command, and that a link under `/tmp` is not.

MEASURED 2026-10-08, from a podman jail against the host daemon (Nix 2.35.2, `Trusted: 0`): a
`yolo nix-roots keep` of a fresh `builtins.toFile` probe made the host's `nix-store --query --roots`
list the probe under the host's spelling of its managed link. The daemon accepted the pinned
handoff's `AddTempRoot`/`AddIndirectRoot`/`AddTempRoot` sequence on one connection. After
`release --all` the query listed nothing.

UNMEASURED: the watcher receiving a real daemon's entries through the bind, because this jail's
own launcher predates the bind. A nested launch cannot measure it either: its launcher binds on
only what its own jail has. The unit tests drive the watcher's inotify loop against a fixture
directory, and the pin's operation order against a fake daemon. Concurrent-GC behavior of the
fence is argued from [§2.4](#24-a-new-permanent-root-does-not-update-an-active-gc-snapshot), not
measured.

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

**Not translated:** the prefix's `--out-link` (`image.JailPrefixOutLink`) still registers under
the jail's spelling, dead as before. The prefix root above backs the same store path, so a
translated copy of it would root nothing more.

The prefix's jail-spelled `--out-link` lies under a mapped bind and points into the store, so the
watcher now admits it as a managed root too: a second root on the same store path, costing one
place under the cap.

## Known limit

**A GC can still collect a root in the moment before the watcher registers it.** The daemon writes
the auto entry when the producing client sends `AddIndirectRoot`. While that client stays
connected, its own temp root holds the store path ([§2.3](#23-what-already-protects-a-jail)). The
watcher reads the entry within milliseconds and pins the path itself, so usually the two overlap
and nothing is exposed. A root is lost only when a host GC (`nix-collect-garbage`, or the daemon's
`min-free` auto-GC) runs in a gap of about that length. There are two ways:

- the producing client disconnects before the watcher's pin lands, and the GC scans in between;
- a GC or a root query deletes the jail-spelled entry before the watcher has read it.

**What the agent sees:** a dangling `result` or profile link (nix reports the missing store path).
The link has no row in `yolo nix-roots list` and no `kept` line in
`~/.local/state/yolo-jail-daemons/nix-roots.log`. **Rebuilding fixes it**: the rebuild makes a
new entry, and the watcher admits it. `yolo nix-roots keep <link>` keeps a link by hand and refuses
one whose store path is already gone. The briefing keeps the "if a link dangles, rebuild it" line.

If the watcher was not running, the next boot's scan admits the jail-path entries that are still in
`gcroots/auto`; an entry a GC or root query removed in the meantime is gone.

## Decision Ledger

Owner policy ([OQ-NR1](#decision-ledger), [OQ-NR2](#decision-ledger)) is built. The mechanism
and its numbers are implementation decisions [NR-D3](#NR-D3) to [NR-D7](#NR-D7), each reversible.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-NR1 | Owner aligned with automatic translated roots, with mandatory lifecycle, cap, workspace inspection and explicit release/cleanup; not indefinite retention dependent on agent memory. Numeric cap/lease and a reliable watcher were not chosen. Vantage comment `b6072a1d`, round 0 | 2026-10-07 | [§1](#1-verdict), [§4](#4-the-translated-root), [§5](#5-what-registers-the-translated-root) | 2026-10-08: [NR-D3](#NR-D3) to [NR-D7](#NR-D7) ([§8](#8-what-is-built)) |
| OQ-NR2 | Owner permits a read-only host `gcroots/auto` mount; no extra routine launch line. Unrelated trust banners unchanged. Vantage comment `51b328aa`, round 0 | 2026-10-07 | [§1](#1-verdict) | 2026-10-08: the bind at `/run/yolo/nix-gcroots-auto`, no launch line ([§8](#8-what-is-built)) |
| OQ-NR3 | Reversible implementation choice: binds only; anonymous `/tmp` and `/var/tmp` volumes do not translate. Provenance: [NR-D1](#NR-D1) | 2026-09-30 | [§4](#4-the-translated-root), [§7](#7-non-goals) | ✅; [§8](#8-what-is-built) |
| OQ-NR4 | Reversible implementation choice: yolo's own in-jail roots use translation as the first consumer; existing reapers remain responsible where links live. Provenance: [NR-D2](#NR-D2) | 2026-09-30 | [§8](#8-what-is-built) | ✅; user-root lifecycle remains unbuilt |
| <a id="NR-D1"></a>NR-D1 | *Implementation decision, [OQ-NR3](#decision-ledger).* **Binds only: the map holds the binds the launcher itself wrote, and the anonymous `/tmp` and `/var/tmp` volumes do not translate.** Those volumes are per-launch scratch that yolo deletes once the jail exits, so a root there could outlive nothing but the launch. A running process that uses the store path is already kept by runtime and temp roots ([§2.3](#23-what-already-protects-a-jail)). A nested launcher cannot learn a volume's host path, while the host launcher could from `podman volume inspect`, so translating volumes would give one link two answers depending on who launched. A nested jail's own roots are [NR-D2](#NR-D2)'s. Reversible: the host launcher can add its volumes to the map later | 2026-09-30 | [§4](#4-the-translated-root), [§7](#7-non-goals) | 2026-10-01: the map holds every mount the argv makes, and a volume, a tmpfs or a read-only bind is a mask, so a link under `/tmp` or `/var/tmp` translates to nothing ([§8](#8-what-is-built)). MEASURED in a nested launch: its `/tmp` workspace was a mask |
| <a id="NR-D2"></a>NR-D2 | *Implementation decision, [OQ-NR4](#decision-ledger).* **Yes: yolo's own in-jail roots become translated roots, the first consumer of the protocol client in [§4](#4-the-translated-root).** Three skips go, not the two the question names: `rootImageFn` (the image root, [`imageload.go`](../../internal/cli/run/imageload.go)), the in-jail skip of `image.RegisterPrefixRoot` ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)), and the in-jail skip of `rootExtrasProfile` for the store-delivered packages' extras profile ([`storepackages.go`](../../internal/cli/run/storepackages.go)). All three root under `paths.BuildDir()`, which in a jail is inside `/home/agent/.local`, a mapped bind, so each one translates. This consumer needs no trigger from [OQ-NR1](#decision-ledger), because yolo registers its own links directly. **Reaping stays with the existing reapers, run where the links live.** A translated root dies with its link. A nested launcher's `yolo prune` sweeps its own `build/roots` and `build/prefix-roots` under the same retention rules the host's sweep follows ([`OQ-LS1`](../reference/image-retention.md#why-its-this-way)'s week, [`OQ-LS4`](../reference/image-retention.md#why-its-this-way)'s liveness), so nothing new reaps. The leaning's concern holds, and it is why this is so: the host's reapers never walk a workspace's home overlay, so the jail that made a link reaps it. What remains is [§4](#4-the-translated-root)'s *who can hold host disk*: a nested jail's root holds its closure until that jail's own sweep removes the link. Reversible: keep the skips | 2026-09-30 | [§8](#8-what-is-built) | 2026-10-01: `internal/nixroots` and `gcRooter` ([§8](#8-what-is-built)). Four skips went, not three: the store-delivered packages' own profile root (`storeProfileRootLink`) was skipped in-jail too. A fifth root is the same principle: the image copier's out-link, dead in a jail, is registered again as a translated root after an in-jail build (`AutoLoadOptions.RootCopier`). MEASURED for the image and prefix roots in a nested launch, and for the copier's root with its build driven directly; the two profile roots are pinned by the unit tests only. Nothing reaps `build/package-roots`, on the host or in a jail, so a nested jail's two profile roots last until their links are deleted, as the host's do |

| <a id="NR-D3"></a>NR-D3 | *Implementation decision, [OQ-NR1](#decision-ledger).* **Option C, with A as its hand-run form, over managed roots.** The watcher reads the daemon's own record of each root request, so it covers every root-making client with nothing in nix's path. A wrapper (B) or a patched client (E) covers only what it wraps, and E needs a maintained nix fork in the image. Managed links, rather than registering the user's own link under the host's spelling, because the daemon cannot drop an indirect root: only a link yolo owns can expire, be capped or be released, which OQ-NR1 requires. Reversible: delete the watcher and keep `keep` | 2026-10-08 | [§1](#1-verdict), [§4.1](#41-workspace-ownership-caps-and-release), [§5](#5-what-registers-the-translated-root) | 2026-10-08 ([§8](#8-what-is-built)) |
| <a id="NR-D4"></a>NR-D4 | *Implementation decision, [OQ-NR1](#decision-ledger).* **A 7-day lease from the last renewal and a 64-root cap per workspace, least recently renewed released first.** A week covers a weekly revisit and matches the image roots' age floor ([`OQ-LS1`](../reference/image-retention.md#why-its-this-way)). 64 leaves room for a few projects' nix-direnv profiles and a profile's recent generations while bounding how many closures a forgotten workspace pins. The cap is a count, not bytes. Renewal is a new request for the same link, never a scan. Constants, not configuration. Reversible: change the constants | 2026-10-08 | [§4.2](#42-the-numbers-and-when-they-apply) | 2026-10-08 |
| <a id="NR-D5"></a>NR-D5 | *Implementation decision.* **The residual window of [Known limit](#known-limit) is accepted.** Closing it would need the producing client itself to wait for yolo (option E), which this design rejects. A lost root costs a rebuild, and the outcome is no worse than the state without the watcher | 2026-10-08 | [Known limit](#known-limit) | — |
| <a id="NR-D6"></a>NR-D6 | *Implementation decision.* **The boot starts the watcher directly, not through `YOLO_JAIL_DAEMONS`.** That payload is the host's composed list of loophole and pack-service daemons, with readiness, orphan and disclosure handling those need. The watcher serves no endpoint and holds no credential, and a jail without it is the jail it was before. Its own lock keeps it to one process per jail; a watcher that dies stays down until the next boot, whose scan catches up. Reversible: compose it into the payload | 2026-10-08 | [§8](#8-what-is-built) | 2026-10-08 |
| <a id="NR-D7"></a>NR-D7 | *Implementation decision.* **The handoff is pinned and fenced on one connection:** `AddTempRoot(P)` when the watcher reads the entry (or `keep` reads the link), the managed link registered on that connection, `AddTempRoot(P)` again after the registration is acknowledged, then close. This is [§2.4](#24-a-new-permanent-root-does-not-update-an-active-gc-snapshot)'s order. The pin starts at discovery, not at the client's own `AddTempRoot`, so it narrows the window but does not close it ([NR-D5](#NR-D5)) | 2026-10-08 | [§4](#4-the-translated-root) | 2026-10-08: the order is pinned against a fake daemon; the real daemon accepted it ([§8](#8-what-is-built)) |

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
